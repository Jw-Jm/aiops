"""Package verified local source/notice inputs without changing admission.

Each outer tar contains regular files only. Publisher archives are preserved as
opaque original files; their scripts are not executed or extracted for install.
"""
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

ROOT = Path("artifacts/task27-materials")
OUT = ROOT / "corresponding-sources"
COMPONENTS = ("postgresql", "keycloak", "seaweedfs", "openbao", "victoria-metrics", "victoria-logs", "vmalert")


def file_sha(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    OUT.mkdir(exist_ok=True)
    pg = ROOT / "postgresql-source.tar.gz"
    pg_commit = "083ac033419f690758508e08c1736089384bbee8"
    pg_url = "https://codeload.github.com/postgres/postgres/tar.gz/" + pg_commit
    if not pg.exists():
        with urllib.request.urlopen(pg_url, timeout=90) as response, pg.open("wb") as f:
            while chunk := response.read(1 << 20):
                f.write(chunk)
    pg_lock = {"source": pg_url, "commit": pg_commit, "sha256": file_sha(pg), "size": pg.stat().st_size}
    (ROOT / "postgresql-source-lock.json").write_text(json.dumps(pg_lock, indent=2) + "\n")
    with tarfile.open(pg) as archive:
        members = [m for m in archive if m.isfile() and m.name.endswith("/COPYRIGHT") and m.name.count("/") == 1]
        if len(members) != 1 or hashlib.sha256(archive.extractfile(members[0]).read()).hexdigest() != "3d6af92ff8a4c2cdf69afb1cf44edea727922f5cd0cf8b5f72b11cdecac8fdfd":
            raise RuntimeError("PostgreSQL exact source copyright differs from selected Catalog")
    native = json.loads((ROOT / "license-review/native-notices/native-notice-lock.json").read_text())
    go = json.loads((ROOT / "license-review/go-license-lock.json").read_text())
    java = json.loads((ROOT / "license-review/java-license-lock.json").read_text())
    jni = json.loads((ROOT / "native-jar-sources/source-lock.json").read_text())
    reports = []
    for component in COMPONENTS:
        inputs = {}

        def add(path, expected=None):
            path = Path(path)
            if not path.is_file():
                raise RuntimeError("required corresponding-source input missing: " + str(path))
            digest = file_sha(path)
            if expected and digest != expected:
                raise RuntimeError("corresponding-source input changed: " + str(path))
            # Retain exact original archive basename for native build tools;
            # source preparation hierarchy disambiguates duplicate names.
            relative = str(path.relative_to(ROOT)) if path.is_relative_to(ROOT) else str(path)
            if path.is_absolute() or ".." in Path(relative).parts:
                raise RuntimeError("source inventory path must stay in preparation/repository roots")
            inputs[relative] = {"file": str(path), "path": relative, "sha256": digest, "size": path.stat().st_size}

        component_native = [p for p in native["packages"] if p["component"] == component]
        component_go = [p for p in go["modules"] if component in p["components"]]
        for package in component_native:
            for file in package["sourceFiles"] + package["noticeFiles"]:
                add(file["file"], file["sha256"])
        for module in component_go:
            add(module["file"], module["sha256"])
            for notice in module["sourceNotices"]:
                add(notice["file"], notice["sha256"])
        if component == "postgresql":
            add(pg, pg_lock["sha256"])
        if component == "keycloak":
            source = json.loads((ROOT / "keycloak-source-lock.json").read_text())
            add(ROOT / "keycloak-source.tar.gz", source["sha256"])
            add(ROOT / "keycloak-third-party-notice-26.7.4.html", java["officialNoticeSHA256"])
            add(ROOT / "keycloak-notice-index.json")
            for artifact in java["artifacts"]:
                if artifact.get("sourceSHA256"):
                    # The recorded URL's Maven layout has a possibly multi-part
                    # group path; source files were prepared under dotted group.
                    pieces = artifact["sourceURL"].removeprefix("https://repo.maven.apache.org/maven2/").split("/")
                    group, name, version, filename = ".".join(pieces[:-3]), *pieces[-3:]
                    add(ROOT / "keycloak-maven" / group / name / version / filename, artifact["sourceSHA256"])
            def add_project(project):
                add(project["file"], project["sha256"])
                for child in project["submodules"]:
                    add_project(child)
            for project in jni["projects"]:
                add_project(project)
            for file in (ROOT / "keycloak-image/os-licenses").rglob("*"):
                if file.is_file():
                    add(file)
            for file in (ROOT / "license-review/openjdk-notices").rglob("*.txt"):
                add(file)
        if component in ("postgresql", "victoria-logs"):
            for file in (ROOT / (component + "-image/common-licenses")).iterdir():
                if file.is_file():
                    add(file)
        if component == "seaweedfs":
            for file in Path("third_party/licenses/seaweedfs").iterdir():
                if file.is_file():
                    add(file)
        if component == "openbao":
            for file in Path("third_party/licenses/openbao").iterdir():
                if file.is_file():
                    add(file)
        source_inventory = {"schemaVersion": 1, "component": component,
                            "nativePackages": component_native, "goModules": component_go,
                            "javaArtifacts": java["artifacts"] if component == "keycloak" else [],
                            "nativeJarProjects": jni["projects"] if component == "keycloak" else [],
                            "files": sorted(inputs.values(), key=lambda p: p["path"]), "qualificationPassed": False}
        inventory = json.dumps(source_inventory, indent=2, sort_keys=True).encode() + b"\n"
        readme = Path("third_party/licenses/corresponding-source-instructions.md").read_bytes()
        target = OUT / (component + "-source.tar")
        with tarfile.open(target, "w", format=tarfile.PAX_FORMAT) as archive:
            for name, data in (("SOURCE-INVENTORY.json", inventory), ("BUILD-AND-RELINK.md", readme)):
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode, entry.mtime = len(data), 0o644, 0
                archive.addfile(entry, io.BytesIO(data))
            for file in source_inventory["files"]:
                entry = tarfile.TarInfo(file["path"])
                entry.size, entry.mode, entry.mtime = file["size"], 0o644, 0
                with Path(file["file"]).open("rb") as f:
                    archive.addfile(entry, f)
        report = {"component": component, "file": str(target), "sha256": file_sha(target), "size": target.stat().st_size,
                  "inventorySHA256": hashlib.sha256(inventory).hexdigest(), "sourceFiles": len(inputs), "qualificationPassed": False}
        (OUT / (component + "-source-lock.json")).write_text(json.dumps(report, indent=2) + "\n")
        reports.append(report)
        print("packaged exact corresponding sources", component, len(inputs), report["size"], flush=True)
    (OUT / "source-bundle-lock.json").write_text(json.dumps({"schemaVersion": 1, "components": reports, "qualificationPassed": False}, indent=2) + "\n")


if __name__ == "__main__":
    main()
