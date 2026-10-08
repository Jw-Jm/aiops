#!/usr/bin/env python3
"""Prepare the exact selected Linux/arm64 Go source and notice closure offline.

License choices are reviewed inputs, not inferred from scanner keywords. Changed
notices, unreviewed modules or selected bytes differing from the authenticated Go
module zip stop preparation. This command does not qualify a component itself.
"""
import argparse
import base64
import hashlib
import io
import json
import os
import re
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile


def objects(raw):
    decoder = json.JSONDecoder()
    while raw.strip():
        raw = raw.lstrip()
        value, end = decoder.raw_decode(raw)
        yield value
        raw = raw[end:]


def digest(value):
    return "sha256:" + hashlib.sha256(value).hexdigest()


def run(*args):
    return subprocess.check_output(args, env={**os.environ, "GOOS": "linux",
        "GOARCH": "arm64", "CGO_ENABLED": "0", "GOPROXY": "off",
        "GOSUMDB": "off", "GOTOOLCHAIN": "local"}, text=True)


def prepare(out, *, review_path=Path("third_party/admission/platform-runtime-go-license-review.json"),
        entrypoints=("./cmd/platform-api", "./cmd/platform-worker", "./cmd/opsctl"),
        admission_paths=(Path("third_party/admission/sp04-selected-runtime.json"),
            Path("third_party/admission/sp05-selected-runtime.json"))):
    review = json.loads(review_path.read_text())
    reviewed = {m["path"]: m for m in review["modules"]}
    packages = list(objects(run("go", "list", "-mod=readonly", "-deps", "-json",
        *entrypoints)))
    modules = {p["Module"]["Path"]: p["Module"] for p in packages
        if p.get("Module") and not p["Module"].get("Main")}
    if set(modules) != set(reviewed):
        raise ValueError("runtime import closure differs from the reviewed module set")
    compiler = run("go", "env", "GOVERSION").strip()
    if compiler != "go1.27.1":
        raise ValueError("runtime source preparation requires locked go1.27.1")
    run("go", "mod", "verify")
    origins = {m["Path"]: m for m in objects(run("go", "mod", "download", "-json",
        *(name+"@"+modules[name]["Version"] for name in sorted(modules))))}
    contents = {}
    locked_modules = []
    for name in sorted(modules):
        origin, decision = origins[name], reviewed[name]
        if decision["version"] != origin["Version"] or origin.get("Error"):
            raise ValueError("unreviewed module version: " + name)
        if not re.fullmatch(r"[0-9a-f]{40}", decision["gitCommit"]) or not decision["gitSource"].startswith("https://"):
            raise ValueError("runtime module requires an exact source repository and full commit: " + name)
        published_origin = origin.get("Origin", {})
        if published_origin.get("Hash") and (published_origin["Hash"] != decision["gitCommit"] or (published_origin.get("URL") and published_origin["URL"] != decision["gitSource"])):
            raise ValueError("reviewed Git identity differs from exact publisher metadata: " + name)
        archive = Path(origin["Zip"])
        root = Path(origin["Dir"])
        selected = set()
        for package in packages:
            if package.get("Module", {}).get("Path") != name:
                continue
            for field in ("GoFiles", "CgoFiles", "SFiles", "CFiles", "HFiles", "SysoFiles", "EmbedFiles"):
                for relative in package.get(field, []):
                    selected.add((Path(package["Dir"])/relative).relative_to(root).as_posix())
        notices = list(decision["notices"])
        overrides = decision.get("subtreeOverrides", [])
        notices.extend(o["notice"] for o in overrides)
        notices = {n["path"]: n for n in notices}
        prefix = name+"@"+origin["Version"]+"/"
        with zipfile.ZipFile(archive) as zip_source:
            # Go's dirhash.HashZip h1 binds archive contents to tracked go.sum.
            h1_input = b"".join((hashlib.sha256(zip_source.read(n)).hexdigest()+"  "+n+"\n").encode()
                for n in sorted(zip_source.namelist()) if not n.endswith("/"))
            h1 = "h1:"+base64.b64encode(hashlib.sha256(h1_input).digest()).decode()
            if h1 != origin["Sum"]:
                raise ValueError("module archive differs from go.sum: " + name)
            files = []
            module_prefix = "modules/"+name+"@"+origin["Version"]+"/"
            for relative in sorted(selected | set(notices) | {"go.mod"}):
                if relative == "go.mod" and not (root/relative).exists():
                    # Legacy modules have publisher metadata in the Go cache.
                    data = Path(origin["GoMod"]).read_bytes()
                    mod_h1 = "h1:"+base64.b64encode(hashlib.sha256(
                        (hashlib.sha256(data).hexdigest()+"  go.mod\n").encode()).digest()).decode()
                    if mod_h1 != origin["GoModSum"]:
                        raise ValueError("legacy publisher go.mod differs from tracked go.sum: " + name)
                else:
                    data = zip_source.read(prefix+relative)
                    if data != (root/relative).read_bytes():
                        raise ValueError("cached source differs from exact publisher zip: " + name+"/"+relative)
                if relative in notices and digest(data) != notices[relative]["digest"]:
                    raise ValueError("publisher notice requires renewed review: " + name+"/"+relative)
                contents[module_prefix+relative] = data
                if relative not in selected:
                    continue
                license_id = decision["codeLicense"]
                for scope in sorted(overrides, key=lambda o: len(o["prefix"])):
                    if relative.startswith(scope["prefix"]):
                        license_id = scope["codeLicense"]
                for scope in decision.get("fileOverrides", []):
                    if relative == scope["path"]:
                        license_id = scope["codeLicense"]
                files.append({"path": relative, "digest": digest(data), "license": license_id})
            locked_modules.append({"path": name, "version": origin["Version"],
                "gitSource": decision["gitSource"], "gitCommit": decision["gitCommit"],
                "source": "https://proxy.golang.org/"+name+"/@v/"+origin["Version"]+".zip",
                "sourceArchiveSHA256": digest(archive.read_bytes()), "goSum": h1,
                "goModSum": origin["GoModSum"],
                "files": files, "notices": list(notices.values())})
    goroot = Path(run("go", "env", "GOROOT").strip())
    standard_files = set()
    for package in packages:
        if not package.get("Standard"):
            continue
        for field in ("GoFiles", "SFiles", "CFiles", "HFiles", "SysoFiles", "EmbedFiles"):
            for relative in package.get(field, []):
                standard_files.add((Path(package["Dir"])/relative).relative_to(goroot).as_posix())
    std_lock = []
    for relative in sorted(standard_files):
        data = (goroot/relative).read_bytes()
        contents["toolchain/"+compiler+"/"+relative] = data
        std_lock.append({"path": relative, "digest": digest(data), "license": "BSD-3-Clause"})
    for relative in ["LICENSE", "VERSION", "src/crypto/internal/boring/LICENSE",
        "src/vendor/golang.org/x/crypto/LICENSE", "src/vendor/golang.org/x/net/LICENSE",
        "src/vendor/golang.org/x/text/LICENSE"]:
        contents["toolchain/"+compiler+"/"+relative] = (goroot/relative).read_bytes()
    # In-tree upstream snippets are not Go modules. Bind their selected bytes,
    # provenance and original notices alongside the module source closure.
    selected_lock = []
    admission_digests = []
    for admission_path in admission_paths:
        admission = json.loads(admission_path.read_text())
        admission_digests.append({"path":str(admission_path),"digest":digest(admission_path.read_bytes())})
        for item in admission["files"]:
            source = Path(item["path"])
            data = source.read_bytes()
            if digest(data) != "sha256:"+item["sha256"]:
                raise ValueError("selected upstream runtime source has drifted: "+str(source))
            contents["selected-upstream/"+str(source)] = data
            selected_lock.append({"path":str(source),"digest":digest(data),"license":item["license"]})
        for item in admission["records"]:
            data = Path(item["path"]).read_bytes()
            if digest(data) != "sha256:"+item["sha256"]: raise ValueError("selected upstream provenance has drifted")
            contents["selected-upstream/"+item["path"]] = data
        contents["selected-upstream/"+str(admission_path)] = admission_path.read_bytes()
    actual_selected = {str((Path(p["Dir"])/f).relative_to(Path.cwd()))
        for p in packages if p.get("Module",{}).get("Main") and "/internal/upstream/" in p.get("Dir","")
        for f in p.get("GoFiles",[])}
    admitted_selected = {item["path"] for item in selected_lock if item["path"].endswith(".go")}
    if actual_selected != admitted_selected:
        raise ValueError("in-tree upstream compiled source differs from the reviewed selected-file closure")
    lock = {"schemaVersion": 1, "architecture": "linux/arm64", "cgoEnabled": False,
        "goVersion": compiler, "licenseReviewSHA256": digest(review_path.read_bytes()),
        "modules": locked_modules, "standardLibraryFiles": std_lock,
        "selectedUpstreamFiles": selected_lock, "selectedUpstreamAdmissions":admission_digests,
        "buildInstructions": "Use go1.27.1, GOOS=linux GOARCH=arm64 CGO_ENABLED=0. Replace all locked modules with their selected local module directories before offline compilation. Only this target is qualified."}
    raw = (json.dumps(lock, indent=2)+"\n").encode()
    contents["runtime-go.lock.json"] = raw
    out.mkdir(parents=True, exist_ok=True)
    (out/"runtime-go.lock.json").write_bytes(raw)
    # An uncompressed deterministic tar avoids compressor-dependent identities.
    with tarfile.open(out/"runtime-go-source.tar", "w") as archive:
        for name, data in sorted(contents.items()):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), 0o644, 0
            archive.addfile(info, io.BytesIO(data))
    return lock, digest((out/"runtime-go-source.tar").read_bytes())


def check_current(out):
    lock, source_digest = prepare(out)
    expected = json.loads(Path("bundle/evidence/third_party/admission/platform-runtime-go.lock.json").read_text())
    if lock != expected:
        raise ValueError("selected runtime source, module or license closure has drifted; renew qualification evidence")
    catalog = Path("bundle/component-catalog.yaml").read_text()
    entry = re.split(r"(?m)^  - name:", catalog.split("  - name: opa-sdk\n", 1)[1], maxsplit=1)[0]
    if "    correspondingSourceBundleSHA256: "+source_digest+"\n" not in entry:
        raise ValueError("runtime source archive differs from the qualified Catalog lock")
    return lock, source_digest


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    if args.check:
        with tempfile.TemporaryDirectory(prefix="ops-runtime-source-check-") as directory:
            lock, source_digest = check_current(Path(directory))
    elif args.out:
        lock, source_digest = prepare(args.out)
    else:
        parser.error("--out or --check is required")
    print(json.dumps({"modules": len(lock["modules"]), "selectedModuleFiles": sum(len(m["files"]) for m in lock["modules"]),
        "standardLibraryFiles": len(lock["standardLibraryFiles"]), "sourceBundleSHA256": source_digest}))
