#!/usr/bin/env python3
"""Freeze independently reviewed native core materials without changing ARM locks.

Only this finite measured set may reuse complete unchanged publisher source.
PostgreSQL requires a separate source-variant review and is deliberately excluded.
"""
import argparse, hashlib, json, pathlib, re, shutil, tarfile
import yaml

EXPECTED = {
    'openbao': ('71156a1c6623a5fa3f5e61b0c6a8ead0faf0df29a778339188443551995d1315', 'kvWriteReadDeletePassed'),
    'keycloak': ('82a77884f3af238beab1e7afd63b5f530e1b5c0590bd7aa60b40a40463e29b2c', 'oidcDiscoveryPassed'),
    'seaweedfs': ('ce9e796f1fe6f06968f4c04bdaf8f678dad9c8acdfef3d244133d71bfa6bf882', 'filerWriteReadDeletePassed'),
    'vmalert': ('b33ab710a7c4f68928c7d832222c9892952736f72e40012449102787fdfae135', 'ruleEvaluationPassed'),
}
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--component', choices=EXPECTED, required=True)
p.add_argument('--image-oci', type=pathlib.Path, required=True)
p.add_argument('--poc-report', required=True)
a = p.parse_args()
root = pathlib.Path.cwd()
name = a.component
sha = lambda b: 'sha256:' + hashlib.sha256(b).hexdigest()
def file_sha(f):
    with pathlib.Path(f).open('rb') as stream:
        return 'sha256:' + hashlib.file_digest(stream, 'sha256').hexdigest()
evidence = root/'docs/evidence/amd64-deployment-20261009'
receipt = json.loads((evidence/(name+'-receipt.json')).read_text())
parity = json.loads((evidence/(name+'-dependency-parity.json')).read_text())
proof = json.loads((evidence/(name+'-native-poc-result.json')).read_text())
expected, capability = EXPECTED[name]
image = receipt['reference'].split('@')[1]
assert expected and image == 'sha256:'+expected, 'unreviewed native image identity'
assert receipt['architecture'] == proof['architecture'] == 'linux/amd64'
assert not parity['amd64Only'] and not parity['arm64Only'], 'native dependencies require a separate source variant'
assert proof.get('passed') and proof.get(capability), 'native capability proof missing'
assert file_sha(a.image_oci) == receipt['archiveSHA256']
# Upstream indexes retain sibling descriptors, while the offline archive carries
# only the selected native closure. Authenticate the index and selected branch.
with tarfile.open(a.image_oci) as oci:
    index = json.load(oci.extractfile('index.json'))
    descriptor = index['manifests'][0]
    assert len(index['manifests']) == 1 and descriptor['digest'] == image
    def read_blob(item):
        member = oci.getmember('blobs/sha256/'+item['digest'].split(':')[1])
        assert member.size == item['size']
        data = oci.extractfile(member).read()
        assert sha(data) == item['digest']
        return data
    upstream = json.loads(read_blob(descriptor))
    native = [x for x in upstream['manifests'] if x.get('platform', {}).get('os') == 'linux' and x.get('platform', {}).get('architecture') == 'amd64']
    assert len(native) == 1 and native[0]['digest'] == receipt['nativeDigest']
    manifest = json.loads(read_blob(native[0]))
    config = json.loads(read_blob(manifest['config']))
    assert config['os'] == 'linux' and config['architecture'] == 'amd64'
    for layer in manifest['layers']:
        read_blob(layer)
original = yaml.safe_load((root/'bundle/component-catalog.yaml').read_text())
c = next(x for x in original['components'] if x['name'] == name)
archive = root/'artifacts/task27-materials/corresponding-sources'/(name+'-source.tar')
assert file_sha(archive) == c['correspondingSourceBundleSHA256']
with tarfile.open(archive) as source:
    inventory = json.load(source.extractfile('SOURCE-INVENTORY.json'))
    for member in inventory['files']:
        info = source.getmember(member['path'])
        assert info.size == member['size'] and sha(source.extractfile(info).read()) == 'sha256:'+member['sha256']
for notice in c['fileLicenses']:
    assert file_sha(root/notice['path']) == notice['digest']
registry = root/'internal/supplychain/licenses/native-core-amd64-reviewed.json'
data = json.loads(registry.read_text()) if registry.exists() else {'schemaVersion':1, 'licenses':[]}
data['licenses'] = [x for x in data['licenses'] if x['component'] != name]
old = json.loads((root/'internal/supplychain/licenses/native-core-reviewed.json').read_text())
mapping = {}
scopes = []
for record in old['licenses']:
    if record['component'] != name: continue
    assert record['imageDigest'] == c['digest'] and record['correspondingSourceBundleSHA256'] == c['correspondingSourceBundleSHA256']
    item = dict(record)
    item['imageDigest'] = image
    item['id'] = 'LicenseRef-Task27-AMD64-Native-' + hashlib.sha256((record['id']+image).encode()).hexdigest()
    mapping[record['id']] = item['id']
    scopes.append(item)
data['licenses'].extend(scopes)
for item in c['fileLicenses'] + c['dependencyClosure']:
    item['license'] = mapping.get(item['license'], item['license'])
c.update(digest=image, architectures=['linux/amd64'], pocReport=a.poc_report)
paths = ['docs/evidence/amd64-deployment-20261009/'+name+x for x in ('-shared-source-integrity.json', '-dependency-parity.json', '-native-poc-result.json')]
c['conformanceFixtures'] += paths
catalog = root/'bundle/component-catalog-amd64.yaml'
text = catalog.read_text()
begin = re.search(r'^  - name: '+re.escape(name)+r'\n', text, re.M).start()
end_match = re.search(r'^(?:  - name: |firstPartyKernels:)', text[begin+1:], re.M)
end = begin+1+end_match.start() if end_match else len(text)
entry = ''.join('  '+line+'\n' for line in yaml.safe_dump([c], sort_keys=False, width=150).splitlines())
catalog.write_text(text[:begin]+entry+text[end:])
registry.write_text(json.dumps(data, indent=2)+'\n')
review = root/'third_party/admission/core-amd64'/(name+'.json')
review.parent.mkdir(parents=True, exist_ok=True)
review.write_text(json.dumps({'state':'qualified', 'architecture':'linux/amd64', 'imageDigest':image, 'nativeDigest':receipt['nativeDigest'], 'correspondingSourceBundleSHA256':c['correspondingSourceBundleSHA256'], 'inventoryFilesVerified':len(inventory['files']), 'dependencyNameVersionParity':'exact', 'nativeCapabilityProof':paths[-1], 'nativeLicenseScopes':scopes, 'deploymentAcceptance':'separate; not installed release acceptance'}, indent=2)+'\n')
for path in [a.poc_report, *paths, str(review.relative_to(root))]:
    target = root/'bundle/evidence'/path
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(root/path, target)
print(json.dumps({'component':name, 'imageDigest':image, 'nativeLicenseScopes':len(scopes), 'deploymentAcceptance':'NOT_RUN'}))
