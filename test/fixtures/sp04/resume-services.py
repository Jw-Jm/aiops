"""Resume only the exact SP04 fixture containers; never print credentials."""
import json, pathlib, shlex, ssl, subprocess, urllib.request, os, time
private = pathlib.Path('/tmp/ops-sp04-services-current-dir').read_text().strip()
directory = pathlib.Path(private)
if not directory.name.startswith('ops-sp04-services-') or directory.stat().st_mode & 0o077:
    raise RuntimeError('fixture private directory ownership boundary invalid')
values = {}
for line in (directory/'credentials.env').read_text().splitlines():
    fields = shlex.split(line)
    if len(fields) != 2 or fields[0] != 'export': raise RuntimeError('fixture environment syntax invalid')
    key, value = fields[1].split('=', 1); values[key] = value
owned = {'29605609f704': ('ops-sp04-openbao-20261002', '8200/tcp'), '2c9ddb0c5554': ('ops-sp04-keycloak-20261002', '8080/tcp')}
ports = {}
for identity, (name, port) in owned.items():
    obj = json.loads(subprocess.check_output(['docker','inspect',identity],text=True))[0]
    if obj['Config']['Labels'].get('ops.platform.test') != 'sp04-20261002' or obj['Name'] != '/'+name:
        raise RuntimeError('fixture container ownership mismatch')
    if not obj['State']['Running']: subprocess.run(['docker','start',obj['Id']],check=True,capture_output=True)
    obj = json.loads(subprocess.check_output(['docker','inspect',identity],text=True))[0]
    mapping = obj['NetworkSettings']['Ports'][port]
    if len(mapping) != 1 or mapping[0]['HostIp'] != '127.0.0.1': raise RuntimeError('fixture port boundary changed')
    ports[name] = mapping[0]['HostPort']
    print(json.dumps({'id':obj['Id'],'name':name,'loopbackPort':mapping[0]['HostPort']}))
bao = 'https://127.0.0.1:'+ports['ops-sp04-openbao-20261002']
context = ssl.create_default_context(cafile=values['SP03_TEST_OPENBAO_CA_FILE'])
def request(path, payload=None):
    body = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(bao+'/v1/'+path, body, method='GET' if body is None else 'PUT', headers={'Content-Type':'application/json'})
    return json.loads(urllib.request.urlopen(req,context=context,timeout=5).read())
state = request('sys/seal-status')
if not state['initialized']:
    init = request('sys/init', {'secret_shares':1,'secret_threshold':1})
    (directory/'recovery.json').write_text(json.dumps(init)); os.chmod(directory/'recovery.json',0o600)
    values['SP03_TEST_OPENBAO_TOKEN'] = init['root_token']
    request('sys/unseal', {'key':init['keys'][0]})
    print(json.dumps({'fixtureInMemoryTransitReinitialized':True}))
elif state['sealed']:
    init = json.loads((directory/'recovery.json').read_text()); request('sys/unseal',{'key':init['keys'][0]})
values['SP03_TEST_OPENBAO_URL'] = bao
values['SP03_KEYCLOAK_TEST_ISSUER'] = 'http://127.0.0.1:'+ports['ops-sp04-keycloak-20261002']+'/realms/ops'
values['SP03_KEYCLOAK_TEST_ADMIN_USERNAME'] = 'admin'
(directory/'credentials.env').write_text(''.join('export '+key+'='+shlex.quote(value)+'\n' for key,value in values.items()))
os.chmod(directory/'credentials.env',0o600)
deadline=time.monotonic()+45
while True:
    try:
        with urllib.request.urlopen(values['SP03_KEYCLOAK_TEST_ISSUER']+'/.well-known/openid-configuration',timeout=5) as response:
            if json.load(response)['issuer'] != values['SP03_KEYCLOAK_TEST_ISSUER']: raise RuntimeError('fixture issuer mismatch')
        break
    except (OSError, urllib.error.URLError):
        if time.monotonic()>=deadline: raise RuntimeError('owned Keycloak fixture did not become ready') from None
        time.sleep(0.5)
print(json.dumps({'ready':True,'secretsPrinted':False}))
