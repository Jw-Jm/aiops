#!/usr/bin/env python3
"""r55 live acceptance: incident collaboration + investigation admission + execution list filter.

Replicates the semantics of test/integration/sp05_collab_test.go and the 00074
admission dedup over the live deployment (https://192.168.0.63:30471), with DB
cross-checks against ui_v12_r24_native. Writes acceptance-results.json.

Run 2 semantics (corrected after reading internal/incident/collab.go):
- transfer toSubject == caller is a 400 validation error, checked before scope.
- platform_admin has adminOverride on transfer/complete/reopen.
- an incident always keeps at least one linked finding (unlink-sole -> 400);
  every finding in this deployment is the sole finding of its incident, so the
  delete path of unlink is not exercisable over live data (covered by
  integration tests instead).
- comment impact is stored on the comment timeline entry, not incident payload.
- investigation admission and command-executions are gated by runtime config
  (policy denial / SP07 configurationOnly) in this environment.
"""
import json
import ssl
import subprocess
import time
import urllib.request
import urllib.error
import http.cookiejar
import uuid
from pathlib import Path

BASE = "https://192.168.0.63:30471"
ORIGIN = BASE
WORKDIR = Path("/data/ops-v12-native-visual-r24-20261026/r55")
PRIVATE = Path("/data/ops-v12-native-visual-r24-20261026/private")

INCIDENT_A = "01a1a904-a737-7364-b110-c42f24e384f1"  # claim/transfer
INCIDENT_B = "01a1a904-a6cc-7117-abd7-9a623279d988"  # comments/follow-ups/alert-links
FINDING_B = "01a1a904-a55b-7cbc-8872-156bb6e3bf27"   # sole finding linked to B

SUBJECT_ADMIN = "user:01a177f1-372c-7f0f-a9de-85df928fbe31"        # legacy subject path
SUBJECT_OP = "native:01a17c41-37c8-7d27-a35c-5c9f540e55dc"         # native subject path
SUBJECT_FULLVIEW = "native:01a1808b-ad44-7bfe-a7bd-7dd74cb4eef5"   # eligible operator (platform_admin)
SUBJECT_MEMBER = "native:01a177fe-c4ed-7b86-9e45-5ec3d167a26c"     # no grant -> ineligible

results = []
limitations = []
failures = 0


def record(area, name, ok, detail):
    global failures
    if not ok:
        failures += 1
    results.append({"area": area, "check": name, "ok": ok, "detail": detail})
    print(("PASS " if ok else "FAIL ") + area + " :: " + name + " :: " + json.dumps(detail, ensure_ascii=False)[:300])


def limitation(area, text):
    limitations.append({"area": area, "note": text})
    print("NOTE " + area + " :: " + text)


def psql(sql):
    out = subprocess.run(
        ["docker", "exec", "ops-v12-native-r24-pg", "psql", "-U", "postgres",
         "-d", "ui_v12_r24_native", "-Atc", sql],
        capture_output=True, text=True, check=True)
    return out.stdout.strip()


class Client:
    def __init__(self, login_name, password):
        self.subject = None
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        self.cookies = http.cookiejar.CookieJar()
        self.http = urllib.request.build_opener(
            urllib.request.HTTPSHandler(context=ctx),
            urllib.request.HTTPCookieProcessor(self.cookies))
        self.csrf = None
        self.login(login_name, password)

    def request(self, method, path, body=None, csrf=True, idem=True):
        headers = {"Origin": ORIGIN, "User-Agent": "Mozilla/5.0 (r55 acceptance)"}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        if csrf and self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        if idem and method == "POST":
            headers["Idempotency-Key"] = str(uuid.uuid4())
        req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
        try:
            with self.http.open(req) as resp:
                return resp.status, json.loads(resp.read() or b"{}")
        except urllib.error.HTTPError as e:
            raw = e.read() or b"{}"
            try:
                return e.code, json.loads(raw)
            except json.JSONDecodeError:
                return e.code, {"raw": raw.decode(errors="replace")[:500]}

    def login(self, login_name, password):
        status, body = self.request("POST", "/api/v1/auth/login",
                                    {"loginName": login_name, "password": password},
                                    csrf=False, idem=False)
        if status != 201:
            raise SystemExit(f"login failed for {login_name}: {status} {body}")
        self.csrf = body["data"]["csrf"]["token"]
        self.subject = body["data"]["subject"]


def error_code(body):
    err = body.get("error") or {}
    return err.get("code") or body.get("code")


def get_incident(client, incident_id):
    status, body = client.request("GET", f"/api/v1/incidents/{incident_id}", idem=False)
    assert status == 200, f"GET incident {incident_id}: {status} {body}"
    return body["data"]


def facts(inc):
    return {"state": inc.get("state"), "revision": inc.get("revision"),
            "resolvedAt": inc.get("resolvedAt"), "recoveryKnownAt": inc.get("recoveryKnownAt"),
            "suppressedUntil": inc.get("suppressedUntil")}


def timeline_count(incident_id, kind):
    return int(psql(f"SELECT count(*) FROM incident.timeline WHERE incident_id='{incident_id}' AND kind='{kind}'"))


def audit_count(incident_id, event_type):
    return int(psql(f"SELECT count(*) FROM audit.records WHERE entity_id='{incident_id}' AND event_type='{event_type}'"))


accounts = {name: json.loads((PRIVATE / fname).read_text())
            for name, fname in [("admin", "browser-account.json"),
                                ("op", "r31-operator-account.json"),
                                ("member", "member-account.json")]}
admin = Client(accounts["admin"]["loginName"], accounts["admin"]["password"])
op = Client(accounts["op"]["loginName"], accounts["op"]["password"])
member = Client(accounts["member"]["loginName"], accounts["member"]["password"])
record("setup", "login admin/op/member", True,
       {"admin": admin.subject, "op": op.subject, "member": member.subject})

# ---------- Area 1: claim / transfer (incident A) ----------
area = "claim-transfer"
base = get_incident(admin, INCIDENT_A)
rev_a = base["revision"]
base_facts = facts(base)
assignee0 = base.get("assigneeSubject")
record(area, "baseline open with assignee from run 1",
       base.get("state") == "open" and assignee0 == SUBJECT_ADMIN,
       {"assignee": assignee0, **base_facts})

tl0 = timeline_count(INCIDENT_A, "assignment")
au0 = audit_count(INCIDENT_A, "incident.assignment")

# 1. self-claim by current assignee: idempotent, no new timeline/audit
status, _ = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:claim",
                          {"expectedRevision": rev_a, "note": "r55 acceptance self-claim"})
record(area, "self-claim idempotent (no new timeline/audit)",
       status == 200 and timeline_count(INCIDENT_A, "assignment") == tl0
       and audit_count(INCIDENT_A, "incident.assignment") == au0,
       {"status": status, "timelineDelta": timeline_count(INCIDENT_A, "assignment") - tl0})

# 2. other operator claim -> ASSIGNMENT_CONFLICT
status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_A}:claim",
                          {"expectedRevision": rev_a})
record(area, "op claim refused 409 ASSIGNMENT_CONFLICT",
       status == 409 and error_code(body) == "ASSIGNMENT_CONFLICT",
       {"status": status, "code": error_code(body)})

# 3. stale revision claim -> STALE_CONTEXT
status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:claim",
                             {"expectedRevision": rev_a + 99})
record(area, "stale claim refused 409 STALE_CONTEXT",
       status == 409 and error_code(body) == "STALE_CONTEXT",
       {"status": status, "code": error_code(body)})

# 4. transfer to ineligible subject -> INVALID_ARGUMENT
status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                             {"expectedRevision": rev_a, "toSubject": SUBJECT_MEMBER,
                              "reason": "r55 acceptance ineligible"})
record(area, "transfer to ineligible refused 400 INVALID_ARGUMENT",
       status == 400 and error_code(body) == "INVALID_ARGUMENT",
       {"status": status, "code": error_code(body)})

# 5. stale transfer -> STALE_CONTEXT
status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                             {"expectedRevision": rev_a + 99, "toSubject": SUBJECT_OP,
                              "reason": "r55 acceptance stale"})
record(area, "stale transfer refused 409 STALE_CONTEXT",
       status == 409 and error_code(body) == "STALE_CONTEXT",
       {"status": status, "code": error_code(body)})

# 6. transfer to self -> 400 validation (before scope check)
status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                             {"expectedRevision": rev_a, "toSubject": SUBJECT_ADMIN,
                              "reason": "r55 acceptance self transfer"})
record(area, "transfer to self refused 400 INVALID_ARGUMENT",
       status == 400 and error_code(body) == "INVALID_ARGUMENT",
       {"status": status, "code": error_code(body)})

# 7. transfer admin -> op (native recipient path); facts/revision untouched
status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                             {"expectedRevision": rev_a, "toSubject": SUBJECT_OP,
                              "reason": "r55 acceptance handover"})
after = get_incident(admin, INCIDENT_A)
record(area, "transfer admin->op ok, facts+revision untouched",
       status == 200 and after.get("assigneeSubject") == SUBJECT_OP and facts(after) == base_facts,
       {"status": status, "assignee": after.get("assigneeSubject"), "facts": facts(after)})
record(area, "transfer recorded (timeline +1, audit +1)",
       timeline_count(INCIDENT_A, "assignment") == tl0 + 1
       and audit_count(INCIDENT_A, "incident.assignment") == au0 + 1,
       {"timeline": timeline_count(INCIDENT_A, "assignment"),
        "audit": audit_count(INCIDENT_A, "incident.assignment")})

# 8. op hands back to admin; op becomes the (non-admin) ex-assignee
status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                          {"expectedRevision": rev_a, "toSubject": SUBJECT_ADMIN,
                           "reason": "r55 acceptance handback"})
after = get_incident(admin, INCIDENT_A)
record(area, "op handback ok", status == 200 and after.get("assigneeSubject") == SUBJECT_ADMIN,
       {"status": status, "assignee": after.get("assigneeSubject")})

# 9. ex-assignee without admin role transfers -> FORBIDDEN
status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                          {"expectedRevision": rev_a, "toSubject": SUBJECT_FULLVIEW,
                           "reason": "r55 acceptance ex-assignee"})
record(area, "ex-assignee (non-admin) transfer refused 403 FORBIDDEN",
       status == 403 and error_code(body) == "FORBIDDEN",
       {"status": status, "code": error_code(body)})

# 10. platform_admin override: admin is not the assignee but may transfer
status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                             {"expectedRevision": rev_a, "toSubject": SUBJECT_OP,
                              "reason": "r55 acceptance admin override"})
after = get_incident(admin, INCIDENT_A)
record(area, "platform_admin override transfer allowed (200)",
       status == 200 and after.get("assigneeSubject") == SUBJECT_OP and facts(after) == base_facts,
       {"status": status, "assignee": after.get("assigneeSubject")})

# restore: op hands back to admin so the incident ends assigned to admin
status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_A}:transfer",
                          {"expectedRevision": rev_a, "toSubject": SUBJECT_ADMIN,
                           "reason": "r55 acceptance restore"})
after = get_incident(admin, INCIDENT_A)
record(area, "restore assignee=admin, revision never moved",
       status == 200 and after.get("assigneeSubject") == SUBJECT_ADMIN
       and after["revision"] == rev_a,
       {"status": status, "assignee": after.get("assigneeSubject"), "revision": after["revision"]})

# ---------- Area 2: comment with impact (incident B) ----------
area = "comment"
base_b = get_incident(op, INCIDENT_B)
rev_b = base_b["revision"]
status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/comments",
                          {"body": "r55 acceptance comment", "impact": "r55 验收影响面记录"})
after = get_incident(op, INCIDENT_B)
db_impact = psql(f"SELECT coalesce(payload->>'impact','-') FROM incident.timeline "
                 f"WHERE incident_id='{INCIDENT_B}' AND kind='comment' ORDER BY created_at DESC LIMIT 1")
record(area, "comment persisted on timeline, impact stored, revision unchanged",
       status == 200 and after["revision"] == rev_b and db_impact == "r55 验收影响面记录"
       and body.get("data", {}).get("kind") == "comment",
       {"status": status, "revision": after["revision"], "dbImpact": db_impact})

# ---------- Area 3: follow-up lifecycle (incident B) ----------
area = "follow-up"
status, f3 = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups",
                        {"title": "r55 验收待办-run2", "detail": "default owner"})
f3 = f3.get("data", {})
f3id = f3.get("followUpId")
record(area, "create default owner=creator",
       status == 200 and f3.get("ownerSubject") == SUBJECT_OP and f3.get("state") == "open",
       {"status": status, "owner": f3.get("ownerSubject"), "id": f3id})

status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups",
                          {"title": "r55 验收待办-不合格属主", "ownerSubject": SUBJECT_MEMBER})
record(area, "create ineligible owner refused 400 INVALID_ARGUMENT",
       status == 400 and error_code(body) == "INVALID_ARGUMENT",
       {"status": status, "code": error_code(body)})

status, body = member.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups/{f3id}:complete",
                              {"note": "member has no incident authorization"})
record(area, "complete by unauthorized member refused 403 FORBIDDEN",
       status == 403 and error_code(body) == "FORBIDDEN",
       {"status": status, "code": error_code(body)})

status, done = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups/{f3id}:complete",
                          {"note": "r55 验收完成"})
done = done.get("data", {})
db_state = psql(f"SELECT state FROM incident.followups WHERE followup_id='{f3id}'")
record(area, "complete by owner ok",
       status == 200 and done.get("state") == "done" and done.get("completedAt")
       and done.get("completerSubject") == SUBJECT_OP and db_state == "done",
       {"status": status, "state": done.get("state"), "completer": done.get("completerSubject")})

status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups/{f3id}:complete",
                          {"note": "already done"})
record(area, "double complete refused 400 INVALID_ARGUMENT",
       status == 400 and error_code(body) == "INVALID_ARGUMENT",
       {"status": status, "code": error_code(body)})

status, reopened = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups/{f3id}:reopen", {})
reopened = reopened.get("data", {})
cleared = psql(f"SELECT completed_at IS NULL AND completer_subject IS NULL AND resolution_note='' "
               f"FROM incident.followups WHERE followup_id='{f3id}'")
record(area, "reopen clears completion facts",
       status == 200 and reopened.get("state") == "open" and cleared == "t",
       {"status": status, "state": reopened.get("state"), "dbCleared": cleared})

# platform_admin override on complete (admin is neither creator nor owner)
status, over = admin.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups/{f3id}:complete",
                             {"note": "r55 验收 admin override"})
over = over.get("data", {})
record(area, "platform_admin override complete allowed (200)",
       status == 200 and over.get("state") == "done" and over.get("completerSubject") == SUBJECT_ADMIN,
       {"status": status, "state": over.get("state"), "completer": over.get("completerSubject")})

# restore f3 to open
status, _ = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/follow-ups/{f3id}:reopen", {})
db_state = psql(f"SELECT state FROM incident.followups WHERE followup_id='{f3id}'")
record(area, "restore follow-up open", status == 200 and db_state == "open",
       {"status": status, "dbState": db_state})
record(area, "followup timeline recorded",
       timeline_count(INCIDENT_B, "followup") >= 4,
       {"timeline": timeline_count(INCIDENT_B, "followup")})

# ---------- Area 4: alert-links CAS (incident B) ----------
area = "alert-link"
rev = get_incident(op, INCIDENT_B)["revision"]
tl0 = timeline_count(INCIDENT_B, "alert_link")
au0 = audit_count(INCIDENT_B, "incident.alert_link")

status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/alert-links",
                          {"expectedRevision": rev, "findingIds": [FINDING_B],
                           "action": "unlink", "reason": "r55 acceptance unlink sole"})
record(area, "unlink sole finding refused 400 (incident keeps >=1 finding)",
       status == 400 and error_code(body) == "INVALID_ARGUMENT",
       {"status": status, "code": error_code(body)})
limitation(area, "every live finding is the sole finding of its incident, so the unlink "
                 "delete path cannot be exercised without corrupting live incidents; "
                 "delete-path coverage stays with test/integration/sp05_collab_test.go")

status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/alert-links",
                          {"expectedRevision": rev, "findingIds": [str(uuid.uuid4())],
                           "action": "link", "reason": "r55 acceptance missing finding"})
record(area, "link missing finding refused 404 NOT_FOUND",
       status == 404 and error_code(body) == "NOT_FOUND",
       {"status": status, "code": error_code(body)})

status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/alert-links",
                          {"expectedRevision": rev - 1, "findingIds": [FINDING_B],
                           "action": "link", "reason": "r55 acceptance stale"})
record(area, "stale link refused 409 STALE_CONTEXT",
       status == 409 and error_code(body) == "STALE_CONTEXT",
       {"status": status, "code": error_code(body)})

status, body = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/alert-links",
                          {"expectedRevision": rev, "findingIds": [FINDING_B],
                           "action": "link", "reason": "r55 acceptance relink (CAS +1)"})
after = get_incident(op, INCIDENT_B)
link_row = psql(f"SELECT count(*) FROM incident.finding_links WHERE incident_id='{INCIDENT_B}' AND finding_id='{FINDING_B}'")
record(area, "relink advances revision +1 via CAS, link row intact",
       status == 200 and after["revision"] == rev + 1 and link_row == "1",
       {"status": status, "revision": after["revision"], "linkRow": link_row})
record(area, "alert_link timeline +1, audit +1",
       timeline_count(INCIDENT_B, "alert_link") == tl0 + 1
       and audit_count(INCIDENT_B, "incident.alert_link") == au0 + 1,
       {"timeline": timeline_count(INCIDENT_B, "alert_link"),
        "audit": audit_count(INCIDENT_B, "incident.alert_link")})

# ---------- Area 5: investigation admission gate (migration 00074 environment state) ----------
area = "investigation-admission"
rev_now = get_incident(op, INCIDENT_B)["revision"]
jobs_before = int(psql(f"SELECT count(*) FROM investigation.jobs WHERE incident_id='{INCIDENT_B}'"))
role_bindings = int(psql("SELECT count(*) FROM platform.role_bindings"))

status_a, body_a = admin.request("POST", f"/api/v1/incidents/{INCIDENT_B}/investigations",
                                 {"triggerRevision": rev_now, "triggerKind": "manual"})
job_a = (body_a.get("data") or {}).get("jobId")
jobs_mid = int(psql(f"SELECT count(*) FROM investigation.jobs WHERE incident_id='{INCIDENT_B}'"))
status_o, body_o = op.request("POST", f"/api/v1/incidents/{INCIDENT_B}/investigations",
                              {"triggerRevision": rev_now, "triggerKind": "manual"})
jobs_after = int(psql(f"SELECT count(*) FROM investigation.jobs WHERE incident_id='{INCIDENT_B}'"))
record(area, "cluster-scoped admin admitted (202), namespace-scoped op denied, op creates nothing",
       status_a == 202 and bool(job_a)
       and status_o == 403 and error_code(body_o) == "FORBIDDEN"
       and jobs_before <= jobs_mid <= jobs_before + 1 and jobs_after == jobs_mid,
       {"admin": [status_a, job_a], "op": [status_o, error_code(body_o)],
        "jobs": [jobs_before, jobs_mid, jobs_after]})

if job_a:
    status_x, body_x = admin.request("POST", f"/api/v1/investigations/{job_a}:cancel", {})
    state = psql(f"SELECT state FROM investigation.jobs WHERE job_id='{job_a}'")
    record(area, "admitted job reaches a terminal state (cancelled by us or worker-settled)",
           state in ("cancelled", "failed", "succeeded", "partial"),
           {"cancelStatus": status_x, "state": state})

status, body = admin.request("POST", f"/api/v1/incidents/{INCIDENT_B}/investigations",
                             {"triggerRevision": rev_now + 99, "triggerKind": "manual"})
probe_rev = (status, error_code(body))
status, body = admin.request("POST", f"/api/v1/incidents/{uuid.uuid4()}/investigations",
                             {"triggerKind": "manual"})
probe_missing = (status, error_code(body))
record(area, "denial is post-validation (revision/incident checks still reachable)",
       probe_rev == (400, "INVALID_ARGUMENT") and probe_missing == (404, "NOT_FOUND"),
       {"wrongRevision": probe_rev, "missingIncident": probe_missing})

# ---------- Area 6: execution list (SP07 configurationOnly in live) ----------
area = "execution-filter"
status_a, page_a = admin.request("GET", "/api/v1/command-executions?limit=50", idem=False)
status_o, page_o = op.request("GET", "/api/v1/command-executions?limit=50", idem=False)
status_m, page_m = member.request("GET", "/api/v1/command-executions?limit=50", idem=False)
record(area, "uniform 409 CAPABILITY_DISABLED while SP07 runtime is configurationOnly",
       status_a == 409 and status_o == 409 and status_m == 409
       and error_code(page_a) == "CAPABILITY_DISABLED"
       and error_code(page_o) == "CAPABILITY_DISABLED"
       and error_code(page_m) == "CAPABILITY_DISABLED",
       {"admin": [status_a, error_code(page_a)], "op": [status_o, error_code(page_o)],
        "member": [status_m, error_code(page_m)]})
limitation(area, "SP07 is configurationOnly in this deployment (and action.executions has 0 "
                 "rows), so the per-subject authorization filter cannot be exercised over "
                 "live data; coverage stays with TestSP07ExecutionListFiltersByAuthorization.")

receipt = {
    "round": "r55",
    "date": time.strftime("%Y-%m-%d %H:%M:%S %z"),
    "base": BASE,
    "incidents": {"claimTransfer": INCIDENT_A, "collab": INCIDENT_B, "finding": FINDING_B},
    "subjects": {"admin": SUBJECT_ADMIN, "op": SUBJECT_OP,
                 "fullview": SUBJECT_FULLVIEW, "member": SUBJECT_MEMBER},
    "checks": len(results),
    "failures": failures,
    "results": results,
    "limitations": limitations,
}
(WORKDIR / "acceptance-results.json").write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n")
print(f"\n{len(results)} checks, {failures} failures, {len(limitations)} limitations -> {WORKDIR/'acceptance-results.json'}")
raise SystemExit(1 if failures else 0)
