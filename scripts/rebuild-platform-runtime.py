#!/usr/bin/env python3
"""Package first-party runtime inputs and prove an offline rebuild of all three CLIs.

The separately admitted runtime archive supplies selected third-party source and
notices. Only go.sum-authenticated go.mod metadata is added for the module graph;
unused module source and generation tools are not required for a runtime build.
"""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[1]
TARGETS = {"ops-api": "platform-api", "ops-worker": "platform-worker", "opsctl": "opsctl"}
BUILD_FLAGS = ["-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-p=2"]


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def escaped(value):
    return "".join("!" + c.lower() if c.isupper() else c for c in value)


def objects(raw):
    decoder = json.JSONDecoder()
    while raw.strip():
        raw = raw.lstrip()
        value, end = decoder.raw_decode(raw)
        yield value
        raw = raw[end:]


def verify_go_mod(data, expected):
    line = (hashlib.sha256(data).hexdigest() + "  go.mod\n").encode()
    actual = "h1:" + base64.b64encode(hashlib.sha256(line).digest()).decode()
    if actual != expected:
        raise ValueError("module metadata differs from tracked go.sum")


def write_file(root, name, data):
    path = PurePosixPath(name)
    if path.is_absolute() or ".." in path.parts or not path.parts:
        raise ValueError("unsafe source archive path")
    target = root.joinpath(*path.parts)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)


def rebuild(source_archive, out, architecture):
    out.mkdir(parents=True, exist_ok=True)
    env = {**os.environ, "GOOS": "linux", "GOARCH": architecture, "CGO_ENABLED": "0",
           "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
           "GOPRIVATE": "", "GONOPROXY": "none", "GONOSUMDB": "none", "GOVCS": "*:off", "GOFLAGS": ""}

    def run(args, cwd=ROOT, build_env=None):
        return subprocess.check_output(args, cwd=cwd, env=build_env or env, text=True)

    if run(["go", "env", "GOVERSION"]).strip() != "go1.27.1":
        raise ValueError("offline rebuild requires locked go1.27.1")
    suffix = "-amd64" if architecture == "amd64" else ""
    expected = json.loads((ROOT / ("bundle/evidence/third_party/admission/platform-runtime-go" + suffix + ".lock.json")).read_text())
    catalog = (ROOT / ("bundle/component-catalog" + suffix + ".yaml")).read_text()
    entry = re.split(r"(?m)^  - name:", catalog.split("  - name: opa-sdk\n", 1)[1], maxsplit=1)[0]
    source_digest = digest(source_archive.read_bytes())
    if "    correspondingSourceBundleSHA256: " + source_digest + "\n" not in entry:
        raise ValueError("runtime source archive differs from the admitted Catalog")
    with tarfile.open(source_archive) as archive:
        contents = {}
        for member in archive:
            if not member.isfile():
                raise ValueError("runtime source archive must contain regular files only")
            if member.name in contents:
                raise ValueError("duplicate runtime source archive path")
            contents[member.name] = archive.extractfile(member).read()
    if json.loads(contents["runtime-go.lock.json"]) != expected:
        raise ValueError("runtime source lock differs from the admitted lock")
    if expected["architecture"] != "linux/" + architecture:
        raise ValueError("runtime source architecture mismatch")

    packages = list(objects(run(["go", "list", "-mod=readonly", "-deps", "-json",
                                *["./cmd/" + target for target in TARGETS.values()]])))
    selected = {(p["Module"]["Path"], p["Module"]["Version"]) for p in packages
                if p.get("Module") and not p["Module"].get("Main")}
    if selected != {(m["path"], m["version"]) for m in expected["modules"]}:
        raise ValueError("runtime build imports an unreviewed module")
    files = {Path("go.mod"), Path("go.sum")}
    for package in packages:
        if package.get("Module", {}).get("Main"):
            for field in ("GoFiles", "SFiles", "CFiles", "HFiles", "SysoFiles", "EmbedFiles"):
                files.update((Path(package["Dir"]) / f).relative_to(ROOT) for f in package.get(field, []))
    first_party = []
    for path in sorted(files):
        data = (ROOT / path).read_bytes()
        if str(path) in contents:
            raise ValueError("first-party source collides with the runtime archive")
        contents[str(path)] = data
        first_party.append({"path": str(path), "digest": digest(data)})

    # Legacy module graphs can read older go.mod versions absent from `go list -m all`.
    sums = {(p, v): h for p, v, h in (line.split() for line in (ROOT / "go.sum").read_text().splitlines())}
    nodes = {node for line in run(["go", "mod", "graph"]).splitlines() for node in line.split()
             if "@" in node and not node.startswith(("go@", "toolchain@"))}
    module_cache = Path(run(["go", "env", "GOMODCACHE"]).strip())
    metadata = []
    for node in sorted(nodes):
        name, version = node.rsplit("@", 1)
        checksum = sums.get((name, version + "/go.mod"))
        if checksum is None:
            continue
        relative = Path(escaped(name)) / "@v" / (escaped(version) + ".mod")
        data = (module_cache / "cache/download" / relative).read_bytes()
        verify_go_mod(data, checksum)
        path = "module-metadata/" + str(relative)
        contents[path] = data
        metadata.append({"path": path, "digest": digest(data), "goModSum": checksum})

    lock = {"schemaVersion": 1, "architecture": "linux/" + architecture,
            "runtimeSourceBundleSHA256": source_digest, "firstPartyFiles": first_party,
            "moduleGraphMetadata": metadata, "buildFlags": BUILD_FLAGS}
    raw = (json.dumps(lock, indent=2) + "\n").encode()
    contents["platform-source.lock.json"] = raw
    (out / "platform-source.lock.json").write_bytes(raw)
    source_path = out / "platform-source.tar"
    with tarfile.open(source_path, "w") as archive:
        for name, data in sorted(contents.items()):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), 0o644, 0
            archive.addfile(info, io.BytesIO(data))

    binaries = []
    with tempfile.TemporaryDirectory(prefix="ops-platform-offline-") as directory:
        task = Path(directory)
        tree, cache, build_cache = task / "source", task / "modules", task / "build-cache"
        tree.mkdir()
        cache.mkdir()
        build_cache.mkdir()
        # Rebuild strictly from the emitted artifact, without the preparation cache.
        with tarfile.open(source_path) as archive:
            for member in archive:
                write_file(tree, member.name, archive.extractfile(member).read())
        for item in metadata:
            relative = item["path"].removeprefix("module-metadata/")
            write_file(cache / "cache/download", relative, (tree / item["path"]).read_bytes())
        for module in expected["modules"]:
            path, version = escaped(module["path"]), escaped(module["version"])
            shutil.copytree(tree / "modules" / (module["path"] + "@" + module["version"]),
                            cache / (path + "@" + version))
            write_file(cache / "cache/download", path + "/@v/" + version + ".ziphash", module["goSum"].encode())
        offline_env = {**env, "GOMODCACHE": str(cache), "GOCACHE": str(build_cache)}
        for binary, target in TARGETS.items():
            print("rebuilding linux/" + architecture + " " + binary, flush=True)
            args = ["go", "build", *BUILD_FLAGS, "-o", str(out / binary), "./cmd/" + target]
            run(args, tree, offline_env)
            reference = task / (binary + "-reference")
            run(["go", "build", *BUILD_FLAGS, "-o", str(reference), "./cmd/" + target])
            actual = digest((out / binary).read_bytes())
            reference_digest = digest(reference.read_bytes())
            if actual != reference_digest:
                raise ValueError("offline binary differs from the repository build: " + binary)
            binaries.append({"name": binary, "sha256": actual, "referenceSHA256": reference_digest,
                             "byteIdentical": True})

    evidence = {"schemaVersion": 1, "architecture": "linux/" + architecture, "goVersion": "go1.27.1",
                "runtimeSourceBundleSHA256": source_digest, "platformSourceBundleSHA256": digest(source_path.read_bytes()),
                "firstPartyFileCount": len(first_party), "compiledModuleCount": len(selected),
                "moduleGraphMetadataCount": len(metadata), "buildFlags": BUILD_FLAGS,
                "GOPROXY": "off", "GOSUMDB": "off", "GOVCS": "*:off", "GOTOOLCHAIN": "local",
                "moduleCacheInitiallyEmpty": True, "buildCacheInitiallyEmpty": True,
                "binaries": binaries, "nativeExecutionTested": False}
    (out / "offline-rebuild.json").write_text(json.dumps(evidence, indent=2) + "\n")
    print(json.dumps(evidence), flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-archive", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--architecture", choices=("arm64", "amd64"), required=True)
    args = parser.parse_args()
    rebuild(args.source_archive.resolve(), args.out.resolve(), args.architecture)
