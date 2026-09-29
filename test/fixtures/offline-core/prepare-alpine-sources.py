"""Collect sources and patches locked by each image's exact Alpine APKBUILD.

Uses the APK database's aports commit, never a floating branch. Does not execute
APKBUILD shell text. Every selected file must match its declared SHA-512. This
is source preparation, not automatic license or runtime qualification.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import re
import urllib.error
import urllib.request
import time


OUT = Path("artifacts/task27-materials/alpine-sources")


def get(url):
    for attempt in range(3):
        try:
            with urllib.request.urlopen(url, timeout=20) as response:
                return response.read()
        except urllib.error.HTTPError:
            raise
        except (urllib.error.URLError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(attempt + 1)


def main():
    packages = {}
    for component in ("seaweedfs", "openbao", "victoria-metrics", "vmalert"):
        sbom = Path("artifacts/task27-materials/sbom") / (component + ".enriched.syft.json")
        for artifact in json.loads(sbom.read_text())["artifacts"]:
            if artifact["type"] != "apk":
                continue
            metadata = artifact["metadata"]
            key = (metadata["originPackage"], metadata["gitCommitOfApkPort"])
            packages.setdefault(key, []).append({"component": component, "name": artifact["name"],
                                                 "version": artifact["version"]})

    def prepare(item):
        (name, commit), users = item
        directory = OUT / (name + "-" + commit)
        directory.mkdir(parents=True, exist_ok=True)
        recipe = None
        recipe_url = None
        for section in ("main", "community"):
            recipe_url = f"https://raw.githubusercontent.com/alpinelinux/aports/{commit}/{section}/{name}/APKBUILD"
            try:
                recipe = get(recipe_url)
                break
            except urllib.error.HTTPError as error:
                if error.code != 404:
                    raise
        if recipe is None:
            raise RuntimeError(f"exact APKBUILD not found: {name}@{commit}")
        (directory / "APKBUILD").write_bytes(recipe)
        text = recipe.decode()
        checksum_block = re.search(r'sha512sums=[\"\'](.*?)[\"\']', text, re.S)
        hashes = re.findall(r"^([0-9a-f]{128})\s+([^\s\"']+)$", checksum_block[1] if checksum_block else "", re.M)
        declared_source = re.search(r'^source=[\"\'](.*?)[\"\']', text, re.M | re.S)
        if not hashes and declared_source and declared_source[1].strip():
            raise RuntimeError(f"APKBUILD has no literal source checksum inventory: {name}")
        files = []
        variables = {"pkgname": name}
        for variable in ("pkgver", "_pkgver", "_srcname", "_commit"):
            value = re.search(r'^' + variable + r'=([^\n]+)', text, re.M)
            if value and not any(c in value[1] for c in "();`"):
                variables[variable] = value[1].strip("\"'")
        source_text = declared_source[1] if declared_source else ""
        for variable, value in variables.items():
            source_text = source_text.replace("${" + variable + "}", value).replace("$" + variable, value)
        declared_urls = [u for u in re.findall(r'https://[^\s\"\']+', source_text) if "$" not in u]
        for expected, filename in hashes:
            if Path(filename).name != filename:
                raise RuntimeError("unsafe source filename")
            destination = directory / filename
            urls = [recipe_url.rsplit("/", 1)[0] + "/" + filename]
            urls += [f"https://distfiles.alpinelinux.org/distfiles/{version}/{filename}" for version in ("v3.23", "v3.22", "v3.21", "v3.20")]
            urls += declared_urls
            if destination.exists():
                data = destination.read_bytes()
                if hashlib.sha512(data).hexdigest() != expected:
                    raise RuntimeError(f"cached source checksum mismatch: {filename}")
                old_lock = json.loads((directory / "source-lock.json").read_text()) if (directory / "source-lock.json").exists() else {}
                url = next((r["url"] for r in old_lock.get("files", []) if r["name"] == filename), None)
                origin = directory / (filename + "-origin.json")
                if url is None and origin.exists():
                    url = json.loads(origin.read_text())["url"]
                if url is None:
                    raise RuntimeError(f"cached source has no recorded download origin: {name}/{filename}")
            else:
                data = None
                for url in urls:
                    try:
                        candidate = get(url)
                    except urllib.error.HTTPError as error:
                        if error.code in (403, 404):
                            continue
                        raise
                    except (urllib.error.URLError, TimeoutError):
                        continue
                    if hashlib.sha512(candidate).hexdigest() == expected:
                        data = candidate
                        break
                if data is None:
                    raise RuntimeError(f"cannot retrieve checksum-matched source: {name}/{filename}")
                destination.write_bytes(data)
            files.append({"name": filename, "url": url, "declaredSourceURLs": declared_urls, "sha512": expected,
                          "sha256": hashlib.sha256(data).hexdigest(), "size": len(data)})
        lock = {"name": name, "commit": commit, "binaryPackages": users, "recipeURL": recipe_url,
                "recipeSHA256": hashlib.sha256(recipe).hexdigest(), "files": files,
                "qualificationPassed": False}
        (directory / "source-lock.json").write_text(json.dumps(lock, indent=2) + "\n")
        print("verified", name, commit, len(files), flush=True)
        return lock

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        locks = list(executor.map(prepare, sorted(packages.items())))
    (OUT / "source-package-lock.json").write_text(json.dumps({"schemaVersion": 1, "packages": locks,
        "qualificationPassed": False}, indent=2) + "\n")
    print("verified exact Alpine source packages:", len(locks), flush=True)


if __name__ == "__main__":
    main()
