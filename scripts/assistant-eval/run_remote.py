#!/usr/bin/env python3
"""Run the owned five-case integration binary on the authorized remote host.
Secrets stay in environment; observations and logs live outside the Git checkout.
No model HTTP client, package installation, image rebuild or production rollout.
"""
import argparse
import os
from pathlib import Path
import subprocess
import urllib.parse


def read_env(path):
    return dict(line.split('=', 1) for line in path.read_text().splitlines() if '=' in line)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', type=Path, default=Path('/opt/ops-assistant-dev-20261012'))
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--output-dir', type=Path, required=True)
    args = parser.parse_args()
    if not args.base.is_absolute() or not args.binary.is_absolute() or not args.output_dir.is_absolute():
        parser.error('absolute task-owned paths required')
    args.output_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    values = read_env(args.base / 'database.env')
    bao = read_env(args.base / 'bao/token.env')
    env = os.environ.copy()
    env.update(SP03_TEST_DATABASE_URL='postgres://postgres:' + urllib.parse.quote(values['POSTGRES_PASSWORD'], safe='') + '@127.0.0.1:15439/postgres?sslmode=disable', SP03_TEST_OPENBAO_URL='https://127.0.0.1:18200', SP03_TEST_OPENBAO_CA_FILE=str(args.base / 'bao/vault-ca.pem'), SP03_TEST_OPENBAO_TOKEN=bao['BAO_DEV_ROOT_TOKEN_ID'], OPS_ASSISTANT_REAL_MODEL='1', OPS_ASSISTANT_EVALUATION='1', OPS_ASSISTANT_EVALUATION_REPEATS='1', OPS_ASSISTANT_EVALUATION_OUTPUT=str(args.output_dir / 'observations.jsonl'), OPS_ASSISTANT_EVALUATION_SUITE=str(args.base / 'evaluation-src/test/fixtures/assistant/evaluation/acceptance-v1.json'), OPS_ASSISTANT_TEST_INVESTIGATOR_IMAGE='ops.local/amd64-poc/investigator:20261010-r12', OPS_ASSISTANT_TEST_PYTHON_SOURCE=str(args.base / 'python/src'))
    with (args.output_dir / 'runtime.log').open('x', encoding='utf-8') as log:
        result = subprocess.run([str(args.binary), '-test.run', '^TestAssistantChineseEvaluation$', '-test.v', '-test.timeout', '22m'], cwd=args.base / 'repo/test/integration', env=env, stdout=log, stderr=subprocess.STDOUT)
    print('Runtime exit:', result.returncode)
    return result.returncode


if __name__ == '__main__':
    raise SystemExit(main())
