"""Prepare local Task 2.7 inputs from reviewed sources and cached images.

No image pull or networked build. Original upstream index metadata may be
retrieved during preparation only, at the exact immutable catalog digest.
Requires PyYAML 6.0.3 and the independently
prepared pinned Syft tool. Output goes to ignored artifacts, not application
runtime. The Bundle builder independently checks every digest and OCI layer.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile

import yaml

ROOT = Path.cwd()
OUT = ROOT / "artifacts/task27-materials/core-inputs"
SYFT = Path("/tmp/ops-task27-tools/syft")
NAMES = ("postgresql", "keycloak", "seaweedfs", "openbao", "victoria-metrics", "victoria-logs", "vmalert")
REPOS = {"postgresql":"docker.io/library/postgres", "keycloak":"quay.io/keycloak/keycloak", "seaweedfs":"docker.io/chrislusf/seaweedfs", "openbao":"ghcr.io/openbao/openbao", "victoria-metrics":"docker.io/victoriametrics/victoria-metrics", "victoria-logs":"docker.io/victoriametrics/victoria-logs", "vmalert":"docker.io/victoriametrics/vmalert"}
ENV = {**os.environ, "GOPROXY":"off", "GOSUMDB":"off", "GOTOOLCHAIN":"local", "CGO_ENABLED":"0", "GOOS":"linux", "GOARCH":"arm64", "SYFT_CHECK_FOR_APP_UPDATE":"false", "SYFT_GOLANG_SEARCH_REMOTE_LICENSES":"false"}


def run(args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, env=ENV, **kwargs).stdout


def sha(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for b in iter(lambda:f.read(1<<20),b""): h.update(b)
    return "sha256:" + h.hexdigest()


def chart_spdx(payload, name, version, source, license):
    return {"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT",
        "name":name+" chart","documentNamespace":"https://ops.local/sbom/"+name+"/"+sha(payload).split(":")[1],
        "creationInfo":{"creators":["Tool: ops-task27-chart-inventory"],"created":"2026-09-29T00:00:00Z"},
        "packages":[{"SPDXID":"SPDXRef-Chart","name":name,"versionInfo":version,"downloadLocation":source,
        "filesAnalyzed":False,"checksums":[{"algorithm":"SHA256","checksumValue":sha(payload).split(":")[1]}],
        "licenseConcluded":license,"licenseDeclared":license,"copyrightText":"Original publisher notices and chart sources accompany archive"}],
        "relationships":[{"spdxElementId":"SPDXRef-DOCUMENT","relationshipType":"DESCRIBES","relatedSpdxElement":"SPDXRef-Chart"}]}


def export(name, reference, expected=None):
    raw, target = OUT / (name + "-raw.tar"), OUT / (name + ".tar")
    run(["docker","--context","orbstack","image","inspect",reference])
    run(["docker","--context","orbstack","image","save","--platform","linux/arm64","--output",str(raw),reference])
    with tarfile.open(raw) as src:
        members = {m.name.removeprefix("./"):m for m in src if m.isfile()}
        def read(path): return src.extractfile(members[path]).read()
        index = json.loads(read("index.json"))
        def leaf(d, expected_digest=expected):
            data = json.loads(read("blobs/sha256/" + d["digest"].split(":")[1]))
            if "manifests" in data:
                for child in data["manifests"]:
                    result = leaf(child, expected_digest)
                    if result: return result
                return None
            config = json.loads(read("blobs/sha256/" + data["config"]["digest"].split(":")[1]))
            if config.get("os") != "linux" or config.get("architecture") != "arm64": return None
            if expected_digest and d["digest"] != expected_digest: return None
            return d
        selected = next((d for item in index["manifests"] if (d:=leaf(item))),None)
        original_blob = None
        if not selected and expected:
            # Docker --platform export trims a multi-platform index and changes
            # its digest. Restore the exact original index, never a fabricated
            # alias. Native manifest/config/layers still come from the cache.
            original_path=OUT/(name+"-original-manifest.json")
            if not original_path.exists():
                original_path.write_bytes(run(["docker","--context","orbstack","buildx","imagetools","inspect","--raw",reference]))
            original_blob=original_path.read_bytes()
            if "sha256:"+hashlib.sha256(original_blob).hexdigest()!=expected: raise RuntimeError("original registry index digest differs")
            original=json.loads(original_blob)
            matching=[d for d in original.get("manifests",[]) if d.get("platform",{}).get("os")=="linux" and d.get("platform",{}).get("architecture")=="arm64"]
            if len(matching)!=1 or not leaf(matching[0], matching[0]["digest"]): raise RuntimeError("native closure does not match original upstream index")
            selected={"mediaType":original["mediaType"],"digest":expected,"size":len(original_blob)}
        if not selected: raise RuntimeError("expected exact arm64 image manifest absent: " + name)
        repo = REPOS.get(name, "ops.local/task27/" + name)
        immutable = repo + "@" + selected["digest"]
        selected["annotations"] = {"org.opencontainers.image.ref.name":immutable}
        selected["platform"] = {"os":"linux","architecture":"arm64"}
        canonical_index = json.dumps({"schemaVersion":2,"manifests":[selected]},separators=(",",":")).encode()
        with tarfile.open(target,"w") as dst:
            if original_blob is not None:
                entry=tarfile.TarInfo("blobs/sha256/"+expected.split(":")[1]);entry.mode=0o644;entry.size=len(original_blob);dst.addfile(entry,io.BytesIO(original_blob))
            for path in sorted(members):
                if path not in ("index.json","oci-layout") and not path.startswith("blobs/sha256/"): continue
                m=members[path]
                entry=tarfile.TarInfo(path); entry.mode=0o644; entry.mtime=0
                if path=="index.json": entry.size=len(canonical_index);dst.addfile(entry,io.BytesIO(canonical_index))
                else: entry.size=m.size;dst.addfile(entry,src.extractfile(m))
    raw.unlink()
    return target,immutable,selected["digest"]


def first_party_notices(binary, target):
    # Only the modules actually linked into this binary, not the entire module
    # graph. Read original notices from cached preferred source directories.
    info=run(["go","version","-m",str(binary)]).decode()
    goroot=run(["go","env","GOROOT"]).decode().strip()
    text="First-party ops-platform development binary; upstream notices follow.\n" + info + "\nGo toolchain license:\n" + Path(goroot,"LICENSE").read_text()
    for line in info.splitlines():
        parts=line.strip().split()
        if not parts or parts[0]!="dep": continue
        meta=json.loads(run(["go","list","-m","-json",parts[1]+"@"+parts[2]]))
        directory=Path(meta["Dir"])
        notices=[p for p in directory.iterdir() if p.is_file() and p.name.upper().startswith(("LICENSE","COPYING","NOTICE","COPYRIGHT"))]
        if not notices: raise RuntimeError("linked module original notice missing: " + parts[1])
        for p in notices: text += "\n===== " + parts[1]+"@"+parts[2]+"/"+p.name+" =====\n" + p.read_text()
    # Selected in-tree upstream snippets are not Go modules; preserve their
    # original license/notice text in every distributed first-party artifact.
    for notice in sorted((ROOT/"internal/upstream").rglob("*")):
        if notice.is_file() and notice.name in ("LICENSE", "NOTICE", "COPYING"):
            text += "\n===== selected source " + str(notice.relative_to(ROOT)) + " =====\n" + notice.read_text()
    target.write_text(text)


def main():
    global OUT
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path)
    parser.add_argument("--reuse-reviewed-spec", type=Path)
    parser.add_argument("--sp05-worker-material", type=Path, help="Exact prepared unchanged Analyzer and source material directory")
    parser.add_argument("--bundle-id", default="task27-core-arm64-20260929")
    args = parser.parse_args()
    if args.out:
        OUT = args.out.resolve()
        if OUT.exists(): raise RuntimeError("new preparation output must not already exist")
    OUT.mkdir(parents=True,exist_ok=True)
    catalog={c["name"]:c for c in yaml.safe_load(Path("bundle/component-catalog.yaml").read_text())["components"]}
    audit={c["component"]:c for c in json.loads(Path("third_party/admission/task-2.7-core-image-sbom-audit.json").read_text())["components"]}
    spec={"schemaVersion":1,"bundleId":args.bundle_id,"platformVersion":"1.0.0","architecture":"linux/arm64","files":[],"materials":[]}
    def file(path,source,kind):
        row={"path":path,"source":str(Path(source).resolve()),"kind":kind,"digest":sha(source),"size":Path(source).stat().st_size}
        spec["files"].append(row);return row["digest"]
    def material(name,kind,version,payload,sbom,license,payloadkind):
        payloadroot={"chart":"charts","binary":"binaries","source":"sources","oci":"oci"}[payloadkind]
        refs=(payloadroot+"/"+Path(payload).name,"sbom/"+name+".json","licenses/"+name+".txt")
        digest=file(refs[0],payload,payloadkind);file(refs[1],sbom,"sbom");file(refs[2],license,"license")
        spec["materials"].append({"name":name,"kind":kind,"version":str(version),"architecture":"linux/arm64","digest":digest,"payloadRef":refs[0],"sbomRef":refs[1],"licenseRef":refs[2],"installAfter":[]})
    if args.reuse_reviewed_spec:
        previous = json.loads(args.reuse_reviewed_spec.read_text())
        names = set(NAMES) | {n+"-source" for n in NAMES}
        spec["materials"] = [m for m in previous["materials"] if m["name"] in names]
        if {m["name"] for m in spec["materials"]} != names: raise RuntimeError("reviewed core source/image material set is incomplete")
        refs = {m[k] for m in spec["materials"] for k in ("payloadRef", "sbomRef", "licenseRef")}
        spec["files"] = [f for f in previous["files"] if f["path"] in refs]
        if {f["path"] for f in spec["files"]} != refs: raise RuntimeError("reviewed core material references are incomplete")
        for f in spec["files"]:
            source = Path(f["source"])
            if not source.is_absolute(): source = args.reuse_reviewed_spec.parent/source
            if sha(source) != f["digest"] or source.stat().st_size != f["size"]: raise RuntimeError("reviewed material changed: "+f["path"])
            f["source"] = str(source.resolve())
    for name in (() if args.reuse_reviewed_spec else NAMES):
        c=catalog[name]
        if c["state"]!="qualified": raise RuntimeError("candidate: "+name)
        payload,reference,image_digest=export(name,REPOS[name]+"@"+c["digest"],c["digest"])
        a=audit[name]
        if sha(a["spdxFile"])!="sha256:"+a["spdxSHA256"]: raise RuntimeError("raw SPDX evidence changed")
        license=OUT/(name+"-notices.txt")
        text=Path("third_party/licenses/corresponding-source-instructions.md").read_text()+"\n"+Path(c["specialLicenseADR"]).read_text()
        for n in c["fileLicenses"]:
            if sha(n["path"])!=n["digest"]: raise RuntimeError("catalog original notice changed")
            text+="\n===== "+n["path"]+" / "+n["license"]+" =====\n"+Path(n["path"]).read_text()
        if name=="keycloak": text+="\n"+Path("third_party/licenses/keycloak/third-party-notice-26.7.4.html").read_text()
        license.write_text(text)
        material(name,"container-image",c["version"],payload,a["spdxFile"],license,"oci")
        source=ROOT/"artifacts/task27-materials/corresponding-sources"/(name+"-source.tar")
        if sha(source)!=c["correspondingSourceBundleSHA256"]: raise RuntimeError("source Bundle differs from catalog")
        source_sbom=OUT/(name+"-source-sbom.json")
        source_sbom.write_text(json.dumps({"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":name+" corresponding source", "documentNamespace":"https://ops.local/sbom/"+source.name+"/"+sha(source).split(":")[1],"creationInfo":{"creators":["Tool: ops-task27-source-inventory"],"created":"2026-09-29T00:00:00Z"},"files":[{"SPDXID":"SPDXRef-SourceBundle","fileName":source.name,"checksums":[{"algorithm":"SHA256","checksumValue":sha(source).split(":")[1]}],"licenseConcluded":"NOASSERTION","licenseInfoInFiles":["NOASSERTION"],"copyrightText":"Original publisher notices accompany all sources"}]}))
        material(name+"-source","source",c["version"],source,source_sbom,license,"source")
        print("prepared exact image and source",name,reference,flush=True)
    for name,cmd in (("platform-api","platform-api"),("platform-worker","platform-worker"),("opsctl","opsctl")):
        binary=OUT/(name+"-binary")
        run(["go","build","-trimpath","-buildvcs=false","-ldflags=-s -w","-o",str(binary),"./cmd/"+cmd])
        license=OUT/(name+"-notices.txt");first_party_notices(binary,license)
        if name=="platform-worker" and args.sp05_worker_material:
            analyzer=args.sp05_worker_material/"k8sgpt"
            if sha(analyzer)!="sha256:6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53":raise RuntimeError("Worker Analyzer executable drift")
            with tarfile.open(args.sp05_worker_material/"k8sgpt-runtime-source.tar") as original:
                text=license.read_text()
                for member in original.getmembers():
                    if member.isfile() and member.name.startswith("notices/"):
                        text+="\n===== unchanged CLI "+member.name+" =====\n"+original.extractfile(member).read().decode()
                license.write_text(text)
        if name=="opsctl": payload=binary;kind="binary";payloadkind="binary";scan="file:"+str(binary)
        else:
            context=OUT/(name+"-context");context.mkdir(exist_ok=True)
            shutil.copyfile(binary,context/"ops-process")
            notices=context/"runtime-notices";notices.mkdir()
            shutil.copyfile(license,notices/"FIRST-PARTY-NOTICES.txt")
            dockerfile=ROOT/"build/images/offline-runtime.Dockerfile"
            if name=="platform-worker" and args.sp05_worker_material:
                shutil.copyfile(args.sp05_worker_material/"k8sgpt",context/"k8sgpt")
                shutil.copytree(args.sp05_worker_material/"runtime-notices",notices,dirs_exist_ok=True)
                dockerfile=ROOT/"build/images/offline-sp05-worker.Dockerfile"
            run(["docker","--context","orbstack","build","--network=none","--pull=false","--provenance=false","--platform=linux/arm64","--file",str(dockerfile),"--tag","ops.local/"+args.bundle_id+"/"+name+":1.0.0",str(context)])
            payload,reference,image_digest=export(name,"ops.local/"+args.bundle_id+"/"+name+":1.0.0")
            kind="container-image";payloadkind="oci";scan="oci-archive:"+str(payload)
        sbom=OUT/(name+".spdx.json")
        document=json.loads(run([str(SYFT),scan,"-o","spdx-json"]))
        # Syft's Go module inventory cannot see copied ontology/check/model
        # source. Include the exact selected source inventory explicitly.
        selected={"files":[]}
        for admission in ["sp04-selected-runtime.json","sp05-selected-runtime.json"]:
            selected["files"]+=json.loads((ROOT/"third_party/admission"/admission).read_text())["files"]
        document.setdefault("files",[])
        for index, f in enumerate(selected["files"]):
            document["files"].append({"SPDXID":"SPDXRef-SP04Selected-"+str(index),"fileName":f["path"],"checksums":[{"algorithm":"SHA256","checksumValue":f["sha256"]}],"licenseConcluded":f["license"],"licenseInfoInFiles":[f["license"]],"copyrightText":"Original source headers and notices accompany the selected-upstream source archive"})
        sbom.write_text(json.dumps(document))
        material(name,kind,"1.1.0" if name=="platform-worker" and args.sp05_worker_material else "1.0.0",payload,sbom,license,payloadkind)
        print("prepared local first-party",name,flush=True)
    for name in ("ops-platform","ops-dependencies"):
        out=run(["helm","package","deploy/charts/"+name,"--destination",str(OUT)]).decode()
        payload=OUT/(name+"-0.1.0.tgz")
        sbom=OUT/(name+"-chart-sbom.json");sbom.write_text(json.dumps(chart_spdx(payload,name,"0.1.0","NOASSERTION","NOASSERTION")))
        license=OUT/(name+"-chart-notices.txt");license.write_text("First-party ops-platform Helm chart; sources accompany repository.\n")
        material(name+"-chart","chart","0.1.0",payload,sbom,license,"chart")
    for name in ("victoria-metrics","victoria-logs","vmalert"):
        c=catalog[name];lock=c["chartLock"]
        payload=ROOT/"deploy/addons/victoria/charts"/Path(lock["source"]).name
        if sha(payload)!=lock["digest"]: raise RuntimeError("upstream Chart archive differs")
        sbom=OUT/(name+"-chart-sbom.json");sbom.write_text(json.dumps(chart_spdx(payload,lock["name"],lock["version"],lock["source"],"Apache-2.0")))
        chart_license = OUT/(name+"-notices.txt")
        if args.reuse_reviewed_spec:
            chart_license = Path(next(f["source"] for f in spec["files"] if f["path"] == "licenses/"+name+".txt"))
        material(name+"-chart","chart",lock["version"],payload,sbom,chart_license,"chart")
    # Qualify the actual SDK runtime closure separately from the candidate
    # standalone OPA image. Its full notices accompany every compiled file.
    runtime = OUT/"runtime-source"
    run(["python3", "scripts/prepare-runtime-source.py", "--out", str(runtime)])
    lock = json.loads((runtime/"runtime-go.lock.json").read_text())
    sdk = catalog["opa-sdk"]
    source = runtime/"runtime-go-source.tar"
    if sdk["state"] != "qualified" or sha(source) != sdk["correspondingSourceBundleSHA256"]: raise RuntimeError("runtime source is not the qualified SDK closure")
    sbom = OUT/"runtime-source.spdx.json"
    entries = []
    for module in lock["modules"]:
        for f in module["files"]:
            entries.append({"SPDXID":"SPDXRef-File-"+str(len(entries)),"fileName":"modules/"+module["path"]+"@"+module["version"]+"/"+f["path"],"checksums":[{"algorithm":"SHA256","checksumValue":f["digest"].split(":")[1]}],"licenseConcluded":f["license"],"licenseInfoInFiles":[f["license"]],"copyrightText":"Original source headers and notices accompany this file"})
    for f in lock["standardLibraryFiles"]:
        entries.append({"SPDXID":"SPDXRef-File-"+str(len(entries)),"fileName":"toolchain/"+lock["goVersion"]+"/"+f["path"],"checksums":[{"algorithm":"SHA256","checksumValue":f["digest"].split(":")[1]}],"licenseConcluded":f["license"],"licenseInfoInFiles":[f["license"]],"copyrightText":"Original source headers and notices accompany this file"})
    for f in lock["selectedUpstreamFiles"]:
        entries.append({"SPDXID":"SPDXRef-File-"+str(len(entries)),"fileName":"selected-upstream/"+f["path"],"checksums":[{"algorithm":"SHA256","checksumValue":f["digest"].split(":")[1]}],"licenseConcluded":f["license"],"licenseInfoInFiles":[f["license"]],"copyrightText":"Original source headers and notices accompany this file"})
    sbom.write_text(json.dumps({"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":"Selected Go runtime source closure","documentNamespace":"https://ops.local/sbom/runtime-source/"+sha(source).split(":")[1],"creationInfo":{"creators":["Tool: ops-selected-runtime-lock"],"created":"2026-10-01T00:00:00Z"},"files":entries}))
    notices = OUT/"runtime-source-notices.txt"
    with tarfile.open(source) as archive:
        text = "Selected SDK/runtime sources: exact original notices below and alongside their source files.\n"
        for module in lock["modules"]:
            for notice in module["notices"]:
                name = "modules/"+module["path"]+"@"+module["version"]+"/"+notice["path"]
                text += "\n===== "+name+" =====\n" + archive.extractfile(name).read().decode()
        text += "\n===== Go LICENSE =====\n" + archive.extractfile("toolchain/"+lock["goVersion"]+"/LICENSE").read().decode()
        for f in lock["selectedUpstreamFiles"]:
            if Path(f["path"]).name in ("LICENSE","NOTICE","COPYING"):
                text += "\n===== selected source "+f["path"]+" =====\n" + archive.extractfile("selected-upstream/"+f["path"]).read().decode()
    notices.write_text(text)
    material("opa-sdk-source", "source", sdk["version"], source, sbom, notices, "source")
    if args.sp05_worker_material:
        cli=catalog["k8sgpt"]
        source=args.sp05_worker_material/"k8sgpt-runtime-source.tar"
        if cli["state"]!="qualified" or sha(source)!=cli["correspondingSourceBundleSHA256"]:raise RuntimeError("fixed Analyzer corresponding source differs from Catalog")
        cli_lock=json.loads((args.sp05_worker_material/"k8sgpt-runtime-source.lock.json").read_text())
        packages=[]
        for index,m in enumerate(cli_lock["modules"]):
            packages.append({"SPDXID":"SPDXRef-Module-"+str(index),"name":m["path"],"versionInfo":m["version"],"downloadLocation":"https://proxy.golang.org/"+m["path"]+"/@v/"+m["version"]+".zip","checksums":[{"algorithm":"SHA256","checksumValue":m["artifactSHA256"].split(":")[1]}],"licenseConcluded":m["license"],"licenseDeclared":m["license"],"filesAnalyzed":False,"copyrightText":"Complete original source and notices accompany this unchanged publisher artifact"})
        cli_sbom=OUT/"k8sgpt-source.spdx.json"
        cli_sbom.write_text(json.dumps({"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":"Unchanged K8sGPT complete source closure","documentNamespace":"https://ops.local/sbom/k8sgpt-source/"+sha(source).split(":")[1],"creationInfo":{"creators":["Tool: ops-fixed-cli-source-lock"],"created":"2026-10-03T00:00:00Z"},"packages":packages}))
        cli_notices=OUT/"k8sgpt-source-notices.txt"
        with tarfile.open(source) as original:
            text="Unchanged no-LLM CLI source distribution. Complete publisher ZIP artifacts (including MPL source) accompany this material.\n"
            for m in original.getmembers():
                if m.isfile() and (m.name.startswith("notices/") or m.name=="toolchain/go1.27.1/LICENSE"):
                    text+="\n===== "+m.name+" =====\n"+original.extractfile(m).read().decode()
        cli_notices.write_text(text)
        material("k8sgpt-source","source",cli["version"],source,cli_sbom,cli_notices,"source")
    head = run(["git", "rev-parse", "HEAD"]).decode().strip()
    if run(["git", "status", "--porcelain"]).strip(): raise RuntimeError("current first-party sources must be committed before preparation")
    binding={"sourceCommit":head,"runtimeSourceDigest":sha(runtime/"runtime-go-source.tar"),"buildSpecBundleID":args.bundle_id,"goVersion":run(["go","version"]).decode().strip(),"network":"Go proxy/sumdb off; Docker build network none; no pull","target":"linux/arm64 CGO_ENABLED=0"}
    if args.sp05_worker_material:
        binding["analyzerSourceDigest"]=sha(args.sp05_worker_material/"k8sgpt-runtime-source.tar")
        binding["analyzerBinaryDigest"]=sha(args.sp05_worker_material/"k8sgpt")
    (OUT/"source-binding.json").write_text(json.dumps(binding,indent=2))
    (OUT/"build-spec.json").write_text(json.dumps(spec,indent=2)+"\n")
    print("local inputs complete",len(spec["materials"]),"materials",len(spec["files"]),"authenticated files",flush=True)


if __name__=="__main__": main()
