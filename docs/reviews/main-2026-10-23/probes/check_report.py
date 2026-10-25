"""Check report IDs, links, ledger scope, baseline and temporary-file cleanup."""
from collections import Counter
import csv
import json
from pathlib import Path
import re
import subprocess

root = Path(__file__).resolve().parents[4]
report = Path(__file__).resolve().parents[1]
git = lambda *args: subprocess.check_output(['git', *args], cwd=root, text=True).strip()
commit = git('rev-parse', 'HEAD')
assert commit == 'a2fa14f419abdc4587f56a1cbae59eb6cbb48c4e'
assert git('rev-parse', 'main') == git('rev-parse', 'origin/main') == commit
assert not git('diff', '--name-only') and not git('diff', '--cached', '--name-only')
rows = list(csv.DictReader((report / 'file-review-ledger.csv').open()))
assert len({row['path'] for row in rows}) == len(rows) == 865
assert all((root / row['path']).is_file() for row in rows)
counts = Counter(row['status'] for row in rows)
assert counts == {'static_reviewed': 689, 'contract_checked': 139, 'material_checked': 23, 'excluded': 14}
inventory = list(csv.DictReader((report / 'inventory.csv').open()))
expected = {row['path'] for row in inventory if row['category'] in ('first_party_source_or_config', 'other_material')}
assert expected == {row['path'] for row in rows}
findings = re.findall(r'^## MR-(\d{3}) · (P\d) · (.+)$', (report / 'findings.md').read_text(), re.M)
assert [int(row[0]) for row in findings] == list(range(1, 21))
priorities = Counter(row[1] for row in findings)
assert priorities == {'P1': 6, 'P2': 14}
plan = (report / 'remediation-plan.md').read_text()
assert all('MR-' + row[0] in plan for row in findings)
broken = []
for path in report.glob('*.md'):
    for link in re.findall(r'\[[^\]]*\]\(([^)]+)\)', path.read_text()):
        if not link.startswith(('https:', 'http:', '#')) and not (path.parent / link.split('#')[0]).exists():
            broken.append({'file': path.name, 'link': link})
assert not broken, broken
temporary = list((root / 'web/tests').rglob('main_review_*.test.tsx'))
assert not temporary
go_log = (report / 'evidence/go-probes.log').read_text()
assert len(re.findall(r'^--- PASS: TestReview', go_log, re.M)) == 10
assert '136 embedded root contracts' in (report / 'evidence/all-contracts.log').read_text()
materials = json.loads((report / 'evidence/material-checks.json').read_text())
assert materials['licenseScopeCount'] == 4095
result = {
    'reviewedCommit': commit, 'lastRemoteFetchDate': '2026-10-25',
    'findings': len(findings), **dict(priorities),
    'ledgerFiles': len(rows), 'ledgerStatuses': dict(counts),
    'inventoryTrackedFiles': len(inventory), 'diagnosticsPassed': 13,
    'embeddedSchemasCompiled': 136, 'materialScopeRecordsChecked': 4095,
    'temporaryWebTestsRemaining': len(temporary), 'trackedChanges': False,
    'brokenLocalReportLinks': broken,
    'enabledFirstPartyFunctionalStaticScopeComplete': True,
    'allTrackedFilesManuallyReadLineByLine': False,
    'allFunctionalFilesRuntimeVerified': False,
    'realEnvironmentAcceptanceExecuted': False,
    'businessFixesImplemented': False,
}
(report / 'evidence/report-check.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(result, ensure_ascii=False))
