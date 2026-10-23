"""Validate implementation tracking only; never certify product behavior.

Checks the current inventory against the fixed design package: every prototype
action is staged (open/prefetch/submit/confirm/local), every confirm-yes has a
per-slice mutation binding, every form visible in any snapshot has a submit
binding, and all 458 states reference the resulting contracts. It does not call
the API, a database, Kubernetes, a model or a browser.
"""
import csv
import glob
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[2]
DESIGN = REPO.parent / 'ui-handoff/ops-platform-v1.2-20261020'
DISPOSITIONS = {'local', 'direct', 'adapt', 'missing'}
STAGES = {'open', 'prefetch', 'submit', 'confirm', 'local'}
CROSS = {'resource-drawer','alert-drawer','ai-resource','ai-incident','observe-ai','observe-device','v11-alert-link','v11-service-list','v11-monitor-open','evidence','execution-detail','investigate-detail','session-open','resource-tab'}
DOM_EXTRAS = {'v11-user-filter','model-filter','evidence-filter','session-rename-form','binding-filter','v11-monitor-form','knowledge-form','source-filter'}


def rows(name):
    with (ROOT / name).open(encoding='utf-8-sig', newline='') as stream:
        return list(csv.DictReader(stream))


def spec_slices():
    return [json.loads(Path(path).read_text()) for path in sorted(glob.glob(str(DESIGN / 'slices/*/spec.json')))]


def extract_visible():
    forms, nonforms = {}, {}
    form_re = re.compile(r'<form\b([^>]*)>(.*?)</form>', re.S)
    field_re = re.compile(r'<(input|select|textarea)\b([^>]*)>', re.S)
    for path in sorted(glob.glob(str(DESIGN / 'slices/*/index.html'))):
        sid = Path(path).parent.name
        html = Path(path).read_text(encoding='utf-8')
        logged_out = 'class="logged-out"' in html
        spans = []
        for match in form_re.finditer(html):
            attrs, body = match.groups()
            fid = re.search(r'id="([^"]+)"', attrs)
            if not fid:
                continue
            spans.append((match.start(), match.end()))
            if fid.group(1) == 'login-form' and not logged_out:
                continue
            fields = []
            for name in re.findall(r'<(?:input|select|textarea)\b[^>]*name="([^"]+)"', body):
                if name not in fields:
                    fields.append(name)
            forms.setdefault(sid, {})[fid.group(1)] = ';'.join(fields)
        dialogs = [(m.start(), m.end()) for m in re.finditer(r'<dialog\b[^>]*\bopen(?:="[^"]*")?[^>]*>.*?</dialog>', html, re.S)]
        for match in field_re.finditer(html):
            if any(a <= match.start() < b for a, b in spans):
                continue
            if logged_out and not any(a <= match.start() < b for a, b in dialogs):
                continue
            attrs = match.group(2)
            name = re.search(r'\bname="([^"]+)"', attrs) or re.search(r'\bid="([^"]+)"', attrs)
            if not name:
                continue
            nonforms.setdefault(sid, {})[name.group(1)] = match.group(1)
    return forms, nonforms


def operations():
    found = {}
    path = None
    for line in (REPO / 'api/openapi/platform-v1.yaml').read_text().splitlines():
        match = re.match(r'^  (/api\S+):\s*(.*)$', line)
        if match:
            path, rest = match.groups()
            found.setdefault(path, set())
            if rest.startswith('{'):
                found[path].update(k.upper() for k in json.loads(rest)
                                   if k in {'get', 'post', 'patch', 'put', 'delete'})
        elif path:
            match = re.match(r'^    (get|post|patch|put|delete):', line)
            if match:
                found[path].add(match.group(1).upper())
    return found


def validate():
    identity_schema = json.loads((REPO/'api/schemas/ui-v12-identity-v1.schema.json').read_text())
    model_schema = json.loads((REPO/'api/schemas/ui-v12-model-v1.schema.json').read_text())
    stepup_schema = json.loads((REPO/'api/schemas/ui-v12-stepup-v1.schema.json').read_text())
    required = lambda schema, name: set(schema['$defs'][name].get('required', []))
    tasks = rows('tasks.csv')
    task_map = {r['task_id']: r for r in tasks}
    visiting, done = set(), set()
    def visit(tid):
        if tid in done: return
        assert tid not in visiting, 'task dependency cycle at '+tid
        visiting.add(tid)
        for dep in filter(None, task_map[tid]['depends_on'].split(';')):
            assert dep in task_map, tid+' missing dependency '+dep
            visit(dep)
        visiting.remove(tid); done.add(tid)
    for tid in task_map: visit(tid)
    deps_valid = len(done) == len(task_map)
    progress = rows('task-progress.csv')
    acceptance = rows('acceptance.csv')
    original = rows('slice-map.csv')
    contracts = rows('contracts.csv')
    acts = rows('evidence/act-bindings.csv')
    forms = rows('evidence/form-bindings.csv')
    confirms = rows('evidence/confirm-bindings.csv')
    states = rows('evidence/state-bindings.csv')
    inventory = rows('evidence/form-inventory.csv')
    nonform_inventory = rows('evidence/nonform-inventory.csv')
    password_variants = rows('evidence/password-form-bindings.csv')
    contract_ids = {row['contract_id'] for row in contracts}
    contract_by_id = {row['contract_id']: row for row in contracts}
    task_ids = {row['task_id'] for row in tasks}
    apis = operations()
    specs = spec_slices()
    spec_by_id = {value['id']: value for value in specs}
    action_names = {a['act'] for s in specs for a in s.get('actions', [])}
    act_modules = {}
    for spec in specs:
        for action in spec.get('actions', []):
            act_modules.setdefault(action['act'], {})
            act_modules[action['act']][spec['module']] = act_modules[action['act']].get(spec['module'], 0) + 1
    overlaid = {}
    for path in glob.glob(str(DESIGN / 'slices/*/index.html')):
        html = Path(path).read_text(encoding='utf-8')
        overlaid[Path(path).parent.name] = re.findall(r'<dialog\b[^>]*\bid="([^"]+)"[^>]*\bopen(?:="[^"]*")?', html)
    extracted, extracted_nonform = extract_visible()
    assert len({f for v in extracted.values() for f in v}) == 54
    recorded = {}
    for row in inventory:
        recorded.setdefault(row['slice_id'], {})[row['form_id']] = row['fields']
    assert recorded == extracted, 'form-inventory.csv is stale versus snapshot DOM'
    recorded_nonform = {}
    for row in nonform_inventory:
        recorded_nonform.setdefault(row['slice_id'], {})[row['control']] = row['kind']
    assert recorded_nonform == extracted_nonform, 'nonform-inventory.csv is stale versus snapshot DOM'
    dom = {}
    for row in inventory:
        dom.setdefault(row['slice_id'], [])
        if row['form_id'] not in dom[row['slice_id']]:
            dom[row['slice_id']].append(row['form_id'])
    nonform = {}
    for row in nonform_inventory:
        nonform.setdefault(row['slice_id'], []).append(row)

    assert len(contract_ids) == len(contracts)
    assert len({(r['method'], r['path']) for r in contracts}) == len(contracts)
    for row in contracts:
        assert row['primary_task'] in task_ids, row['contract_id']
        exists = row['method'] in apis.get(row['path'], set())
        assert exists == row['status'].startswith('已有'), row['contract_id']
    assert any(r['contract_id'] == 'C-139'
               and r['method'] == 'POST'
               and r['path'].endswith('/execution-profiles/{executionProfileId}/versions/{version}:retire')
               for r in contracts)

    assert len(progress) == len({r['task_id'] for r in progress}) == 39
    assert {r['task_id'] for r in progress} == task_ids
    assert len(acceptance) == len({r['slice_id'] for r in acceptance}) == 458
    assert {r['slice_id'] for r in acceptance} == {r['slice_id'] for r in original} == set(spec_by_id)
    states_by_id = {r['slice_id']: r for r in states}
    assert len(states) == len(states_by_id) == 458
    assert set(states_by_id) == set(spec_by_id)

    act_by_id = {r['act_id']: r for r in acts}
    assert len(acts) == len(act_by_id)
    assert set(act_by_id) == action_names - {'confirm-yes'}
    for row in acts:
        assert row['stage'] in STAGES, row['act_id']
        assert row['disposition'] in DISPOSITIONS, row['act_id']
        assert row['task'] in task_ids, row['act_id']
        for cid in filter(lambda value: value and value != '-', row['contract'].split(';')):
            assert cid in contract_ids, (row['act_id'], cid)
        assert row['ui_evidence'].strip() and row['service_evidence'].strip(), row['act_id']
        if row['stage'] in {'open', 'confirm', 'local'}:
            assert row['request'] == '-' and row['handler'] == 'n/a', row['act_id']
            assert row['service_evidence'] == 'n/a', row['act_id']
            if row['stage'] == 'open':
                assert 'form:' in row['gap'] or 'submit' in row['gap'], row['act_id']
            if row['stage'] == 'confirm':
                assert 'confirm-bindings.csv' in row['gap'], row['act_id']
        else:
            assert row['request'] != '-' and row['handler'] not in ('', 'n/a'), row['act_id']
            if row['disposition'] == 'missing':
                assert row['handler'] == 'not implemented', row['act_id']
                assert row['gap'].strip() and row['gap'] != 'none', row['act_id']
            else:
                assert row['service'] not in ('', 'n/a') and row['storage'] not in ('', 'n/a'), row['act_id']
                assert row['service_evidence'] != 'n/a', row['act_id']
        if row['stage'] == 'submit':
            assert row['request'].split(' ', 1)[0] in {'POST', 'PATCH', 'PUT', 'DELETE'}, row['act_id']

    form_by_id = {r['form']: r for r in forms}
    assert len(forms) == len(form_by_id) == 54
    recipe_forms = {st['form'] for s in specs for st in s.get('steps', []) if 'form' in st}
    assert recipe_forms <= set(form_by_id), sorted(recipe_forms - set(form_by_id))
    assert DOM_EXTRAS <= set(form_by_id), sorted(DOM_EXTRAS - set(form_by_id))
    assert set(inventory[i]['form_id'] for i in range(len(inventory))) <= set(form_by_id)
    for row in forms:
        assert row['stage'] == 'submit', row['form']
        assert row['disposition'] in DISPOSITIONS, row['form']
        assert row['task'] in task_ids, row['form']
        for cid in filter(lambda value: value and value != '-', row['contract'].split(';')):
            assert cid in contract_ids, (row['form'], cid)
        assert row['fields'].strip() and row['submit_request'].strip(), row['form']

    confirm_by_slice = {r['slice_id']: r for r in confirms}
    assert len(confirms) == len(confirm_by_slice)
    for row in confirms:
        sid = row['slice_id']
        assert sid in spec_by_id, sid
        assert 'confirm-yes' in {a['act'] for a in spec_by_id[sid].get('actions', [])}, sid
        assert row['disposition'] in DISPOSITIONS and row['task'] in task_ids, sid
        contract = contract_by_id[row['contract']]
        assert contract['method'] == row['mutation_method'] and contract['path'] == row['mutation_path'], sid
        assert row['request_keys'].strip() and row['ui_evidence'].strip(), sid
        if row['disposition'] == 'missing':
            assert row['handler'] == 'not implemented', sid
        else:
            assert row['handler'] not in ('', 'n/a') and row['service'] not in ('', 'n/a'), sid
            assert row['service_evidence'] != 'n/a', sid

    statuses = {'not_verified', 'passed', 'failed', 'blocked', 'not_applicable'}
    dimensions = ('visual', 'interaction', 'api', 'persistence', 'authorization', 'live')
    for item in acceptance:
        sid = item['slice_id']
        spec = spec_by_id[sid]
        state = states_by_id[sid]
        assert item['primary_task'] in task_ids, sid
        for dimension in dimensions:
            assert item[dimension] in statuses, (sid, dimension)
            if item[dimension] in {'passed', 'not_applicable'}:
                assert item['evidence_refs'].strip(), (sid, 'evidence/reason required')
        candidates = set(filter(None, item['contract_candidates'].split(';')))
        assert candidates <= contract_ids, sid
        assert item['contract_candidates'] == original[[r['slice_id'] for r in original].index(sid)]['contract_ids'], sid
        assert state['primary_contract'] in contract_ids, sid
        assert state['primary_contract'] in candidates, (sid, state['primary_contract'])
        pcontract = contract_by_id[state['primary_contract']]
        assert state['primary_read'].replace(' (not implemented)', '') == pcontract['method'] + ' ' + pcontract['path'], sid
        assert state['primary_disposition'] in DISPOSITIONS, sid
        assert state['primary_task'] == item['primary_task'], sid
        assert state['primary_read'].strip() and state['evidence'].strip(), sid
        slice_forms = list(dict.fromkeys(dom.get(sid, [])))
        for st in spec.get('steps', []):
            if 'form' in st and st['form'] not in slice_forms and st['form'] != 'login-form':
                slice_forms.append(st['form'])
        slice_nonform = nonform.get(sid, [])
        surfaces = set(filter(None, state['surfaces'].split(';')))
        assert {a['act'] for a in spec.get('actions', [])} <= surfaces, sid
        assert {'form:' + fid for fid in slice_forms} <= surfaces, sid
        assert {'nonform:' + item['control'] for item in slice_nonform} <= surfaces, sid
        assert set(filter(None, state['nonform_controls'].split(';'))) == {item['control'] for item in slice_nonform}, sid
        assert int(state['surface_instances']) == len(spec.get('actions', [])) + len(slice_forms) + len(slice_nonform), sid
        assert int(state['surface_instances']) == sum(int(state[key]) for key in
               ('open_actions', 'prefetch_actions', 'submit_actions', 'confirm_actions', 'local_stage_actions')), sid
        assert int(state['surface_instances']) == sum(int(state[key]) for key in
               ('local_actions', 'direct_actions', 'adapt_actions', 'missing_actions')), sid
        confirm_acts = sum(1 for a in spec.get('actions', []) if a['act'] == 'confirm-yes') + \
            sum(1 for a in spec.get('actions', []) if a['act'] != 'confirm-yes' and act_by_id[a['act']]['stage'] == 'confirm')
        assert int(state['confirm_actions']) == confirm_acts, sid
        assert item['actions'] == ';'.join(dict.fromkeys(a['act'] for a in spec.get('actions', []))), sid
        assert item['fields'] == ';'.join(f['name'] for f in spec.get('fields', [])), sid
        expected = ('open=%s;prefetch=%s;submit=%s;confirm=%s;local_stage=%s;local=%s;direct=%s;adapt=%s;missing=%s;primary=%s;composite=%s' % (
            state['open_actions'], state['prefetch_actions'], state['submit_actions'], state['confirm_actions'],
            state['local_stage_actions'], state['local_actions'], state['direct_actions'], state['adapt_actions'],
            state['missing_actions'], state['primary_disposition'], state['composite']))
        assert item['disposition'] == expected, (sid, item['disposition'], expected)
        assert set(filter(None, state['active_overlays'].split(';'))) == set(overlaid.get(sid, [])), sid
        cross = any(a['act'] in CROSS and max(act_modules[a['act']], key=act_modules[a['act']].get) != spec['module']
                    for a in spec.get('actions', []))
        expected_composite = 'yes' if (len(overlaid.get(sid, [])) > 1 or cross) else 'no'
        assert state['composite'] == expected_composite, (sid, state['composite'], expected_composite)
        assert state['gap_owner'] in task_ids, sid

    # Stage-specific guards for actions that only open, display, confirm or validate.
    assert act_by_id['session-rename']['stage'] == 'open' and act_by_id['session-rename']['request'] == '-'
    assert 'session-rename-form' in act_by_id['session-rename']['gap']
    assert act_by_id['native-change-password']['stage'] == 'open' and act_by_id['native-change-password']['request'] == '-'
    assert 'native-password-form' in act_by_id['native-change-password']['gap']
    assert act_by_id['session-scope']['stage'] == 'open' and act_by_id['session-scope']['request'] == '-'
    assert act_by_id['session-archive']['stage'] == 'confirm' and act_by_id['session-archive']['request'] == '-'
    assert confirm_by_slice['SL-297']['contract'] == 'C-079'
    assert act_by_id['observe-log-bucket']['stage'] == 'local' and act_by_id['observe-log-bucket']['request'] == '-'
    assert act_by_id['observe-device']['stage'] == 'local' and act_by_id['observe-device']['request'] == '-'
    assert act_by_id['query-source']['stage'] == 'local' and act_by_id['query-source']['request'] == '-'
    assert 'evidence:query' in act_by_id['query-source']['gap']
    assert act_by_id['policy-validate']['contract'] == 'C-177'
    assert act_by_id['policy-validate']['request'].startswith('POST /api/v1/admin/policy-bundles:validate')
    assert ':publish' not in act_by_id['policy-validate']['request']
    assert contract_by_id['C-177']['method'] == 'POST' and contract_by_id['C-177']['path'].endswith(':validate')
    assert contract_by_id['C-177']['status'] == '拟新增'
    assert contract_by_id['C-156']['path'].endswith(':publish')
    publish_form = form_by_id['policy-publish-form']
    assert ':publish' in publish_form['submit_request']
    # No open/confirm/local action may carry a write request.
    for row in acts:
        if row['stage'] in {'open', 'confirm', 'local'}:
            assert not row['request'].upper().startswith(('POST ', 'PATCH ', 'PUT ', 'DELETE ')), row['act_id']

    sl106 = states_by_id['SL-106']
    assert sl106['composite'] == 'yes', sl106['composite']
    assert set(filter(None, sl106['active_overlays'].split(';'))) == {'modal', 'drawer'}, sl106['active_overlays']
    assert 'form:v11-alert-link-form' in sl106['surfaces'].split(';'), sl106['surfaces']
    assert sl106['primary_contract'] == 'C-030' and 'C-031' in sl106['related_reads'].split(';'), sl106
    assert 'C-039' in sl106['gap_summary'], sl106['gap_summary']
    sl106_accept = [r for r in acceptance if r['slice_id'] == 'SL-106'][0]
    assert {'C-030', 'C-031', 'C-039'} <= set(sl106_accept['contract_candidates'].split(';')), sl106_accept['contract_candidates']

    # R5 login/topology/non-form roots.
    for sid in ('SL-001', 'SL-002'):
        state = states_by_id[sid]
        assert state['primary_contract'] == 'C-009' and state['primary_disposition'] == 'missing', sid
        assert state['primary_read'] == 'POST /api/v1/auth/login', sid
        assert 'form:login-form' in state['surfaces'].split(';'), sid
        accept = [r for r in acceptance if r['slice_id'] == sid][0]
        assert 'C-001' not in accept['contract_candidates'].split(';'), sid
        assert 'C-009' in accept['contract_candidates'].split(';'), sid
    assert states_by_id['SL-005']['primary_contract'] == 'C-011', states_by_id['SL-005']['primary_read']
    assert states_by_id['SL-009']['primary_contract'] == 'C-004', states_by_id['SL-009']['primary_read']
    assert {'nonform:graph-root', 'nonform:graph-depth'} <= set(states_by_id['SL-009']['surfaces'].split(';'))
    for item in nonform_inventory:
        if item['control'] in ('graph-root','graph-depth'):
            assert item['stage'] == 'prefetch' and item['contract'] == 'C-004', item
        if item['control'] == 'rca-version':
            assert item['stage'] == 'prefetch' and item['contract'] == 'C-071', item
        if item['control'] in ('cluster-picker','config-cluster','session-search','riskAcknowledged','stepup'):
            assert item['stage'] == 'local', item
    assert 'C-004' in [r for r in acceptance if r['slice_id'] == 'SL-009'][0]['contract_candidates'].split(';')

    # R5 schema/CSV/form projection consistency.
    model_required = required(model_schema, 'connectionUpsertRequest')
    for cid in ('C-051', 'C-052'):
        keys = contract_by_id[cid]['request_keys']
        for name in model_required:
            assert name in keys, (cid, name, keys)
        assert 'providerReportedMax' not in keys
        assert 'credential{action' in keys and 'contextWindow' in keys
    reset_required = required(identity_schema, 'adminPasswordResetRequest')
    assert reset_required == {'initialPassword', 'expectedRevision', 'expectedCredentialVersion'}
    for name in reset_required:
        assert name in contract_by_id['C-017']['request_keys']
    grant_required = required(identity_schema, 'roleGrantRequest')
    assert grant_required == {'userId', 'role', 'scope', 'expectedAuthorizationVersion', 'reason'}
    for name in grant_required:
        assert name in contract_by_id['C-020']['request_keys']
    for name in required(stepup_schema, 'mfaEnrollmentVerifyRequest'):
        assert name in contract_by_id['C-173']['request_keys']
    for name in required(stepup_schema, 'recoveryCodeVerifyRequest'):
        assert name in contract_by_id['C-175']['request_keys']
    for name in required(identity_schema, 'subjectMappingRequest'):
        assert name in contract_by_id['C-170']['request_keys']
    assert 'confirmExplicit' not in contract_by_id['C-170']['request_keys']
    assert 'endpoint:baseURL' in form_by_id['model-form']['fields'] and 'credential:credential.action' in form_by_id['model-form']['fields']
    assert 'context:contextWindow' in form_by_id['model-form']['fields'] and ':credentialRef' not in form_by_id['model-form']['fields']
    assert ':contextLength' not in form_by_id['model-form']['fields']
    assert 'PATCH /api/v1/admin/model-connections/{modelConnectionId}' in form_by_id['model-form']['submit_request']
    assert 'credential{action=replace;apiKey}' in form_by_id['model-form']['submit_request']
    assert 'providerReportedMax' not in form_by_id['model-form']['submit_request']
    assert 'password:password' in form_by_id['login-form']['fields']
    assert 'password:initialPassword' in form_by_id['v11-user-form']['fields'] and 'mustChange:unsupported' in form_by_id['v11-user-form']['fields']
    assert 'PATCH /api/v1/admin/users/{userId}' in form_by_id['v11-user-form']['submit_request']
    update_part = form_by_id['v11-user-form']['submit_request'].split('update:', 1)[1]
    assert 'initialPassword' not in update_part and 'password' not in update_part
    assert 'password:newPassword' in form_by_id['native-password-form']['fields']
    assert 'confirmPassword:local-confirmation' in form_by_id['native-password-form']['fields']
    assert 'confirmPassword' not in form_by_id['native-password-form']['submit_request']
    assert 'mustChange' not in form_by_id['native-password-form']['submit_request']
    binding_req = form_by_id['binding-form']['submit_request']
    for name in ('userId', 'role', 'scope{clusterUids;namespaces}', 'expectedAuthorizationVersion', 'reason'):
        assert name in binding_req, name
    assert 'subject:' not in binding_req and 'subject:subject' not in form_by_id['binding-form']['fields']
    assert 'synchronous' in contract_by_id['C-022']['notes'] and 'lifecycle' in contract_by_id['C-024']['notes']

    variants = {r['slice_id']: r for r in password_variants}
    assert set(variants) == {'SL-004','SL-118','SL-432','SL-433','SL-005','SL-094','SL-370'}
    for sid, variant in variants.items():
        state = states_by_id[sid]
        assert state['form_variants'] == 'native-password-form=' + variant['context'], sid
        assert state['primary_contract'] == variant['contract'], sid
        assert state['primary_read'] == variant['method'] + ' ' + variant['path'], sid
        accept = [r for r in acceptance if r['slice_id'] == sid][0]
        assert variant['contract'] in accept['contract_candidates'].split(';'), sid
        if variant['context'] == 'reset':
            assert 'C-011' not in accept['contract_candidates'].split(';'), sid
            assert 'initialPassword' in variant['body_keys'] and 'expectedRevision' in variant['body_keys']
            assert 'expectedCredentialVersion' in variant['body_keys'] and '{userId}' in variant['path']
        if variant['context'] == 'first':
            assert 'currentPassword' in variant['supplemental'] and 'P2-02' in variant['supplemental']
    # R5 schema/CSV/form projection consistency.
    assert 'credential{action' in contract_by_id['C-051']['request_keys']
    assert 'providerReportedMax' not in contract_by_id['C-051']['request_keys']
    assert 'contextWindow(integer|null)' in contract_by_id['C-051']['request_keys']
    assert 'credential{action' in contract_by_id['C-052']['request_keys']
    assert 'providerReportedMax' not in contract_by_id['C-052']['request_keys']
    assert 'stepUp{challengeId;sessionRef}' in contract_by_id['C-173']['request_keys']
    assert 'challengeId' in contract_by_id['C-175']['request_keys']
    assert 'confirmExplicit' not in contract_by_id['C-170']['request_keys']
    assert 'endpoint:baseURL' in form_by_id['model-form']['fields'] and 'credential:credential.action' in form_by_id['model-form']['fields']
    assert 'context:contextWindow' in form_by_id['model-form']['fields']
    assert ':credentialRef' not in form_by_id['model-form']['fields'] and ':contextLength' not in form_by_id['model-form']['fields']
    assert 'password:password' in form_by_id['login-form']['fields']
    assert 'password:initialPassword' in form_by_id['v11-user-form']['fields']
    assert 'mustChange:unsupported' in form_by_id['v11-user-form']['fields']
    assert 'password:newPassword' in form_by_id['native-password-form']['fields']
    assert 'synchronous' in contract_by_id['C-022']['notes'] and 'lifecycle' in contract_by_id['C-024']['notes']

    for item in progress:
        assert item['status'] in {'not_started', 'in_progress', 'blocked', 'complete'}
        if item['status'] == 'complete':
            assert item['evidence'].strip() and not item['remaining'].strip(), item['task_id']

    dimension_counts = {d: sum(1 for r in acceptance if r[d] == 'passed') for d in dimensions}
    structure_blockers = [r['task_id'] for r in progress if r['task_id'] in {'P0-01', 'P0-02', 'P0-03', 'P0-04'} and r['status'] != 'complete']
    approval_path = ROOT / 'evidence/g0-independent-approval.json'
    baseline = json.loads((ROOT/'evidence/baseline-code.json').read_text())
    approval = json.loads(approval_path.read_text()) if approval_path.exists() else None
    approved = bool(approval and approval.get('status') == 'approved' and approval.get('head') == baseline.get('head'))
    g0_blockers = list(structure_blockers)
    if not approved:
        g0_blockers.append('independent-approval')
    return {'scope': 'tracking structure and action/field stage bindings only',
            'tasks': len(progress), 'slices': len(acceptance), 'contracts': len(contracts),
            'existing_operations': sum(r['status'].startswith('已有') for r in contracts),
            'proposed_operations': sum(r['status'] == '拟新增' for r in contracts),
            'prototype_actions': len(acts) + 1, 'staged_actions': len(acts), 'confirm_instances': len(confirms),
            'prototype_forms': len(forms), 'dom_forms': len({f for v in dom.values() for f in v}),
            'dom_extras': len(DOM_EXTRAS), 'slice_form_rows': len(inventory),
            'stages': {key: sum(int(r[key]) for r in states) for key in
                       ('open_actions', 'prefetch_actions', 'submit_actions', 'confirm_actions', 'local_stage_actions')},
            'state_dispositions': {key: sum(int(r[key]) for r in states) for key in
                                   ('local_actions', 'direct_actions', 'adapt_actions', 'missing_actions')},
            'composite_states': sum(r['composite'] == 'yes' for r in states),
            'product_acceptance_asserted': False,
            'product_six_verified': all(r[d] == 'passed' for r in acceptance for d in dimensions),
            'six_dimension_passed': dimension_counts,
            'g0_structure_ready': (not structure_blockers and deps_valid),
            'task_dependencies_valid': deps_valid,
            'g0_independent_approval': approved,
            'g0_passed': not g0_blockers,
            'g0_blockers': g0_blockers,
            'completed_tasks': [r['task_id'] for r in progress if r['status'] == 'complete']}


if __name__ == '__main__':
    result = validate()
    (ROOT / 'evidence/progress-validation.json').write_text(
        json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False, indent=2))
