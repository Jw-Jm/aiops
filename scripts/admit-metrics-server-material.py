#!/usr/bin/env python3
"""Admit only the measured v0.8.0 arm64 publisher material closure.

This connected preparation command never installs a workload or claims R3/R4
acceptance. Original sources remain outside the repository; notices and finite
review bindings are embedded independently of the signed input Catalog.
"""
import argparse
import hashlib
import json
import shutil
import tarfile
from pathlib import Path

import yaml

parser = argparse.ArgumentParser()
parser.add_argument("--materials", type=Path, required=True)
parser.add_argument("--verify-only", action="store_true", help="verify every admission output without modifying it")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
materials = args.materials.resolve()
image = "sha256:8f49cf1b0688bb0eae18437882dbf6de2c7a2baac71b1492bc4eca25439a1bf2"
commit = "d66279c6426c6581d9656fe3d42bc52db7c29597"
source_hash = "sha256:4dc2060e33613c35ae0f7c48aba1b813d986d82049f641966fd9eb5eefc49864"
adr = "docs/adr/0029-pre-sp07-metrics-server-distribution.md"
evidence_dir = root / "docs/evidence/pre-sp07-20261005"


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def load(path):
    return json.loads(path.read_text())


source = materials / "source.tar.gz"
assert digest(source.read_bytes()) == source_hash
complete_source = materials / "closure-r4/metrics-server-complete-source.tar.gz"
complete_hash = digest(complete_source.read_bytes())
preparation = load(evidence_dir / "metrics-complete-source-preparation-r4.json")
assert preparation["sourceBundleSHA256"] == complete_hash
assert preparation["selectedOCIManifestDigest"] == image
assert preparation["materialArchiveReadbackVerified"]
bindings = load(evidence_dir / "metrics-go-image-source-binding-r6.json")
assert len(bindings["verifiedRecords"]) == 95 and not bindings.get("failedRecords")
assert all(record["zipH1MatchesImage"] for record in bindings["verifiedRecords"])
bound_modules = {record["module"]: record for record in bindings["verifiedRecords"]}
build = load(evidence_dir / "metrics-offline-source-build-r6.json")
assert build["exitCode"] == 0 and build["network"] == "none"
assert build["initialBuildCacheEmpty"] and build["modulesRestoredFromExactZIPs"] == 95
with tarfile.open(complete_source) as archive:
    lock = json.loads(archive.extractfile("complete-source.lock.json").read())
    assert lock["imageManifestDigest"] == image and lock["sourceCommit"] == commit
    for row in lock["sourceFiles"]:
        assert not row["path"].startswith("/") and ".." not in Path(row["path"]).parts
        assert digest(archive.extractfile(row["path"]).read()) == "sha256:" + row["sha256"]

review = load(materials / "go-source-license-review.json")
inventory = load(materials / "source-inventory.json")
assert len(review["modules"]) == 97 and inventory["unreviewedGoLicenses"] == []
assert digest((materials / "image.syft.json").read_bytes()) == "sha256:" + inventory["sbomSHA256"]
licenses, dependencies, finite = [], [], []
outputs = {}
native_notices = []


def retain_notice(relative, data, permission):
    outputs[relative] = data
    record = dict(path=relative, license=permission, digest=digest(data))
    if record not in licenses:
        licenses.append(record)
    return record


for module in review["modules"]:
    assert digest(Path(module["file"]).read_bytes()) == "sha256:" + module["sha256"]
    if module["module"] not in ("stdlib", "sigs.k8s.io/metrics-server"):
        measured = bound_modules[module["module"]]
        assert module["version"] == measured["version"]
        assert module["sha256"] == measured["moduleZIP_SHA256"]
    elif module["module"] == "stdlib":
        assert module["version"] == "go1.24.4"
        assert module["sha256"] == "5a86a83a31f9fa81490b8c5420ac384fd3d95a3e71fba665c7b3f95d1dfef2b4"
    permission = " AND ".join(sorted(module["compiledLinuxARM64Licenses"]))
    if module["module"] != "sigs.k8s.io/metrics-server":
        dependencies.append(dict(name="go:" + module["module"], version=module["version"],
                                 source=module["source"], sourceType="archive",
                                 digest="sha256:" + module["sha256"],
                                 sourceArchiveSHA256="sha256:" + module["sha256"], license=permission))
    for notice in module["originalNotices"]:
        # Compiler-only vendors retain their notices in complete source. Do not
        # misdescribe them as linked Metrics-server standard-library code.
        if module["module"] == "stdlib" and notice["path"] != "go/LICENSE":
            continue
        original = materials / "original-go-notices" / (notice["sha256"] + ".txt")
        data = original.read_bytes()
        assert digest(data) == "sha256:" + notice["sha256"]
        retain_notice("third_party/licenses/metrics-server/go/" + original.name, data, permission)

ca = load(materials / "ca-certificates-source-binding.json")
assert len(ca) == 1 and ca[0]["match"] and ca[0]["certificateSetsByteIdentical"]
assert ca[0]["originalCopyrightByteIdentical"] and ca[0]["imageCertCount"] == 142
packages = [(package["name"], package["version"], package["files"])
            for package in inventory["nativePackages"]]
packages.append(("ca-certificates", ca[0]["version"],
                 [dict(name=Path(f["file"]).name, url=f["source"], sha256=f["sha256"])
                  for f in ca[0]["correspondingSourceFiles"]]))
assert {(name, version) for name, version, _ in packages} == {
    ("base-files", "12.4+deb12u11"), ("netbase", "6.4"),
    ("tzdata", "2025b-0+deb12u1"), ("ca-certificates", "20230311+deb12u1")}
adr_hash = digest((root / adr).read_bytes())
for name, version, files in packages:
    for source_file in files:
        if name == "ca-certificates":
            original_source = materials / "ca-certificates" / source_file["name"]
        else:
            original_source = materials / "debian-sources/packages" / (name + "_" + version) / source_file["name"]
        assert digest(original_source.read_bytes()) == "sha256:" + source_file["sha256"]
    publisher = (materials / "image-notices/usr/share/doc" / name / "copyright").read_bytes()
    aggregate = b"Exact unchanged publisher copyright; original terms govern their stated files.\n" + publisher
    for notice in sorted((materials / "image-notices/usr/share/common-licenses").iterdir()):
        aggregate += b"\nOriginal publisher common-license member: " + notice.name.encode() + b"\n" + notice.read_bytes()
    notice_hash = digest(aggregate)
    identifier = "LicenseRef-PreSP07-Metrics-" + hashlib.sha256(
        (name + "@" + version + image + complete_hash + notice_hash).encode()).hexdigest()
    relative = "third_party/licenses/metrics-server/native/" + name + "/publisher-notices.txt"
    retain_notice(relative, aggregate, identifier)
    descriptor = next(f for f in files if f["name"].endswith(".dsc"))
    dependencies.append(dict(name="native:" + name, version=version, source=descriptor["url"],
                             digest="sha256:" + descriptor["sha256"], license=identifier,
                             sourceType="archive", sourceArchiveSHA256="sha256:" + descriptor["sha256"]))
    finite.append(dict(id=identifier, component="metrics-server", componentVersion="v0.8.0",
                       imageDigest=image, dependencyName="native:" + name, version=version,
                       source=descriptor["url"], sourceArchiveSHA256="sha256:" + descriptor["sha256"],
                       correspondingSourceBundleSHA256=complete_hash, noticePath=relative,
                       noticeDigest=notice_hash, adr=adr, adrDigest=adr_hash))
    native_notices.append(dict(package=name, version=version, sourceFiles=files,
                               originalCopyrightSHA256=digest(publisher), finiteLicenseRef=identifier))
assert len(dependencies) == 100 and len(finite) == 4

fixtures = ["docs/evidence/pre-sp07-20261004/metrics-apiservice-live.json",
            "docs/evidence/pre-sp07-20261004/metrics-nodes-live.json",
            "docs/evidence/pre-sp07-20261004/metrics-owned-pods-live.json",
            "docs/evidence/pre-sp07-20261005/metrics-offline-source-build-r6.json"]
section = dict(name="metrics-server", state="qualified", version="v0.8.0",
               source="https://github.com/kubernetes-sigs/metrics-server", commit=commit, digest=image,
               license="Apache-2.0", specialLicenseADR=adr, fileLicenses=licenses,
               sourceSnapshot=True, sourceArchiveSHA256=source_hash,
               correspondingSourceBundleSHA256=complete_hash, architectures=["linux/arm64"],
               usage="Bounded native Node/Pod metrics observations; symptoms only; full R3/R4 gates separate",
               reuseMode="process-isolated", linkageMode="unchanged-upstream-process",
               importedPaths=["cmd/metrics-server", "pkg"], dependencyClosure=dependencies,
               dependencyClosureVerified=True, patches=[], forkPolicy="not-applicable",
               owner="pre-SP07 R3", pocReport="docs/poc/pre-sp07-metrics-server-v0.8.0.md",
               conformanceFixtures=fixtures, exitPlan="Disable optional metrics observations; retain Evidence and Audit",
               requiredFor1_0=True, officialSupportSources=[
                   "https://github.com/kubernetes-sigs/metrics-server/tree/" + commit])
catalog = root / "bundle/component-catalog.yaml"
text = catalog.read_text()
end = text.index("  - name: deepflow\n")
new = "".join("  " + line + "\n" for line in yaml.safe_dump([section], sort_keys=False, width=150).splitlines())
marker = "  - name: metrics-server\n"
if marker in text:
    start = text.index(marker)
    assert text[start:end] == new, "existing Metrics admission differs; review and explicitly update it"
    candidate_catalog = text
else:
    assert not args.verify_only, "Metrics admission is not yet installed"
    candidate_catalog = text[:end] + new + text[end:]
outputs["bundle/component-catalog.yaml"] = candidate_catalog.encode()
outputs["internal/supplychain/licenses/pre-sp07-metrics-reviewed.json"] = (
    json.dumps(dict(schemaVersion=1, licenses=finite), indent=2) + "\n").encode()
admission = dict(schemaVersion=1, component="metrics-server", version="v0.8.0", imageDigest=image,
                 sourceCommit=commit, sourceArchiveSHA256=source_hash,
                 correspondingSourceBundleSHA256=complete_hash, externalGoModules=95,
                 compiler="go1.24.4", nativePublisherNotices=native_notices,
                 sourceBuildEvidence=fixtures[-1], fullR3Acceptance=False, signedAddonAccepted=False)
admission_path = root / "third_party/admission/pre-sp07-metrics-server.json"
outputs[str(admission_path.relative_to(root))] = (json.dumps(admission, indent=2) + "\n").encode()
for relative in [adr, section["pocReport"], str(admission_path.relative_to(root))] + fixtures + [n["path"] for n in licenses]:
    outputs["bundle/evidence/" + relative] = outputs.get(relative, (root / relative).read_bytes())
# All source and license checks above finish before any output changes. A
# read-only verification also catches edited notices or stale embedded evidence.
for relative, data in outputs.items():
    target = root / relative
    if args.verify_only:
        assert target.is_file() and target.read_bytes() == data, "admission output differs: " + relative
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
print("Metrics exact distribution admission verified" if args.verify_only else "Metrics exact distribution admission prepared",
      len(dependencies), "dependencies,", len(licenses), "notices; full R3/R4 separate")
