#!/usr/bin/env python3
"""Check generator reproducibility against the reviewable working files."""
from pathlib import Path
import subprocess
import sys
import shutil
import tempfile
import difflib


def snapshot(root, directories):
    return {
        str(path.relative_to(root)): path.read_bytes()
        for directory in directories
        for path in ([root / directory] if (root / directory).is_file() else (root / directory).rglob("*"))
        if path.is_file()
    }


def main():
    directories = sys.argv[2:]
    root = Path.cwd()
    before = snapshot(root, directories)
    # Use current tracked/untracked source, respecting Git's exclusions for
    # secrets and large local fixtures. Never remove output from the workspace:
    # editors, concurrent builds, and an interrupted check keep their files.
    result = subprocess.run(["git", "ls-files", "--cached", "--others",
                             "--exclude-standard", "-z"], capture_output=True)
    if result.returncode:
        print("check-generated requires the platform Git repository", file=sys.stderr)
        return result.returncode
    with tempfile.TemporaryDirectory(prefix="ops-generated-") as temporary:
        isolated = Path(temporary)
        for relative in set(result.stdout.decode().rstrip("\0").split("\0")):
            if not relative or any(relative == directory or relative.startswith(directory + "/")
                                   for directory in directories):
                continue
            source = root / relative
            if not source.is_file() and not source.is_symlink():
                continue
            destination = isolated / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, destination, follow_symlinks=False)
        dependencies = root / "web/node_modules"
        if dependencies.exists():
            # pnpm requires a real hoist directory; preserve the installed
            # package symlinks inside a private copy rather than reinstalling.
            shutil.copytree(dependencies, isolated / "web/node_modules", symlinks=True)
        result = subprocess.run([sys.argv[1], "generate", "./..."], cwd=isolated)
        if result.returncode:
            return result.returncode
        after = snapshot(isolated, directories)
    changed = sorted(path for path in before.keys() | after.keys()
                     if before.get(path) != after.get(path))
    if changed:
        print("Generated artifacts are out of date; review regenerated changes:", file=sys.stderr)
        print("\n".join(changed), file=sys.stderr)
        for path in changed:
            if path.endswith("index.ts"):
                print("".join(difflib.unified_diff(before.get(path,b"").decode().splitlines(True),after.get(path,b"").decode().splitlines(True),fromfile="working/"+path,tofile="generated/"+path)),file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
