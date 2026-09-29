"""Preserve exact native publisher notices and package/source correspondence.

The output is a review input, never automatic permission or qualification.
RPM spec files are read as data; no package script is executed.
"""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tarfile

ROOT = Path("artifacts/task27-materials")
OUT = ROOT / "license-review/native-notices"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    records = []
    debian = json.loads((ROOT / "debian-sources/source-package-lock.json").read_text())["packages"]
    rpm = json.loads((ROOT / "keycloak-sources/source-rpm-lock.json").read_text())["sourceRPMs"]
    alpine = json.loads((ROOT / "alpine-sources/source-package-lock.json").read_text())["packages"]
    rpm_specs = {}
    for source in rpm:
        path = ROOT / "keycloak-sources" / source["file"]
        if digest(path.read_bytes()) != source["sha256"]:
            raise RuntimeError("source RPM changed")
        names = subprocess.check_output(["tar", "-tf", str(path)], text=True).splitlines()
        specs = [name for name in names if name.endswith(".spec") and "/" not in name]
        if len(specs) != 1:
            raise RuntimeError("source RPM spec is ambiguous: " + str(path))
        text = subprocess.check_output(["tar", "-xOf", str(path), specs[0]])
        rpm_specs[Path(source["file"]).name] = (source, specs[0], text)

    for component in ("postgresql", "victoria-logs", "keycloak", "seaweedfs", "openbao", "victoria-metrics", "vmalert"):
        suffix = ".syft.json" if component in ("postgresql", "keycloak") else ".enriched.syft.json"
        sbom = json.loads((ROOT / "sbom" / (component + suffix)).read_text())
        for package in sbom["artifacts"]:
            kind = package["type"]
            if kind not in ("deb", "rpm", "apk"):
                continue
            name, version = package["name"], package["version"]
            metadata = package["metadata"]
            source_files, notice_files, labels = [], [], []
            if kind == "deb":
                source_name = metadata.get("source") or name
                source_version = metadata.get("sourceVersion") or version
                source = next(p for p in debian if p["name"] == source_name and p["version"] == source_version)
                for file in source["files"]:
                    path = ROOT / "debian-sources/packages" / (source_name + "_" + source_version.replace(":", "%3A")) / file["name"]
                    if digest(path.read_bytes()) != file["sha256"]:
                        raise RuntimeError("Debian source changed")
                    source_files.append({**file, "file": str(path)})
                notice = ROOT / (component + "-image/doc") / name / "copyright"
                if component == "victoria-logs" and name == "cross-toolchain-base" and version == "77" and not notice.exists():
                    file = next(f for f in source_files if f["name"] == "cross-toolchain-base_77.tar.xz")
                    if file["sha256"] != "23a15a64a339a3ea1b67cbfe453079150b86748d24a9ec137f1e96f2e3e74308":
                        raise RuntimeError("cross-toolchain exact reviewed source changed")
                    with tarfile.open(file["file"]) as archive:
                        data = archive.extractfile("cross-toolchain-base/debian/copyright").read()
                    notice = OUT / "cross-toolchain-base-77-copyright"
                    notice.write_bytes(data)
                else:
                    data = notice.read_bytes()
                notice_files.append({"path": "/usr/share/doc/" + name + "/copyright", "sha256": digest(data), "file": str(notice)})
                labels = sorted(set(re.findall(r"^License:[ \t]*(.*)$", data.decode(errors="replace"), re.M)))
            elif kind == "rpm":
                source, spec_name, spec = rpm_specs[metadata["sourceRpm"]]
                source_name, source_version = metadata["sourceRpm"], version
                source_files = [{**source, "file": str(ROOT / "keycloak-sources" / source["file"])}]
                spec_path = OUT / (digest(spec) + ".spec")
                spec_path.write_bytes(spec)
                notice_files.append({"path": "source-rpm/" + spec_name, "sha256": digest(spec), "file": str(spec_path)})
                # Actual image license files remain original, alongside the
                # full source RPM's upstream license files and packaging.
                for directory in (ROOT / "keycloak-image/os-licenses").iterdir():
                    if directory.name == name or directory.name == name.rsplit("-", 1)[0]:
                        for path in directory.rglob("*"):
                            if path.is_file():
                                notice_files.append({"path": "/usr/share/licenses/" + str(path.relative_to(directory.parent)), "sha256": digest(path.read_bytes()), "file": str(path)})
                labels = sorted(set(re.findall(r"^License:\s*(.*)$", spec.decode(errors="replace"), re.M)))
                if not labels:
                    raise RuntimeError("source RPM has no license declaration")
            else:
                source_name, source_version = metadata["originPackage"], version
                source = next(p for p in alpine if p["name"] == source_name and any(
                    b["component"] == component and b["name"] == name and b["version"] == version for b in p["binaryPackages"]))
                directory = ROOT / "alpine-sources" / (source_name + "-" + source["commit"])
                recipe = directory / "APKBUILD"
                if digest(recipe.read_bytes()) != source["recipeSHA256"]:
                    raise RuntimeError("APKBUILD changed")
                source_files = [{"url": source["recipeURL"], "sha256": source["recipeSHA256"], "name": "APKBUILD", "file": str(recipe)}]
                for file in source["files"]:
                    path = directory / file["name"]
                    if digest(path.read_bytes()) != file["sha256"]:
                        raise RuntimeError("Alpine source changed")
                    source_files.append({**file, "file": str(path)})
                notice_files = [{"path": "aports/" + source_name + "/APKBUILD", "sha256": source["recipeSHA256"], "file": str(recipe)}]
                m = re.search(r'^license=[\"\'](.*?)[\"\']', recipe.read_text(), re.M)
                if not m:
                    raise RuntimeError("APKBUILD license is not literal")
                labels = [m[1]]
            record = {"component": component, "package": name, "version": version, "type": kind,
                      "sourcePackage": source_name, "sourceVersion": source_version,
                      "originalLicenseDeclarations": labels,
                      "imageLicenseMetadata": [l["value"] for l in package.get("licenses", [])],
                      "sourceFiles": source_files, "noticeFiles": notice_files,
                      "permissionReviewComplete": False}
            records.append(record)
    (OUT / "native-notice-lock.json").write_text(json.dumps({"schemaVersion": 1, "packages": records,
        "qualificationPassed": False}, indent=2) + "\n")
    print("verified native publisher/source notice bindings:", len(records))


if __name__ == "__main__":
    main()
