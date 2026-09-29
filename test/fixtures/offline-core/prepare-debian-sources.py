"""Collect exact Debian/PGDG source packages for the pinned core images.

Connected preparation only. Current signed-distribution repository paths are
tried first; historical packages use Debian's content-addressed snapshot API.
Every source archive is matched to the image SBOM's source name/version and
locked by SHA-256. This command does not qualify a component.
"""
import concurrent.futures
import hashlib
import gzip
import json
import lzma
from pathlib import Path
import time
import urllib.error
import urllib.parse
import urllib.request


OUT = Path("artifacts/task27-materials/debian-sources")
REPOSITORIES = (
    ("https://deb.debian.org/debian/", "bookworm/main"),
    ("https://deb.debian.org/debian/", "bookworm-updates/main"),
    ("https://security.debian.org/debian-security/", "bookworm-security/main"),
    ("https://deb.debian.org/debian/", "trixie/main"),
    ("https://deb.debian.org/debian/", "trixie-updates/main"),
    ("https://security.debian.org/debian-security/", "trixie-security/main"),
    ("https://apt.postgresql.org/pub/repos/apt/", "bookworm-pgdg/main"),
)


def get(url):
    for attempt in range(3):
        try:
            with urllib.request.urlopen(url, timeout=60) as response:
                return response.read()
        except (urllib.error.URLError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def save(url, destination, expected=None, algorithm="sha256"):
    data = destination.read_bytes() if destination.exists() else get(url)
    if expected and hashlib.new(algorithm, data).hexdigest() != expected:
        raise RuntimeError(f"source checksum mismatch: {url}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    if not destination.exists():
        temporary = destination.with_suffix(destination.suffix + ".partial")
        temporary.write_bytes(data)
        temporary.replace(destination)
    return data


def paragraphs(text):
    for paragraph in text.split("\n\n"):
        fields = {}
        last = None
        for line in paragraph.splitlines():
            if line.startswith(" ") and last:
                fields[last] += "\n" + line.strip()
            elif ":" in line:
                last, value = line.split(":", 1)
                fields[last] = value.strip()
        if fields:
            yield fields


def main():
    required = {}
    for component in ("postgresql", "victoria-logs"):
        sbom = Path("artifacts/task27-materials/sbom") / (component + ".syft.json")
        if not sbom.exists():
            sbom = sbom.with_name(component + ".enriched.syft.json")
        for package in json.loads(sbom.read_text())["artifacts"]:
            if package["type"] == "deb":
                metadata = package["metadata"]
                key = (metadata.get("source") or package["name"], metadata.get("sourceVersion") or package["version"])
                required.setdefault(key, []).append({"component": component, "binaryPackage": package["name"], "version": package["version"]})
    sources = {}
    metadata_locks = []
    for base, suite in REPOSITORIES:
        for suffix, decompress in ((".xz", lzma.decompress), (".gz", gzip.decompress), ("", lambda data: data)):
            url = base + "dists/" + suite + "/source/Sources" + suffix
            filename = OUT / "metadata" / (suite.replace("/", "-") + "-Sources" + suffix)
            try:
                compressed = save(url, filename)
                text = decompress(compressed).decode()
                break
            except urllib.error.HTTPError as error:
                if error.code != 404 or not suffix:
                    raise
        metadata_locks.append({"url": url, "sha256": hashlib.sha256(compressed).hexdigest()})
        for paragraph in paragraphs(text):
            key = (paragraph.get("Package"), paragraph.get("Version"))
            if key not in required or "Checksums-Sha256" not in paragraph:
                continue
            sources[key] = [{"url": base + paragraph["Directory"] + "/" + row.split()[2],
                             "sha256": row.split()[0], "size": int(row.split()[1]), "name": row.split()[2]}
                            for row in paragraph["Checksums-Sha256"].splitlines() if row.strip()]

    def prepare(item):
        (name, version), users = item
        records = sources.get((name, version))
        package_dir = OUT / "packages" / (name + "_" + version.replace(":", "%3A"))
        if records is None:
            api = "https://snapshot.debian.org/mr/package/" + urllib.parse.quote(name, safe="") + "/" + urllib.parse.quote(version, safe="") + "/srcfiles"
            response = save(api, package_dir / "snapshot-srcfiles.json")
            document = json.loads(response)
            if document.get("package") != name or document.get("version") != version:
                raise RuntimeError("snapshot source identity mismatch")
            snapshot_files = []
            for entry in document["result"]:
                file_hash = entry["hash"]
                info_url = "https://snapshot.debian.org/mr/file/" + file_hash + "/info"
                info = json.loads(save(info_url, package_dir / (file_hash + "-info.json")))
                snapshot_files.append((file_hash, {row["name"] for row in info["result"]}))
            dsc_files = [(digest, filename) for digest, filenames in snapshot_files for filename in filenames if filename.endswith(".dsc")]
            if len(dsc_files) != 1:
                raise RuntimeError("source snapshot must have exactly one dsc identity")
            dsc_hash, dsc_name = dsc_files[0]
            dsc_url = "https://snapshot.debian.org/file/" + dsc_hash
            dsc_bytes = save(dsc_url, package_dir / dsc_name, dsc_hash, "sha1")
            dsc = next(p for p in paragraphs(dsc_bytes.decode()) if "Source" in p)
            if dsc.get("Source") != name or dsc.get("Version") != version:
                raise RuntimeError("dsc source name/version differs from image")
            sha1_files = {row.split()[2]: row.split()[0] for row in dsc["Checksums-Sha1"].splitlines() if row.strip()}
            records = [{"url": dsc_url, "sha256": hashlib.sha256(dsc_bytes).hexdigest(), "size": len(dsc_bytes), "name": dsc_name}]
            for row in dsc["Checksums-Sha256"].splitlines():
                if not row.strip():
                    continue
                digest, size, filename = row.split()
                expected_sha1 = sha1_files[filename]
                if not any(h == expected_sha1 and filename in aliases for h, aliases in snapshot_files):
                    raise RuntimeError("dsc archive identity is absent from snapshot source inventory")
                records.append({"url": "https://snapshot.debian.org/file/" + expected_sha1,
                                "sha256": digest, "size": int(size), "name": filename})
        for record in records:
            data = save(record["url"], package_dir / record["name"], record["sha256"])
            if len(data) != record["size"]:
                raise RuntimeError("source archive size mismatch")
        lock = {"name": name, "version": version, "binaryPackages": users, "files": records}
        (package_dir / "source-lock.json").write_text(json.dumps(lock, indent=2) + "\n")
        print("verified", name, version, flush=True)
        return lock

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        locks = list(executor.map(prepare, sorted(required.items())))
    (OUT / "source-package-lock.json").write_text(json.dumps({"schemaVersion": 1, "metadata": metadata_locks,
        "packages": locks, "qualificationPassed": False}, indent=2) + "\n")
    print("verified exact Debian source packages:", len(locks), flush=True)


if __name__ == "__main__":
    main()
