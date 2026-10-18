#!/usr/bin/env python3
"""Run the isolated five-source fault-chain acceptance on 192.168.0.63.

Credentials come from the existing private task files and are only placed in the
child process environment. Nothing is printed.
"""
import argparse
import json
import os
import subprocess
import urllib.parse
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--name", required=True)
parser.add_argument("--test", required=True)
parser.add_argument("--sources", required=True)
parser.add_argument("--http-proof", required=True)
parser.add_argument("--windows-proof", required=True)
args = parser.parse_args()
if not args.name.replace("-", "").isalnum():
    raise SystemExit("safe unique run name required")

base = Path("/opt/ops-five-source-reverify-20261018/round5")
old = Path("/opt/ops-assistant-dev-20261012")
values = dict(line.split("=", 1) for line in (old / "database.env").read_text().splitlines() if "=" in line)
bao = dict(line.split("=", 1) for line in (old / "bao/token.env").read_text().splitlines() if "=" in line)

env = os.environ.copy()
env.update(
    SP03_TEST_DATABASE_URL="postgres://postgres:" + urllib.parse.quote(values["POSTGRES_PASSWORD"], safe="") + "@127.0.0.1:15439/postgres?sslmode=disable",
    SP03_TEST_OPENBAO_URL="https://127.0.0.1:18200",
    SP03_TEST_OPENBAO_CA_FILE=str(old / "bao/vault-ca.pem"),
    SP03_TEST_OPENBAO_TOKEN=bao["BAO_DEV_ROOT_TOKEN_ID"],
    OPS_TEST_ARCHIVE_HOST_NETWORK="1",
    OPS_TEST_ARCHIVE_IMAGE=subprocess.check_output(["docker", "image", "inspect", "--format", "{{.Id}}", "ops.local/amd64-poc/seaweedfs:4.47"], text=True).strip(),
    OPS_ASSISTANT_REAL_MODEL="1",
    OPS_ASSISTANT_RUNTIME_ACCEPTANCE="1",
    OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY="1",
    OPS_ASSISTANT_OPERATIONAL_SOURCES="1",
    OPS_ASSISTANT_LIVE_SOURCE_DIR=args.sources,
    OPS_ASSISTANT_RUNTIME_PRIVATE=str(base.parent / "runtime-private"),
    OPS_ASSISTANT_TEST_INVESTIGATOR_IMAGE="ops.local/amd64-poc/investigator:20261010-r12",
    OPS_ASSISTANT_TEST_PYTHON_SOURCE=str(base.parent / "repo" / "services" / "investigator" / "src"),
    OPS_FAULT_CHAIN_RUNTIME="1",
    OPS_FAULT_CHAIN_PROOF=args.http_proof,
    OPS_FAULT_CHAIN_WINDOWS_PROOF=args.windows_proof,
    OPS_FAULT_CHAIN_MODEL=os.environ.get("OPS_FAULT_CHAIN_MODEL", "0"),
    OPS_FAULT_CHAIN_RUNTIME_REPORT=str(base / (args.name + "-runtime-report.json")),
)
log = base / (args.name + ".log")
code = 1
with log.open("x") as handle:
    code = subprocess.run(
        [str(base / "ops-five-source-20261018-timing.test"), "-test.run", args.test, "-test.v", "-test.timeout", "26m"],
        cwd=base.parent / "repo" / "test" / "integration",
        env=env,
        stdout=handle,
        stderr=subprocess.STDOUT,
    ).returncode
text = log.read_text()
passed = [line for line in text.splitlines() if line.startswith("--- PASS: Test")]
skipped = [line for line in text.splitlines() if line.startswith("--- SKIP: Test")]
failed = [line for line in text.splitlines() if line.startswith("--- FAIL: Test")]
print(json.dumps({"exit": code, "passed": len(passed), "failed": len(failed), "skipped": len(skipped), "log": str(log)}))
raise SystemExit(code)
