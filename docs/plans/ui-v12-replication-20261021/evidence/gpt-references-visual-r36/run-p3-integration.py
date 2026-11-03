"""Run a compiled integration test only on the owned remote PostgreSQL instance."""
import os
from pathlib import Path
import socket
import subprocess
import sys
from urllib.parse import quote

assert socket.gethostname() == "mssc", "business integration runs only on mssc-direct"
root = Path("/data/ops-v12-native-visual-r24-20261026")
work = root / "r36"
assert len(sys.argv) == 4
binary, selection, log = sys.argv[1:]
assert Path(binary).name == binary and Path(log).name == log
assert binary.endswith(".test") and log.endswith(".log")
env = os.environ.copy()
env["SP03_TEST_DATABASE_URL"] = (
    "postgresql://postgres:"
    + quote((root / "private/pg-password").read_text().strip(), safe="")
    + "@127.0.0.1:15454/postgres?sslmode=disable"
)
with (work / log).open("x") as output:
    result = subprocess.run(
        [str(work / binary), "-test.run=" + selection, "-test.v", "-test.timeout=5m"],
        cwd=work / "p3-repo/test/integration", env=env,
        stdout=output, stderr=subprocess.STDOUT,
    )
print(f"integration exit={result.returncode}; retained log={log}")
sys.exit(result.returncode)
