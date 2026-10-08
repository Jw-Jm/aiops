#!/usr/bin/env python3
"""Prepare the exact declared Cargo source superset of the locked DeepFlow source.

This records original publisher inputs, not runtime or distribution admission.
Rust cannot be omitted merely because an image scanner reports no Rust packages.
"""
import argparse
import concurrent.futures
import hashlib
import json
import pathlib
import re
import tarfile
import time
import tomllib
import urllib.request


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=pathlib.Path, required=True)
    parser.add_argument("--source-sha256", required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--cache", type=pathlib.Path)
    args = parser.parse_args()
    raw = args.source.read_bytes()
    if digest(raw) != args.source_sha256:
        raise ValueError("locked DeepFlow source archive digest mismatch")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    origins, registry, git = [], {}, {}
    with tarfile.open(args.source) as archive:
        for member in archive.getmembers():
            if not member.isfile() or not member.name.endswith("/Cargo.lock"):
                continue
            data = archive.extractfile(member).read()
            # Preserve every declared lock, including the sample/build superset.
            target = output / "publisher-locks" / member.name
            if not target.resolve().is_relative_to(output):
                raise ValueError("unsafe publisher lock path")
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            origins.append({"member": member.name, "sha256": digest(data)})
            for package in tomllib.loads(data.decode())["package"]:
                source = package.get("source")
                if not source:
                    continue  # Local packages are retained in the full source archive.
                key = package["name"], package["version"]
                if not all(re.fullmatch(r"[A-Za-z0-9_.+-]+", part) for part in key):
                    raise ValueError("unsafe Cargo package identity")
                if source == "registry+https://github.com/rust-lang/crates.io-index":
                    if not re.fullmatch("[a-f0-9]{64}", package.get("checksum", "")):
                        raise ValueError("registry package has no locked publisher checksum")
                    if key in registry and registry[key]["checksum"] != package["checksum"]:
                        raise ValueError("conflicting Cargo checksum")
                    registry[key] = package
                elif re.fullmatch(r"git\+https://github.com/deepflowio/[A-Za-z0-9_.-]+/?#[a-f0-9]{40}", source):
                    git[source] = package
                else:
                    raise ValueError("unadmitted Cargo source: " + source)
    if not origins:
        raise ValueError("locked DeepFlow source has no Cargo locks")

    def download(url):
        for attempt in range(3):
            try:
                with urllib.request.urlopen(url, timeout=60) as response:
                    return response.read()
            except Exception:
                if attempt == 2:
                    raise
                time.sleep(1 + attempt)

    def fetch(package):
        name, version = package["name"], package["version"]
        url = f"https://static.crates.io/crates/{name}/{name}-{version}.crate"
        target = output / "crates" / f"{name}-{version}.crate"
        target.parent.mkdir(exist_ok=True)
        record = {"name": name, "version": version, "source": url,
                  "sha256": "sha256:" + package["checksum"]}
        try:
            cached = args.cache / target.name if args.cache else None
            if target.exists():
                data = target.read_bytes()
            elif cached and cached.exists():
                data = cached.read_bytes()
            else:
                data = download(url)
            if digest(data) != record["sha256"]:
                raise ValueError("Cargo archive checksum differs from publisher lock")
            target.write_bytes(data)
            notices = []
            with tarfile.open(target) as archive:
                metadata_raw = archive.extractfile(f"{name}-{version}/Cargo.toml").read()
                metadata = tomllib.loads(metadata_raw.decode())["package"]
                record["publisherLicense"] = metadata.get("license")
                record["publisherLicenseFile"] = metadata.get("license-file")
                for member in archive.getmembers():
                    if not member.isfile():
                        continue
                    filename = pathlib.PurePosixPath(member.name).name.upper()
                    declared = metadata.get("license-file")
                    if not (member.name == f"{name}-{version}/Cargo.toml" or
                            any(word in filename for word in ("LICENSE", "LICENCE", "COPYING", "NOTICE", "COPYRIGHT")) or
                            (declared and member.name == f"{name}-{version}/{declared}")):
                        continue
                    data = archive.extractfile(member).read()
                    destination = output / "notices" / member.name
                    if not destination.resolve().is_relative_to(output):
                        raise ValueError("unsafe Cargo notice path")
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    destination.write_bytes(data)
                    notices.append({"path": str(destination.relative_to(output)), "sha256": digest(data)})
            record.update(status="prepared", originalNotices=notices, path=str(target.relative_to(output)))
        except Exception as error:
            record.update(status="failed", errorType=type(error).__name__)
        return record

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        records = list(executor.map(fetch, [registry[key] for key in sorted(registry)]))
    git_records = []
    for source, package in sorted(git.items()):
        repository, commit = source.removeprefix("git+").split("#")
        repository = repository.rstrip("/")
        url = repository.replace("https://github.com/", "https://codeload.github.com/") + "/tar.gz/" + commit
        target = output / "git" / (repository.rsplit("/", 1)[1] + "-" + commit + ".tar.gz")
        target.parent.mkdir(exist_ok=True)
        record = {"name": package["name"], "version": package["version"], "source": url, "commit": commit}
        try:
            data = target.read_bytes() if target.exists() else download(url)
            with tarfile.open(fileobj=__import__("io").BytesIO(data)) as archive:
                if not archive.getmembers() or not archive.getmembers()[0].name.endswith(commit):
                    raise ValueError("Git snapshot root differs from locked commit")
            target.write_bytes(data)
            record.update(status="prepared", sha256=digest(data), path=str(target.relative_to(output)),
                          binding="exact commit URL from Cargo.lock; TLS publisher archive, not Git object revalidation")
        except Exception as error:
            record.update(status="failed", errorType=type(error).__name__)
        git_records.append(record)
    report = {"schemaVersion": 1, "sourceArchiveSHA256": args.source_sha256,
              "scope": "complete declared Cargo lock superset, including conditional/build/sample inputs; actual linked closure still requires review",
              "origins": origins, "registryPackages": records, "gitPackages": git_records,
              "runtimeAndDistributionAdmission": False}
    (output / "source.lock.json").write_text(json.dumps(report, indent=2) + "\n")
    failed = sum(row["status"] != "prepared" for row in records + git_records)
    print(json.dumps({"registryPackages": len(records), "gitPackages": len(git_records), "failed": failed, "admitted": False}))
    raise SystemExit(1 if failed else 0)


if __name__ == "__main__":
    main()
