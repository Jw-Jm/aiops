"""Syntax and import-boundary check for the pinned Keep Community incident files."""
import argparse
import ast
from pathlib import Path

FILES = (
    "keep/api/models/db/incident.py",
    "keep/api/models/incident.py",
    "keep/api/models/db/rule.py",
    "keep/api/models/db/tenant.py",
    "keep/api/models/alert.py",
    "keep/api/models/severity_base.py",
)
EXPECTED_EXTERNAL_ROOTS = {"pydantic", "retry", "sqlalchemy", "sqlalchemy_utils", "sqlmodel", "pytz"}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", required=True)
    args = parser.parse_args()
    root = Path(args.source_root).resolve(strict=True)
    imports = set()
    for relative in FILES:
        path = root / relative
        tree = ast.parse(path.read_text(), filename=relative)
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                imports.update(alias.name.split(".", 1)[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                imports.add(node.module.split(".", 1)[0])
    if not EXPECTED_EXTERNAL_ROOTS.issubset(imports):
        raise SystemExit(f"missing expected upstream imports: {sorted(EXPECTED_EXTERNAL_ROOTS - imports)}")
    if any("ee" in Path(path).parts for path in root.rglob("*") if path.is_file()):
        raise SystemExit("fixture unexpectedly includes Keep ee/ files")
    print(f"parsed {len(FILES)} selected Keep Community model files with Python AST")
    print("external import roots:", ", ".join(sorted(imports & EXPECTED_EXTERNAL_ROOTS)))
    print("no Keep ee/ files present; runtime imports were not executed")


if __name__ == "__main__":
    main()
