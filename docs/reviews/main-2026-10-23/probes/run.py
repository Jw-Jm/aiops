"""Reproduce review findings without editing production sources or using live services."""
import json
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[4]
probes = Path(__file__).resolve().parent
evidence = probes.parent / "evidence"
with tempfile.TemporaryDirectory(prefix="ops-main-review-") as directory:
    overlay = Path(directory) / "overlay.json"
    targets = {
        "auth": "native-stepup.go.txt",
        "investigation": "native-job.go.txt",
        "resourcestore": "history-relations.go.txt",
        "investigation/mcp": "graph-budget.go.txt",
        "observability": "collector-metrics.go.txt",
        "incident": "native-incident.go.txt",
        "graph": "native-worker-scope.go.txt",
        "inspection/kubernetes": "node-ready-lifecycle.go.txt",
        "integrations/openbao": "openbao-lifecycle.go.txt",
    }
    overlay.write_text(json.dumps({"Replace": {
        str(root / "internal" / package / "main_review_probe_test.go"): str(probes / backing)
        for package, backing in targets.items()
    }}))
    commands = [
        ("native-sse-probe.log", ["node", "--experimental-strip-types", str(probes / "native-sse.mjs")]),
        ("go-probes.log", ["go", "test", "-overlay", str(overlay),
         *["./internal/" + package for package in targets], "-run", "^TestReview", "-count=1", "-v"]),
    ]
    for name, command in commands:
        result = subprocess.run(command, cwd=root, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        (evidence / name).write_text(result.stdout)
        print(result.stdout, end="")
        if result.returncode:
            raise SystemExit(result.returncode)

# Reuse the existing scope harness; put the diagnostic next to it only while
# Vitest resolves its relative imports, then remove the owned temporary file.
with tempfile.NamedTemporaryFile(prefix="main_review_", suffix=".test.tsx", dir=root / "web/tests/v12", delete=False) as file:
    target = Path(file.name)
try:
    harness = (root / "web/tests/v12/p1_assistant_scope.test.tsx").read_text()
    target.write_text(harness + "\n" + (probes / "assistant-busy.test.tsx.txt").read_text())
    result = subprocess.run(["pnpm", "exec", "vitest", "run", str(target.relative_to(root / "web")), "-t", "review probe:"], cwd=root / "web", text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (evidence / "assistant-busy-probe.log").write_text(result.stdout)
    print(result.stdout, end="")
    if result.returncode:
        raise SystemExit(result.returncode)
finally:
    target.unlink()

with tempfile.NamedTemporaryFile(prefix="main_review_", suffix=".test.tsx", dir=root / "web/tests", delete=False) as file:
    target = Path(file.name)
try:
    harness = (root / "web/tests/fault_chain_checks.test.tsx").read_text()
    target.write_text(harness + "\n" + (probes / "fault-chain-late.test.tsx.txt").read_text())
    result = subprocess.run(["pnpm", "exec", "vitest", "run", str(target.relative_to(root / "web")), "-t", "review probe:"], cwd=root / "web", text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (evidence / "fault-chain-late-probe.log").write_text(result.stdout)
    print(result.stdout, end="")
    if result.returncode:
        raise SystemExit(result.returncode)
finally:
    target.unlink()
