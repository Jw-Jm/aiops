import json
import os
from pathlib import Path

root = Path('/data/ops-v12-completion-20261026-r1')
env = json.loads((root / 'private/api-env.json').read_text())
os.execve(str(root / 'platform-api'), [str(root / 'platform-api')], env)
