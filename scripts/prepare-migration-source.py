#!/usr/bin/env python3
"""Prepare the separate db-migrate source/notice closure; never runtime deps."""
import argparse
import importlib.util
import json
import io
import tarfile
from pathlib import Path
import tempfile

spec = importlib.util.spec_from_file_location("runtime_source", Path(__file__).with_name("prepare-runtime-source.py"))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


def prepare(out):
    lock, _ = runtime.prepare(out,
        review_path=Path("third_party/admission/migration-tool-go-license-review.json"),
        entrypoints=("./cmd/db-migrate",), admission_paths=())
    archive_path = out / "runtime-go-source.tar"
    with tarfile.open(archive_path) as source:
        contents = {m.name: source.extractfile(m).read() for m in source.getmembers() if m.isfile()}
    first_party = [Path("cmd/db-migrate/main.go"), *sorted(Path("migrations").glob("*.sql"))]
    lock["firstPartyFiles"] = []
    for path in first_party:
        data = path.read_bytes()
        contents[str(path)] = data
        lock["firstPartyFiles"].append({"path": str(path), "digest": runtime.digest(data)})
    # Preserve original repository module declarations separately. The exact
    # offline rebuild uses only the reviewed migration closure and local source
    # replacements; it neither fetches unused runtime modules nor embeds secrets.
    for name in ("go.mod", "go.sum"):
        data = Path(name).read_bytes()
        contents["first-party-original/" + name] = data
        lock["firstPartyFiles"].append({"path": name, "digest": runtime.digest(data)})
    lines = ["module ops-platform", "", "go 1.27.1", "", "require ("]
    lines.extend("\t" + m["path"] + " " + m["version"] for m in lock["modules"])
    lines.extend([")", "", "replace ("])
    lines.extend("\t" + m["path"] + " => ./modules/" + m["path"] + "@" + m["version"] for m in lock["modules"])
    lines.append(")")
    contents["go.mod"] = ("\n".join(lines) + "\n").encode()
    lock["offlineBuildGoModSHA256"] = runtime.digest(contents["go.mod"])
    lock["buildInstructions"] = "At archive root, use locked go1.27.1 with GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOPROXY=off GOSUMDB=off and empty module cache: go build -mod=mod -trimpath -buildvcs=false -o db-migrate ./cmd/db-migrate. Use packaged migrations; bootstrap version 1, provision distinct LOGINs, then migrate forward as migration_role."
    contents["BUILDING.txt"] = (lock["buildInstructions"] + "\n").encode()
    raw = (json.dumps(lock, indent=2) + "\n").encode()
    contents["runtime-go.lock.json"] = raw
    (out / "runtime-go.lock.json").write_bytes(raw)
    with tarfile.open(archive_path, "w") as archive:
        for name, data in sorted(contents.items()):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(data), 0o644, 0
            archive.addfile(info, io.BytesIO(data))
    return lock, runtime.digest(archive_path.read_bytes())


def check(out):
    lock, source_digest = prepare(out)
    admission = json.loads(Path("bundle/evidence/third_party/admission/migration-tool-source.json").read_text())
    expected = json.loads(Path("bundle/evidence/third_party/admission/migration-tool-go.lock.json").read_text())
    if lock != expected or source_digest != admission["sourceBundleSHA256"]:
        raise ValueError("migration source/notice closure has drifted; renew admission evidence")
    if runtime.digest(Path("cmd/db-migrate/main.go").read_bytes()) != admission["firstPartySourceSHA256"]:
        raise ValueError("migration CLI source has drifted; renew offline binary and real forward-migration evidence")
    return lock, source_digest


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    if args.check:
        with tempfile.TemporaryDirectory(prefix="ops-migration-source-check-") as directory:
            lock, source_digest = check(Path(directory))
    elif args.out:
        lock, source_digest = prepare(args.out)
    else:
        parser.error("--out or --check is required")
    print(json.dumps({"modules": len(lock["modules"]),
        "selectedModuleFiles": sum(len(m["files"]) for m in lock["modules"]),
        "standardLibraryFiles": len(lock["standardLibraryFiles"]), "sourceBundleSHA256": source_digest}))
