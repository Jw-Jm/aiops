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

require(len(contracts) == len(contract_map) == base["contract_operations"] == 138, "138 unique contract rows")
require(len({(c["method"], c["path"]) for c in contracts}) == len(contracts), "unique method/path operations")
for row in contracts:
    cid = row["contract_id"]
    exists = row["method"] in apis.get(row["path"], set())
    require(row["primary_task"] in task_map, cid + " primary task exists")
    require(exists if row["status"].startswith("已有") else not exists, cid + " existing/proposed classification")
require(sum(c["status"].startswith("已有") for c in contracts) == base["existing_contract_operations"] == 62, "62 existing operations")
require(sum(c["status"] == "拟新增" for c in contracts) == base["proposed_contract_operations"] == 76, "76 proposed operations")

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
