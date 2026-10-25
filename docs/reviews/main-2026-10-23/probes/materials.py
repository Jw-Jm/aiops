"""Read-only structural/material checks. Uses the existing investigator environment."""
import hashlib
import json
from pathlib import Path
import re
import tarfile
import tomllib
import yaml

root = Path(__file__).resolve().parents[4]
out = Path(__file__).resolve().parents[1] / 'evidence/material-checks.json'
results = {}
cache = {}

def digest(path):
    path = root / path
    if path not in cache:
        cache[path] = 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()
    return cache[path]

ids = set()
registries = []
for path in sorted((root / 'internal/supplychain/licenses').glob('*.json')):
    data = json.loads(path.read_text())
    assert data['schemaVersion'] == 1
    for record in data['licenses']:
        assert record['id'] not in ids
        ids.add(record['id'])
        for field in ('imageDigest', 'sourceArchiveSHA256', 'correspondingSourceBundleSHA256'):
            assert re.fullmatch(r'sha256:[a-f0-9]{64}', record[field])
        assert digest(record['noticePath']) == record['noticeDigest']
        assert digest(record['adr']) == record['adrDigest']
    registries.append({'path': str(path.relative_to(root)), 'records': len(data['licenses'])})
results['licenseRegistries'] = registries
results['licenseScopeCount'] = len(ids)
results['licenseBoundary'] = 'Exact retained notice/ADR identity only; no new legal opinion, source archive rebuild or public CVE audit'

catalogs = []
for path in sorted((root / 'bundle').glob('component-catalog*.yaml')):
    data = yaml.safe_load(path.read_text())
    names = [x['name'] for x in data['components']]
    assert len(names) == len(set(names))
    summary = []
    for item in data['components']:
        local_notices = archive_notice_paths = 0
        if item['state'] == 'qualified':
            for notice in item['fileLicenses']:
                assert re.fullmatch(r'sha256:[a-f0-9]{64}', notice['digest'])
                if (root / notice['path']).is_file():
                    assert digest(notice['path']) == notice['digest'], notice['path']
                    local_notices += 1
                else:
                    archive_notice_paths += 1
            for dep in item['dependencyClosure']:
                assert re.fullmatch(r'sha256:[a-f0-9]{64}', dep['digest'])
            assert (root / item['pocReport']).is_file()
            assert all((root / p).is_file() for p in item['conformanceFixtures'])
        summary.append({'name': item['name'], 'state': item['state'], 'version': item['version'],
                        'architectures': item['architectures'], 'dependencies': len(item['dependencyClosure']) if isinstance(item['dependencyClosure'], list) else 'pending',
                        'localNoticesVerified': local_notices, 'sourceArchiveNoticePathsNotRead': archive_notice_paths})
    catalogs.append({'path': str(path.relative_to(root)), 'components': summary})
results['catalogs'] = catalogs

charts = []
for path in sorted((root / 'deploy/addons/victoria/charts').glob('*.tgz')):
    with tarfile.open(path) as archive:
        members = archive.getmembers()
        names = [m.name for m in members]
        assert len(names) == len(set(names))
        assert all(not Path(n).is_absolute() and '..' not in Path(n).parts for n in names)
        meta = next(m for m in members if m.name.count('/') == 1 and m.name.endswith('/Chart.yaml'))
        data = yaml.safe_load(archive.extractfile(meta).read())
        charts.append({'path': str(path.relative_to(root)), 'digest': digest(path),
                       'name': data['name'], 'version': data['version'], 'members': len(members)})
results['charts'] = charts

for name in ('services/investigator/uv.lock', 'poc/pyrca/uv.lock'):
    data = tomllib.loads((root / name).read_text())
    for package in data['package']:
        if 'registry' not in package.get('source', {}):
            continue
        assert package['version'] and package['source']['registry'].startswith('https://')
        for material in package.get('wheels', []) + ([package['sdist']] if 'sdist' in package else []):
            assert material['url'].startswith('https://')
            assert re.fullmatch(r'sha256:[a-f0-9]{64}', material['hash'])
    results[name] = {'packages': len(data['package']), 'structuralCheck': 'PASS', 'upstreamPackagesReviewed': False}

lock = (root / 'services/runner/ansible-runner/requirements.lock').read_text()
logical = re.sub(r'\\\n\s*', ' ', lock)
requirements = []
for line in logical.splitlines():
    if not line.strip() or line.lstrip().startswith('#'):
        continue
    match = re.match(r'^([A-Za-z0-9_.-]+)==([^\s]+)\s+', line)
    assert match and '--hash=sha256:' in line
    hashes = re.findall(r'--hash=sha256:([a-f0-9]+)', line)
    assert all(len(h) == 64 for h in hashes)
    requirements.append({'name': match[1], 'version': match[2], 'hashes': len(hashes)})
results['runnerRequirements'] = requirements
pnpm_documents = list(yaml.safe_load_all((root / 'web/pnpm-lock.yaml').read_text()))
results['pnpmLock'] = {'documents': len(pnpm_documents), 'lockfileVersion': pnpm_documents[0]['lockfileVersion'],
                       'documentKeys': [list(item) for item in pnpm_documents]}

for name in ('api/openapi/platform-v1.yaml', 'api/internal/graph-v1.yaml'):
    document = yaml.safe_load((root / name).read_text())
    refs = []
    def walk(value):
        if isinstance(value, dict):
            if '$ref' in value:
                refs.append(value['$ref'])
            for child in value.values(): walk(child)
        elif isinstance(value, list):
            for child in value: walk(child)
    walk(document)
    for ref in refs:
        assert ref.startswith('#/'), ref
        value = document
        for part in ref[2:].split('/'):
            value = value[part.replace('~1', '/').replace('~0', '~')]
    operations = [op for item in document['paths'].values() for method, op in item.items()
                  if method in ('get', 'post', 'put', 'patch', 'delete')]
    assert len({op['operationId'] for op in operations}) == len(operations)
    results[name] = {'referencesResolved': len(refs), 'operations': len(operations), 'structuralCheck': 'PASS'}
results['sameEmbeddedRealm'] = (root / 'internal/bootstrap/assets/realm-ops.json').read_bytes() == (root / 'deploy/keycloak/realm-ops.json').read_bytes()
assert results['sameEmbeddedRealm']
results['boundary'] = 'Local files only. No install, registry pull, external notice retrieval, live deployment or qualification renewal.'
out.write_text(json.dumps(results, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'registries': len(registries), 'licenseScopes': len(ids), 'charts': len(charts),
                  'runnerRequirements': len(requirements), 'result': str(out)}, ensure_ascii=False))
