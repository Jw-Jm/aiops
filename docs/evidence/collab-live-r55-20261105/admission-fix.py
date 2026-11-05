#!/usr/bin/env python3
"""r55 follow-up: restore live investigation admission for the cluster-scoped
operator and verify the 00074 dedup semantics end to end.

Root cause (probe-verified against the live DB): the admission PolicyVersion
gate evaluates the r28-read-only tool policy at cluster scope. Every live
operator grant was namespace-scoped, so the evaluator's scopeAllowed was
false for every subject (reasons: tool_default_deny +
authorization_scope_mismatch). The same database shows HTTP-created
investigation jobs on 2026-10-27, before the admin operator grant was
recreated namespace-scoped on 2026-11-03.

Fix: through the real admin API (step-up challenge with password+TOTP,
exactly like the UI), grant the admin a cluster-scoped operator binding. The
grant function replaces the namespace-scoped operator grant, bumps the
authorization version, and revokes the admin sessions. op stays
namespace-scoped on purpose and must remain denied.

Prints no credentials. Results land in admission-fix-results.json.
"""
import base64
import hashlib
import hmac
import http.cookiejar
import json
import ssl
import struct
import subprocess
import time
import urllib.request
import uuid
from pathlib import Path

BASE = "https://192.168.0.63:30471"
PRIVATE = Path("/data/ops-v12-native-visual-r24-20261026/private")
WORK = Path("/data/ops-v12-native-visual-r24-20261026/r55")
CLUSTER_UID = "79014400-9453-4cb4-9678-0fae47981d79"
INCIDENT_B = "01a1a904-a6cc-7117-abd7-9a623279d988"
ACTIVE_POLICY = "01a17b22-56d7-757c-8cfe-99339a3a6e30"

results = []
limitations = []


def record(area, name, ok, detail=None):
    results.append({"area": area, "check": name, "ok": bool(ok),
                    "detail": detail})
    print(("PASS " if ok else "FAIL ") + area + ": " + name)


def psql(sql):
    out = subprocess.run(
        ["docker", "exec", "ops-v12-native-r24-pg", "psql", "-U", "postgres",
         "-d", "ui_v12_r24_native", "-Atc", sql],
        capture_output=True, text=True, check=True)
    return out.stdout.strip()


def canonical(value):
    return json.dumps(value, separators=(",", ":"), sort_keys=True).encode()


class Client:
    def __init__(self, login_name, password):
        self.cookies = http.cookiejar.CookieJar()
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(self.cookies),
            urllib.request.HTTPSHandler(context=ctx))
        self.csrf = None
        self.login(login_name, password)

    def request(self, method, path, body=None, csrf=True, idem=True,
                extra=None):
        headers = {"Origin": BASE, "User-Agent": "Mozilla/5.0 (r55 fix)"}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        if csrf and self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        if idem:
            headers["Idempotency-Key"] = str(uuid.uuid4())
        if extra:
            headers.update(extra)
        req = urllib.request.Request(BASE + path, data=data,
                                     headers=headers, method=method)
        try:
            resp = self.opener.open(req)
            status, payload = resp.status, resp.read()
        except urllib.error.HTTPError as exc:
            status, payload = exc.code, exc.read()
        try:
            return status, json.loads(payload or b"{}")
        except json.JSONDecodeError:
            return status, {}

    def login(self, login_name, password):
        status, body = self.request(
            "POST", "/api/v1/auth/login",
            {"loginName": login_name, "password": password},
            csrf=False, idem=False)
        if status != 201:
            raise SystemExit(f"login failed for {login_name}: {status} {body}")
        self.csrf = body["data"]["csrf"]["token"]


def totp(secret):
    key = base64.b32decode(secret)
    counter = struct.pack(">Q", int(time.time() // 30))
    mac = hmac.new(key, counter, hashlib.sha1).digest()
    offset = mac[-1] & 0x0F
    return str((struct.unpack(">I", mac[offset:offset + 4])[0]
                & 0x7FFFFFFF) % 1000000).zfill(6)


def error_code(body):
    return body.get("code") or (body.get("error") or {}).get("code")


admin_account = json.loads((PRIVATE / "browser-account.json").read_text())
admin_factor = json.loads((PRIVATE / "browser-mfa.json").read_text())
op_account = json.loads((PRIVATE / "r31-operator-account.json").read_text())
member_account = json.loads((PRIVATE / "member-account.json").read_text())

# --- Phase 1: cluster-scoped operator grant through the real admin API ------
admin = Client(admin_account["loginName"], admin_account["password"])
status, session = admin.request("GET", "/api/v1/auth/session",
                                csrf=False, idem=False)
record("grant", "admin session readable", status == 200)
session_version = session["data"]["revocation"]["sessionVersion"]

status, users = admin.request("GET", "/api/v1/admin/users?limit=100",
                              csrf=False, idem=False)
admin_user = next(u for u in users["data"]
                  if u["userId"] == admin_factor["userId"])
authz_version = admin_user["revocation"]["authorizationVersion"]

grant_body = {
    "userId": admin_factor["userId"],
    "role": "operator",
    "scope": {"clusterUids": [CLUSTER_UID], "namespaces": []},
    "expectedAuthorizationVersion": authz_version,
    "reason": "r55 live: restore cluster-scoped operator grant so the "
              "investigation admission policy gate can pass",
}
write_path = "/api/v1/admin/role-bindings"
body_digest = "sha256:" + hashlib.sha256(canonical(grant_body)).hexdigest()

# Fresh TOTP window: a code must not be replayed inside its own window.
time.sleep(30 - time.time() % 30 + 1.5)

status, challenge = admin.request(
    "POST", "/api/v1/auth/step-up-challenges",
    {"operation": "user_administration",
     "target": {"kind": "administrative_write",
                "write": {"method": "POST", "path": write_path,
                          "bodyDigest": body_digest}},
     "expectedSessionVersion": session_version})
record("grant", "step-up challenge created", status == 201,
       {"status": status, "code": error_code(challenge)})
challenge_id = challenge["data"]["challengeId"]
revision = challenge["data"]["revision"]

status, verified = admin.request(
    "POST", f"/api/v1/auth/step-up-challenges/{challenge_id}:verify",
    {"method": "password", "credential": admin_account["password"],
     "expectedChallengeRevision": revision})
record("grant", "password factor verified", status == 200,
       {"status": status, "code": error_code(verified)})
revision = verified["data"]["revision"]

status, verified = admin.request(
    "POST", f"/api/v1/auth/step-up-challenges/{challenge_id}:verify",
    {"method": "totp", "credential": totp(admin_factor["secret"]),
     "expectedChallengeRevision": revision})
record("grant", "TOTP factor verified",
       status == 200 and verified["data"].get("verified") is True,
       {"status": status, "code": error_code(verified)})

status, granted = admin.request(
    "POST", write_path, grant_body,
    extra={"X-Ops-Step-Up-Challenge": challenge_id})
record("grant", "cluster-scoped operator grant created (201)",
       status == 201 and bool(granted.get("data", {}).get("bindingId")),
       {"status": status, "code": error_code(granted),
        "bindingId": granted.get("data", {}).get("bindingId")})

rows = psql("SELECT jsonb_build_object('scope',scope,'status',status)::text"
            f" FROM identity.role_grants WHERE user_id='{admin_factor['userId']}'"
            " AND role='operator' ORDER BY granted_at DESC LIMIT 2").splitlines()
parsed = [json.loads(r) for r in rows]
latest = parsed[0]["scope"] if parsed else {}
record("grant", "DB: new operator grant is cluster-scoped, prior one revoked",
       latest.get("clusterUids") == [CLUSTER_UID]
       and latest.get("namespaces") == []
       and parsed[0]["status"] == "active"
       and (len(parsed) < 2 or parsed[1]["status"] == "revoked"),
       {"rows": rows})

# --- Phase 2: admission + dedup verification --------------------------------
admin = Client(admin_account["loginName"], admin_account["password"])
op = Client(op_account["loginName"], op_account["password"])
member = Client(member_account["loginName"], member_account["password"])
record("setup", "re-login admin/op/member after grant", True)

revision_b = int(psql("SELECT revision FROM incident.records"
                      f" WHERE incident_id='{INCIDENT_B}'"))
jobs_before = int(psql("SELECT count(*) FROM investigation.jobs"
                       f" WHERE incident_id='{INCIDENT_B}'"))
create_body = {"triggerRevision": revision_b, "triggerKind": "manual"}

key1 = str(uuid.uuid4())
status_c, body_c = admin.request(
    "POST", f"/api/v1/incidents/{INCIDENT_B}/investigations", create_body,
    idem=False, extra={"Idempotency-Key": key1})
job_id = (body_c.get("data") or {}).get("jobId")
record("admission", "admin creates investigation (202)", status_c == 202,
       {"status": status_c, "code": error_code(body_c), "jobId": job_id})

status_r, body_r = admin.request(
    "POST", f"/api/v1/incidents/{INCIDENT_B}/investigations", create_body,
    idem=False, extra={"Idempotency-Key": key1})
record("admission", "idempotent replay returns the same job",
       status_r == 202 and (body_r.get("data") or {}).get("jobId") == job_id,
       {"status": status_r, "jobId": (body_r.get("data") or {}).get("jobId")})

status_d, body_d = admin.request(
    "POST", f"/api/v1/incidents/{INCIDENT_B}/investigations", create_body)
jobs_after = int(psql("SELECT count(*) FROM investigation.jobs"
                      f" WHERE incident_id='{INCIDENT_B}'"))
record("admission", "natural dedup: new key, same admission digest, same job",
       status_d == 202 and (body_d.get("data") or {}).get("jobId") == job_id
       and jobs_after == jobs_before + 1,
       {"status": status_d, "jobId": (body_d.get("data") or {}).get("jobId"),
        "jobsBefore": jobs_before, "jobsAfter": jobs_after})

status_o, body_o = op.request(
    "POST", f"/api/v1/incidents/{INCIDENT_B}/investigations", create_body)
status_m, body_m = member.request(
    "POST", f"/api/v1/incidents/{INCIDENT_B}/investigations", create_body)
jobs_final = int(psql("SELECT count(*) FROM investigation.jobs"
                      f" WHERE incident_id='{INCIDENT_B}'"))
record("admission", "namespace-scoped operator and member remain denied",
       status_o == 403 and error_code(body_o) == "FORBIDDEN"
       and status_m == 403 and error_code(body_m) == "FORBIDDEN"
       and jobs_final == jobs_after,
       {"op": [status_o, error_code(body_o)],
        "member": [status_m, error_code(body_m)]})

if job_id:
    row = psql("SELECT requesting_subject||'|'||policy_version||'|'||trigger_revision||'|'||state"
               f" FROM investigation.jobs WHERE job_id='{job_id}'")
    subject, policy, trigger, state = row.split("|")
    record("db", "job row carries admin subject, active policy, trigger revision",
           subject == f"user:{admin_factor['userId']}"
           and policy == ACTIVE_POLICY and int(trigger) == revision_b,
           {"row": row})
    keys = int(psql("SELECT count(*) FROM investigation.request_keys"
                    f" WHERE job_id='{job_id}'"))
    record("db", "both idempotency keys map to the single job", keys == 2,
           {"requestKeys": keys})

    status_x, body_x = admin.request(
        "POST", f"/api/v1/investigations/{job_id}:cancel", {})
    final_state = psql("SELECT state FROM investigation.jobs"
                       f" WHERE job_id='{job_id}'")
    record("cleanup", "verification job cancelled",
           status_x == 200 and final_state in ("cancelled", "failed",
                                               "succeeded", "partial"),
           {"cancelStatus": status_x, "state": final_state})

out = {
    "observedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "base": BASE,
    "incident": INCIDENT_B,
    "checks": results,
    "passed": sum(1 for r in results if r["ok"]),
    "failed": sum(1 for r in results if not r["ok"]),
}
(WORK / "admission-fix-results.json").write_text(
    json.dumps(out, indent=2) + "\n")
print(json.dumps({"passed": out["passed"], "failed": out["failed"]}))
raise SystemExit(1 if out["failed"] else 0)
