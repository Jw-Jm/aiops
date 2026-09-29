"""Capture full exact projects for native libraries embedded in Keycloak JARs.

Java source JARs do not by themselves supply preferred C/C++ source. These
immutable tags were resolved with git ls-remote; submodule commits come from
the exact publisher Git tree. No build or downloaded script is executed.
"""
import concurrent.futures
import configparser
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

ROOT = Path("artifacts/task27-materials")
OUT = ROOT / "native-jar-sources"
PROJECTS = (
    ("jna", "5.8.0", "java-native-access/jna", "cc4ce71d511a9aa17219cc36e2338dd1b0f52770"),
    ("byte-buddy", "1.17.8", "raphw/byte-buddy", "ffd89ff7c500b50ce4ccaee73edaf63427716a30"),
    ("netty", "4.1.136.Final", "netty/netty", "fca0764703b3bb59c6e6dc5d29c6d9710d35c0e6"),
    ("jline", "4.2.1", "jline/jline3", "ee6981e7a8676dc2ffb5548c4527bf7e4b418ddb"),
    ("brotli4j", "1.23.0", "hyperxpro/Brotli4j", "205afeafd65d1e1c83f88a059e150687a1117426"),
)


def fetch(url, path):
    if path.exists():
        return path.read_bytes()
    request = urllib.request.Request(url, headers={"User-Agent": "ops-task27-source-preparation"})
    with urllib.request.urlopen(request, timeout=90) as response:
        data = response.read()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return data


def prepare(item):
    name, version, repository, commit = item
    directory = OUT / name
    url = "https://codeload.github.com/" + repository + "/tar.gz/" + commit
    path = directory / (commit + ".tar.gz")
    data = fetch(url, path)
    notices, native_sources, gitmodules = [], [], None
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        prefix = archive.getmembers()[0].name.split("/")[0] + "/"
        for entry in archive:
            if not entry.isfile():
                continue
            relative = entry.name.removeprefix(prefix)
            if relative == ".gitmodules":
                gitmodules = archive.extractfile(entry).read().decode()
            if Path(relative).name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE")):
                notices.append({"path": relative, "sha256": hashlib.sha256(archive.extractfile(entry).read()).hexdigest()})
            if relative.endswith((".c", ".h", ".cpp", ".cc")):
                native_sources.append(relative)
    submodules = []
    if gitmodules:
        tree_url = "https://api.github.com/repos/" + repository + "/git/trees/" + commit + "?recursive=1"
        tree_raw = fetch(tree_url, directory / "git-tree.json")
        tree = json.loads(tree_raw)
        if tree.get("truncated") or tree.get("sha") != commit:
            raise RuntimeError("publisher tree is incomplete or changed")
        modules = {entry["path"]: entry["sha"] for entry in tree["tree"] if entry["mode"] == "160000"}
        config = configparser.ConfigParser()
        config.read_string(gitmodules)
        for section in config.sections():
            module_path, module_url = config[section]["path"], config[section]["url"]
            if not module_url.startswith("https://github.com/") or module_path not in modules:
                raise RuntimeError("unresolved/non-HTTPS native submodule")
            repo = module_url.removeprefix("https://github.com/").removesuffix(".git")
            submodules.append(prepare((name + "-" + module_path.replace("/", "-"), modules[module_path], repo, modules[module_path])))
    if not native_sources and not submodules:
        raise RuntimeError("native preferred sources missing: " + name)
    record = {"name": name, "version": version, "source": url, "commit": commit, "file": str(path),
              "sha256": hashlib.sha256(data).hexdigest(), "size": len(data), "sourceNotices": notices,
              "nativeSourcePaths": native_sources, "submodules": submodules, "qualificationPassed": False}
    print("verified native JAR project source", name, commit, len(native_sources), "native source files", flush=True)
    return record


def main():
    OUT.mkdir(exist_ok=True)
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        records = list(executor.map(prepare, PROJECTS))
    (OUT / "source-lock.json").write_text(json.dumps({"schemaVersion": 1, "projects": records, "qualificationPassed": False}, indent=2) + "\n")


if __name__ == "__main__":
    main()
