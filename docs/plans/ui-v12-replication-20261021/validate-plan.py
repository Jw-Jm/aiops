"""Validate the implementation document bundle without running platform services."""
from pathlib import Path
from collections import Counter
import csv
import json
import re
from urllib.parse import unquote

root = Path(__file__).resolve().parent
repo = root.parents[2]
project = repo.parent
base = json.loads((root / "baseline.json").read_text())
manifest_path = project / base["design_package"] / "manifest.json"
manifest = json.loads(manifest_path.read_text())
errors = []
checks = []


def require(condition, message):
    (checks if condition else errors).append(message)


def read_csv(name):
    with (root / name).open(encoding="utf-8-sig", newline="") as stream:
        return list(csv.DictReader(stream))


tasks = read_csv("tasks.csv")
slices = read_csv("slice-map.csv")
contracts = read_csv("contracts.csv")
task_map = {row["task_id"]: row for row in tasks}
contract_map = {row["contract_id"]: row for row in contracts}
require(len(tasks) == len(task_map) == base["task_count"] == 39, "39 unique tasks")
require(len(slices) == len({s["slice_id"] for s in slices}) == manifest["total"] == 458, "458 unique slices")
require({s["slice_id"] for s in slices} == {s["id"] for s in manifest["slices"]}, "exact manifest slice coverage")
require(len(manifest["routes"]) == base["route_count"] == 22, "22 menu routes")
require(manifest["sourceSHA256"] == base["source_sha256"], "fixed design source identity")
require(dict(Counter(s["module"] for s in slices)) == base["module_counts"], "module counts match manifest baseline")

visiting, done = set(), set()


def visit(task_id):
    if task_id in done:
        return
    if task_id in visiting:
        errors.append("dependency cycle at " + task_id)
        return
    visiting.add(task_id)
    for dep in filter(None, task_map[task_id]["depends_on"].split(";")):
        require(dep in task_map, task_id + " dependency exists: " + dep)
        if dep in task_map:
            visit(dep)
    visiting.remove(task_id)
    done.add(task_id)


for task_id, row in task_map.items():
    visit(task_id)
    require(row["status"] == "待实施", task_id + " not marked implemented")
    for target in row["code_targets"].split(";"):
        require((repo / target).exists(), task_id + " candidate path: " + target)
require(len(done) == len(task_map), "dependency traversal covers all tasks")

# Parse only top-level path keys and direct HTTP operations in this repository's YAML.
apis = {}
current = None
for line in (repo / "api/openapi/platform-v1.yaml").read_text().splitlines():
    match = re.match(r"^  (/api\S+):(?:\s*(.*))$", line)
    if match:
        current = match.group(1)
        apis.setdefault(current, set())
        rest = match.group(2)
        if rest.startswith("{"):
            try:
                apis[current].update(k.upper() for k in json.loads(rest) if k in {"get", "post", "patch", "put", "delete"})
            except json.JSONDecodeError:
                errors.append("unsupported inline OpenAPI shape: " + current)
    elif current:
        match = re.match(r"^    (get|post|patch|put|delete):", line)
        if match:
            apis[current].add(match.group(1).upper())

require(len(contracts) == len(contract_map) == base["contract_operations"] == 179, "179 unique contract rows")
require(len({(c["method"], c["path"]) for c in contracts}) == len(contracts), "unique method/path operations")

# The frozen design baseline is an identity, not a moving target: the
# 2026-10-21 snapshot is 119 existing and 60 proposed operations. The current
# classification in contracts.csv legitimately advances proposed operations to
# existing as endpoints land, so the counts are checked separately: the frozen
# split must stay 119/60, and the current rows must be fully classified and
# match the OpenAPI reality.
FROZEN_EXISTING = 119
FROZEN_PROPOSED = 60


def baseline_errors(frozen):
    """Drift of the immutable frozen baseline is always a failure."""
    problems = []
    if frozen.get("existing_contract_operations") != FROZEN_EXISTING:
        problems.append("frozen existing baseline drifted from 119")
    if frozen.get("proposed_contract_operations") != FROZEN_PROPOSED:
        problems.append("frozen proposed baseline drifted from 60")
    if frozen.get("existing_contract_operations", 0) + frozen.get("proposed_contract_operations", 0) != frozen.get("contract_operations"):
        problems.append("frozen baseline is internally inconsistent")
    return problems


def contract_errors(rows, frozen, operations):
    """Every contract row is classified, and the classification matches reality.

    baseline.json records the frozen design-time split; contracts.csv records
    the current implementation state and legitimately advances a proposed
    operation to existing once its endpoint exists. A wrong classification,
    an unclassified row or a regression below the frozen baseline must fail.
    """
    problems = []
    for row in rows:
        cid = row["contract_id"]
        exists = row["method"] in operations.get(row["path"], set())
        if row["primary_task"] not in task_map:
            problems.append(cid + " primary task exists")
        if not (exists if row["status"].startswith("已有") else not exists):
            problems.append(cid + " existing/proposed classification")
    existing = sum(c["status"].startswith("已有") for c in rows)
    proposed = sum(c["status"] == "拟新增" for c in rows)
    if existing + proposed != len(rows):
        problems.append("every contract row is classified as existing or proposed")
    if existing < frozen["existing_contract_operations"]:
        problems.append("existing operations regressed below the frozen baseline")
    if proposed > frozen["proposed_contract_operations"]:
        problems.append("proposed operations grew above the frozen baseline")
    return problems


require(not baseline_errors(base), "frozen baseline identity is unchanged: " + "; ".join(baseline_errors(base)))
for row in contracts:
    cid = row["contract_id"]
    exists = row["method"] in apis.get(row["path"], set())
    require(row["primary_task"] in task_map, cid + " primary task exists")
    require(exists if row["status"].startswith("已有") else not exists, cid + " existing/proposed classification")
current_problems = contract_errors(contracts, base, apis)
require(not current_problems, "current classification refines the frozen baseline: " + "; ".join(current_problems))
# The same rules must still reject a drifted baseline or a wrong classification.
require(bool(baseline_errors({**base, "existing_contract_operations": 118, "proposed_contract_operations": 61})), "a mutated frozen baseline is rejected")
drifted = [dict(c) for c in contracts]
drifted[0]["status"] = "拟新增" if drifted[0]["status"].startswith("已有") else "已有-适配"
require(bool(contract_errors(drifted, base, apis)), "a wrong classification is rejected")
require(not contract_errors(contracts, base, apis), "current implementation progress is accepted")

status_fields = ["visual_status", "interaction_status", "api_status", "persistence_status", "authorization_status", "live_status"]
for row in slices:
    sid = row["slice_id"]
    require(row["primary_task"] in task_map, sid + " primary task exists")
    require(all(t in task_map for t in row["related_tasks"].split(";")), sid + " related tasks exist")
    require(all(c in contract_map for c in row["contract_ids"].split(";")), sid + " candidate contracts exist")
    require((project / row["prototype_reference"]).is_file(), sid + " design spec exists")
    require(all("待" in row[f] for f in status_fields), sid + " six implementation statuses remain pending")
    for target in row["existing_code_candidates"].split(";"):
        require((repo / target).exists(), sid + " code candidate exists: " + target)

for file in root.glob("*.md"):
    text = file.read_text()
    for target in re.findall(r"\[[^\]]+\]\(([^)]+)\)", text):
        target = target.strip("<>")
        if re.match(r"[a-zA-Z][a-zA-Z0-9+.-]*:", target) or target.startswith("#"):
            continue
        resolved = file.parent / unquote(target.split("#")[0])
        # validation.json is produced by this invocation.
        require(resolved.exists() or resolved.resolve() == (root / "validation.json").resolve(), file.name + " relative link: " + target)

result = {
    "date": "2026-10-21",
    "scope": "document structure and static contract consistency only",
    "status": "passed" if not errors else "failed",
    "checks_passed": len(checks),
    "tasks": len(tasks), "slices": len(slices), "contracts": len(contracts),
    "errors": errors,
    "business_runtime_tested": False,
}
(root / "validation.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps(result, ensure_ascii=False, indent=2))
raise SystemExit(1 if errors else 0)
