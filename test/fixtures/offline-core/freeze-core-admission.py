"""Freeze the completed exact-source review and isolated component PoCs.

Preparation only; requires PyYAML 6.0.3. This is never invoked by install.
Historical scanner/preparation reports remain unchanged. Core install/reinstall
is a separate live gate, and future versions need a new explicit review.
"""
import hashlib
import json
import re
from pathlib import Path
import shutil
import tarfile

import yaml

ROOT = Path("artifacts/task27-materials")
ADR = "docs/adr/0010-core-image-license-admission.md"
NAMES = ("postgresql", "keycloak", "seaweedfs", "openbao", "victoria-metrics", "victoria-logs", "vmalert")


def sha(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for data in iter(lambda: f.read(1 << 20), b""):
            h.update(data)
    return h.hexdigest()


def load(path):
    return json.loads(Path(path).read_text())


def mirror(path):
    p = Path(path)
    dst = Path("bundle/evidence") / p
    dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(p, dst)


def main():
    catalog_path = Path("bundle/component-catalog.yaml")
    original = catalog_path.read_text()
    catalog = yaml.safe_load(original)
    components = {c["name"]: c for c in catalog["components"]}
    native = load("internal/supplychain/licenses/native-core-reviewed.json")["licenses"]
    go = load(ROOT / "license-review/go-license-lock.json")["modules"]
    java = load(ROOT / "license-review/java-license-lock.json")["artifacts"]
    jni = load(ROOT / "native-jar-sources/source-lock.json")["projects"]
    bundles = {c["component"]: c for c in load(ROOT / "corresponding-sources/source-bundle-lock.json")["components"]}
    startup = load("artifacts/test-reports/task-2.7-dependency-startup.json")
    bao = load("artifacts/test-reports/task-2.7-openbao-live-health.json")
    if not all(startup.get(k) is True for k in ("postgresqlSQL", "keycloakManagementReady", "readOnlyRootFilesystem")):
        raise RuntimeError("pinned PostgreSQL/Keycloak offline startup evidence incomplete")
    if bao["image"] != "ghcr.io/openbao/openbao@" + components["openbao"]["digest"] or not bao["initialized"] or bao["sealed"] or not bao["independentCATLSVerified"]:
        raise RuntimeError("pinned OpenBao independent trust/health evidence incomplete")
    for log, required in {
        "task-2.7-seaweed-s3-poc.log": "--- PASS: TestSeaweedPinnedOfflineS3Capability",
        "task-2.7-victoria-poc.log": "--- PASS: TestVictoriaBundledOfflineInstall",
    }.items():
        if required not in Path("artifacts/test-reports", log).read_text():
            raise RuntimeError("required actual component PoC missing: " + log)
    report_root = Path("third_party/admission/core-arm64")
    report_root.mkdir(parents=True, exist_ok=True)
    for report in ("task-2.7-dependency-startup.json", "task-2.7-openbao-live-health.json", "task-2.7-seaweed-s3-poc.log", "task-2.7-victoria-poc.log"):
        target = report_root / report
        shutil.copyfile(Path("artifacts/test-reports") / report, target)
        mirror(target)
    roots = {
        "postgresql": load(ROOT / "postgresql-source-lock.json"),
        "keycloak": load(ROOT / "keycloak-source-lock.json"),
    }
    for name in NAMES:
        c, source_bundle = components[name], bundles[name]
        if sha(source_bundle["file"]) != source_bundle["sha256"]:
            raise RuntimeError("corresponding source changed: " + name)
        with tarfile.open(source_bundle["file"]) as t:
            inventory = t.extractfile("SOURCE-INVENTORY.json").read()
            if hashlib.sha256(inventory).hexdigest() != source_bundle["inventorySHA256"]:
                raise RuntimeError("source inventory changed")
        g = [m for m in go if name in m["components"]]
        if name not in roots:
            roots[name] = next(m for m in g if m.get("selectedPath") == "." and m.get("commit") == c["commit"])
        root = roots[name]
        if root["commit"] != c["commit"]:
            raise RuntimeError("root source differs from reviewed component")
        files, deps = [], []

        def add_notice(path, license, digest):
            if sha(path) != digest:
                raise RuntimeError("reviewed notice changed: " + path)
            row = {"path": path, "license": license, "digest": "sha256:" + digest}
            if row not in files:
                files.append(row)
            mirror(path)

        # Root text is a real file copied at the immutable selected source.
        root_license = Path("third_party/licenses") / name / ("COPYRIGHT" if name == "postgresql" else "LICENSE.txt" if name == "keycloak" else "LICENSE")
        add_notice(str(root_license), c["license"], sha(root_license))
        n = [p for p in native if p["component"] == name]
        for p in n:
            if p["imageDigest"] != c["digest"] or p["componentVersion"] != c["version"] or p["correspondingSourceBundleSHA256"] != "sha256:" + source_bundle["sha256"]:
                raise RuntimeError("finite native review identity mismatch")
            deps.append({"name": p["dependencyName"], "version": p["version"], "source": p["source"], "sourceType": "archive", "sourceArchiveSHA256": p["sourceArchiveSHA256"], "digest": p["sourceArchiveSHA256"], "license": p["id"]})
            add_notice(p["noticePath"], p["id"], p["noticeDigest"].removeprefix("sha256:"))
        for m in g:
            if sha(m["file"]) != m["sha256"]:
                raise RuntimeError("Go source changed")
            version = m.get("publisherVersion", m.get("version", c["version"]))
            deps.append({"name": "go:" + m["module"], "version": version, "source": m["source"], "sourceType": "archive", "sourceArchiveSHA256": "sha256:" + m["sha256"], "digest": "sha256:" + m["sha256"], "license": m["reviewedLicense"]})
            for notice in m["sourceNotices"]:
                target = Path("third_party/licenses/go") / (notice["sha256"] + ".txt")
                target.parent.mkdir(exist_ok=True)
                shutil.copyfile(notice["file"], target)
                add_notice(str(target), m["reviewedLicense"], notice["sha256"])
        if name == "keycloak":
            for m in java:
                parts = m["coordinate"].split(":")
                version = parts[-1]
                source = m.get("sourceURL", root["source"])
                digest = m.get("sourceSHA256", root["sha256"])
                deps.append({"name": "maven:" + m["coordinate"], "version": version, "source": source, "sourceType": "archive", "sourceArchiveSHA256": "sha256:" + digest, "digest": "sha256:" + m.get("imageJarSHA256", m.get("jarSHA256", digest)), "license": m["reviewedLicense"]})
            # Native compiled libraries include their full preferred C sources.
            jni_licenses = {"jna": "Apache-2.0 AND MIT", "byte-buddy": "Apache-2.0", "netty": "Apache-2.0", "jline": "BSD-3-Clause", "brotli4j": "Apache-2.0 AND MIT"}
            for m in jni:
                deps.append({"name": "native-jar:" + m["name"], "version": m["version"], "source": m["source"], "sourceType": "archive", "sourceArchiveSHA256": "sha256:" + m["sha256"], "digest": "sha256:" + m["sha256"], "license": jni_licenses[m["name"]]})
            target = Path("third_party/licenses/keycloak/third-party-notice-26.7.4.html")
            shutil.copyfile(ROOT / "keycloak-third-party-notice-26.7.4.html", target)
            mirror(target)
        fixture_path = report_root / (name + ".json")
        fixture = {"schemaVersion": 1, "scope": "pinned development linux/arm64 component admission; not full core acceptance or HA", "component": name, "version": c["version"], "imageDigest": c["digest"], "sourceCommit": c["commit"], "sourceArchiveSHA256": "sha256:" + root["sha256"], "correspondingSourceBundleSHA256": "sha256:" + source_bundle["sha256"], "sourceInventorySHA256": "sha256:" + source_bundle["inventorySHA256"], "nativePackages": len(n), "goSources": len(g), "javaSources": len(java) if name == "keycloak" else 0, "nativeJarProjects": len(jni) if name == "keycloak" else 0, "sourceNoticeClosureReviewed": True, "coreOfflineInstallationPassed": False,
                   "runtimeEvidence": [str(report_root / f) for f in ("task-2.7-dependency-startup.json",) if name in ("postgresql", "keycloak")] + [str(report_root / ("task-2.7-openbao-live-health.json" if name == "openbao" else "task-2.7-seaweed-s3-poc.log" if name == "seaweedfs" else "task-2.7-victoria-poc.log"))] if name not in ("postgresql", "keycloak") else [str(report_root / "task-2.7-dependency-startup.json")]}
        fixture_path.write_text(json.dumps(fixture, indent=2) + "\n")
        mirror(fixture_path)
        c.update(state="qualified", specialLicenseADR=ADR, fileLicenses=files, sourceSnapshot=True, sourceArchiveSHA256="sha256:" + root["sha256"], correspondingSourceBundleSHA256="sha256:" + source_bundle["sha256"], linkageMode="separate-process", importedPaths=["Bundle corresponding source archive; no upstream code copied into platform"], dependencyClosure=deps, dependencyClosureVerified=True, patches=[], forkPolicy="not-applicable", owner="Core Bundle maintainer", pocReport="docs/poc/core-arm64-admission.md", conformanceFixtures=[str(fixture_path)], exitPlan="Retain pinned distribution until a replacement passes source/license/closure and runtime conformance; preserve existing external service data and trust")
        c.pop("candidateNote", None)
        # Keep unrelated task entries byte-for-byte. Replace this YAML block only.
        start = original.index("  - name: " + name + "\n")
        match = re.search(r"^  - name: ", original[start + 1:], re.M)
        end = start + 1 + match.start() if match else -1
        if end < 0:
            end = original.index("firstPartyKernels:", start)
        rendered = yaml.safe_dump([c], sort_keys=False, width=140, indent=2)
        rendered = "\n".join("  " + line if line else line for line in rendered.splitlines()) + "\n"
        original = original[:start] + rendered + original[end:]
        print(name, "reviewed source/notice closure", len(deps), flush=True)
    mirror(ADR)
    mirror("docs/poc/core-arm64-admission.md")
    catalog_path.write_text(original)


if __name__ == "__main__":
    main()
