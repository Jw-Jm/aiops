"""Collect exact Go module and toolchain sources from pinned image buildinfo.

Local replacements use the application's immutable source, never a guessed
module release. Preparation does not change Component Catalog admission.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import urllib.request
import urllib.error
import zipfile
import io


ROOT = Path("artifacts/task27-materials")
OUT = ROOT / "go-module-sources"
COMPONENTS = ("postgresql", "seaweedfs", "openbao", "victoria-metrics", "victoria-logs", "vmalert")
MAIN_MODULES = {"github.com/seaweedfs/seaweedfs": "seaweedfs",
                "github.com/openbao/openbao/v2": "openbao",
                "github.com/VictoriaMetrics/VictoriaMetrics": "victoria-metrics",
                "github.com/VictoriaMetrics/VictoriaLogs": "victoria-logs"}
BAO_LOCAL = {"github.com/boltdb/bolt": "internal/helper/stubbolt",
             "github.com/openbao/openbao/api/v2": "api",
             "github.com/openbao/openbao/api/auth/kubernetes/v2": "api/auth/kubernetes",
             "github.com/openbao/openbao/sdk/v2": "sdk"}


def fetch(url, path):
    if path.exists():
        return path.read_bytes()
    try:
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read()
    except urllib.error.URLError as error:
        raise RuntimeError("cannot retrieve exact source: " + url) from error
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return data


def escape(value):
    return "".join("!" + char.lower() if char.isupper() else char for char in value)


def main():
    modules = {}
    for component in COMPONENTS:
        suffix = ".syft.json" if component == "postgresql" else ".enriched.syft.json"
        sbom = json.loads((ROOT / "sbom" / (component + suffix)).read_text())
        for artifact in sbom["artifacts"]:
            if artifact["type"] == "go-module":
                modules.setdefault((artifact["name"], artifact["version"]), []).append(component)
    releases_raw = fetch("https://go.dev/dl/?mode=json&include=all", OUT / "go-release-index.json")
    releases = {release["version"]: release for release in json.loads(releases_raw)}

    def prepare(item):
        (name, version), components = item
        if name == "github.com/tianon/gosu":
            if version != "v1.19.0" or components != ["postgresql"]:
                raise RuntimeError("gosu source mapping requires a new runtime/release check")
            # Pinned PG image: `gosu --version` reports 1.19 (go1.24.6);
            # upstream tag 1.19 resolves to this full commit. Syft normalized
            # the version, so the nonexistent v1.19.0 module is not requested.
            commit = "6456aaa0f3c854d199d0f037f068eb97515b7513"
            url = "https://codeload.github.com/tianon/gosu/tar.gz/" + commit
            path = OUT / "gosu-1.19.tar.gz"
            data = fetch(url, path)
            return {"module": name, "publisherVersion": "1.19", "binaryBuildinfoVersion": version,
                    "components": components, "source": url, "commit": commit, "file": str(path),
                    "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}
        # The VictoriaMetrics module in VictoriaLogs is an ordinary versioned
        # dependency, not the independently deployed VictoriaMetrics image's
        # main module. Only actual main-module buildinfo uses its image source.
        own_main = name in MAIN_MODULES and (MAIN_MODULES[name] in components or
                    (MAIN_MODULES[name] == "victoria-metrics" and "vmalert" in components))
        if own_main or (name in BAO_LOCAL and "openbao" in components):
            component = MAIN_MODULES.get(name, "openbao")
            record = json.loads((ROOT / "go-sources" / (component + "-lock.json")).read_text())
            record.update({"module": name, "binaryBuildinfoVersion": version, "components": components,
                           "sourceKind": "application-source", "selectedPath": BAO_LOCAL.get(name, ".")})
            if name == "github.com/boltdb/bolt":
                record["licenseEvidence"] = "internal/helper/stubbolt/bolt.go: SPDX-License-Identifier: MPL-2.0"
            return record
        if name == "stdlib":
            files = releases.get(version, {}).get("files", [])
            source = next((file for file in files if file["kind"] == "source"), None)
            if source is None:
                raise RuntimeError("exact Go compiler source release unavailable: " + version)
            url = "https://go.dev/dl/" + source["filename"]
            path = OUT / "toolchains" / source["filename"]
            data = fetch(url, path)
            if hashlib.sha256(data).hexdigest() != source["sha256"]:
                raise RuntimeError("Go source release checksum mismatch")
            return {"module": name, "version": version, "components": components, "source": url,
                    "file": str(path), "sha256": source["sha256"], "size": len(data)}
        if version in ("", "UNKNOWN", "(devel)") or "/" in version:
            raise RuntimeError("Go module has no exact source version: " + name)
        escaped = escape(name)
        prefix = OUT / "modules" / escaped / version
        url = "https://proxy.golang.org/" + escaped + "/@v/" + escape(version)
        info_raw = fetch(url + ".info", prefix / "module.info")
        info = json.loads(info_raw)
        if info["Version"] != version:
            raise RuntimeError("Go proxy changed requested module version")
        data = fetch(url + ".zip", prefix / "module.zip")
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            expected = name + "@" + version + "/"
            if not archive.namelist() or any(not path.startswith(expected) or ".." in Path(path).parts for path in archive.namelist()):
                raise RuntimeError("Go source ZIP changed module identity")
            notices = [{"path": path, "sha256": hashlib.sha256(archive.read(path)).hexdigest()}
                       for path in archive.namelist() if Path(path).name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE"))]
        print("verified Go module source", name, version, flush=True)
        return {"module": name, "version": version, "components": components, "source": url + ".zip",
                "file": str(prefix / "module.zip"), "sha256": hashlib.sha256(data).hexdigest(), "size": len(data),
                "origin": info.get("Origin"), "metadataSHA256": hashlib.sha256(info_raw).hexdigest(), "noticeFiles": notices}

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        records = list(executor.map(prepare, sorted(modules.items())))
    (OUT / "source-module-lock.json").write_text(json.dumps({"schemaVersion": 1, "modules": records,
        "releaseIndexSHA256": hashlib.sha256(releases_raw).hexdigest(), "qualificationPassed": False}, indent=2) + "\n")
    print("verified exact Go source identities:", len(records), flush=True)


if __name__ == "__main__":
    main()
