"""Prepare exact Keycloak UBI source RPMs; never changes component admission.

Run during connected preparation. Match the pinned image's RPM database to
official source repository metadata, verify every downloaded archive checksum,
and retain the original metadata alongside a machine-readable lock. A missing
exact source is an error, not permission to substitute a different version.
"""
import concurrent.futures
import gzip
import hashlib
import json
from pathlib import Path
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET


OUT = Path("artifacts/task27-materials/keycloak-sources")
SBOM = Path("artifacts/task27-materials/sbom/keycloak.enriched.syft.json")
NS = {"r": "http://linux.duke.edu/metadata/repo", "c": "http://linux.duke.edu/metadata/common"}


def download(url, destination, expected=None, algorithm="sha256"):
    if destination.exists():
        data = destination.read_bytes()
    else:
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read()
    if expected and hashlib.new(algorithm, data).hexdigest() != expected:
        raise RuntimeError(f"checksum mismatch: {url}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    if not destination.exists():
        temporary = destination.with_suffix(destination.suffix + ".partial")
        temporary.write_bytes(data)
        temporary.replace(destination)
    return data


def main():
    artifacts = json.loads(SBOM.read_text())["artifacts"]
    required = {a["metadata"]["sourceRpm"] for a in artifacts if a["type"] == "rpm"}
    sources = {}
    metadata_locks = []
    for repo in ("baseos", "appstream", "codeready-builder"):
        base = f"https://cdn-ubi.redhat.com/content/public/ubi/dist/ubi9/9/aarch64/{repo}/source/SRPMS/"
        repomd_url = base + "repodata/repomd.xml"
        repomd_file = OUT / "metadata" / (repo + "-repomd.xml")
        repomd = download(repomd_url, repomd_file)
        root = ET.fromstring(repomd)
        primary = next(d for d in root.findall("r:data", NS) if d.get("type") == "primary")
        checksum = primary.find("r:checksum", NS)
        primary_url = urllib.parse.urljoin(base, primary.find("r:location", NS).get("href"))
        primary_file = OUT / "metadata" / (repo + "-primary.xml.gz")
        compressed = download(primary_url, primary_file, checksum.text, checksum.get("type"))
        metadata_locks.append({"url": repomd_url, "sha256": hashlib.sha256(repomd).hexdigest(),
                               "primaryURL": primary_url, "primarySHA256": hashlib.sha256(compressed).hexdigest()})
        for package in ET.fromstring(gzip.decompress(compressed)).findall("c:package", NS):
            location = package.find("c:location", NS).get("href")
            filename = Path(location).name
            if filename in required:
                digest = package.find("c:checksum", NS)
                if digest.get("type") != "sha256":
                    raise RuntimeError(f"source RPM lacks SHA-256: {filename}")
                sources[filename] = {"url": urllib.parse.urljoin(base, location), "sha256": digest.text}
    missing = sorted(required - sources.keys())
    if missing:
        OUT.mkdir(parents=True, exist_ok=True)
        (OUT / "missing-exact-sources.json").write_text(json.dumps({"sources": missing, "metadata": metadata_locks}, indent=2) + "\n")
        raise RuntimeError("exact source RPMs missing from official repositories: " + ", ".join(missing))

    def prepare(item):
        filename, source = item
        data = download(source["url"], OUT / "rpms" / filename, source["sha256"])
        print("verified", filename, len(data), flush=True)
        return {"file": "rpms/" + filename, "size": len(data), **source}

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        files = list(executor.map(prepare, sorted(sources.items())))
    lock = {"schemaVersion": 1, "component": "keycloak", "version": "26.7.4",
            "imageDigest": "sha256:1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c",
            "sbomSHA256": hashlib.sha256(SBOM.read_bytes()).hexdigest(), "metadata": metadata_locks,
            "sourceRPMs": files, "qualificationPassed": False}
    (OUT / "source-rpm-lock.json").write_text(json.dumps(lock, indent=2) + "\n")
    print("verified exact source RPM closure:", len(files), flush=True)


if __name__ == "__main__":
    main()
