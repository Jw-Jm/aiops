#!/usr/bin/env python3
"""Bootstrap the single development stack; private inputs stay outside Git.

Run on the deployment host. This does not mutate or import any legacy database.
Phases are explicit so failures can be inspected before resuming.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
from urllib.parse import quote

import yaml


def run(args, **kwargs):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, **kwargs)
    if result.returncode:
        # Commands may contain credentials, as may dependency stderr.
        raise RuntimeError(f"{Path(args[0]).name} failed (exit {result.returncode}); inspect private phase log")
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["dependencies", "database"])
    parser.add_argument("--root", type=Path, default=Path("/data/ops-current"))
    parser.add_argument("--release", required=True)
    parser.add_argument("--namespace", default="ops-current")
    parser.add_argument("--profile-template", type=Path)
    args = parser.parse_args()
    root, ns = args.root.resolve(), args.namespace
    release = root / "releases" / args.release
    assert release.is_dir() and release.parent == root / "releases"
    private, config, evidence = root / "private", root / "config", root / "evidence"
    for path in [private, config, evidence, root / "data", root / "backups"]:
        path.mkdir(parents=True, exist_ok=True)
    private.chmod(0o700)

    def save(name, value):
        path = private / name
        if path.exists():
            return json.loads(path.read_text())
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            json.dump(value, output)
        return value

    def apply(obj):
        return run(["kubectl", "apply", "-f", "-"], input=json.dumps(obj))

    def sql(statement, database="postgres"):
        return run(["docker", "exec", "-i", "ops-current-pg", "psql", "-X", "-U", "postgres",
                    "-d", database, "-v", "ON_ERROR_STOP=1"], input=statement)

    if args.phase == "dependencies":
        if not args.profile_template:
            parser.error("dependencies requires --profile-template (public resolved profile)")
        apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": ns,
               "labels": {"ops.platform.io/environment": "current"}}})
        profile = yaml.safe_load(args.profile_template.read_text())
        for name, component in profile["components"].items():
            if name not in ["openbao", "postgresql", "seaweedfs"]:
                component["mode"] = "disabled"
            elif name == "postgresql":
                component.update(namespace=ns, endpoint="postgresql://127.0.0.1:15554")
            else:
                service, port = ("ops-openbao", 8200) if name == "openbao" else ("ops-seaweedfs-s3", 8333)
                component.update(namespace=ns, endpoint=f"https://{service}.{ns}.svc.cluster.local:{port}")
            component.pop("evidence", None)
        profile["kubernetes"]["storageClass"] = ""
        (config / "profile.yaml").write_text(yaml.safe_dump(profile, allow_unicode=True))
        values = {"components": {"openbao": {"image": profile["components"]["openbao"]["image"]}}}
        (config / "bao-values.json").write_text(json.dumps(values))
        docs = list(yaml.safe_load_all(run(["helm", "template", "ops-current", str(release / "deploy/charts/ops-dependencies"),
                    "-n", ns, "-f", str(config / "bao-values.json"), "-s", "templates/openbao.yaml"])))
        data = root / "data/openbao"
        data.mkdir(exist_ok=True); os.chown(data, 100, 1000); data.chmod(0o770)
        for doc in docs:
            if not doc:
                continue
            if doc["kind"] != "ClusterRoleBinding":
                doc["metadata"]["namespace"] = ns
            if doc["kind"] == "StatefulSet":
                doc["spec"].pop("volumeClaimTemplates")
                spec = doc["spec"]["template"]["spec"]
                spec["automountServiceAccountToken"] = False
                spec["volumes"].extend([
                    {"name": "data", "hostPath": {"path": str(data), "type": "Directory"}},
                    {"name": "reviewer", "projected": {"defaultMode": 0o444, "sources": [
                        {"serviceAccountToken": {"path": "token", "expirationSeconds": 600}},
                        {"configMap": {"name": "kube-root-ca.crt", "items": [{"key": "ca.crt", "path": "ca.crt"}]}},
                        {"downwardAPI": {"items": [{"path": "namespace", "fieldRef": {"fieldPath": "metadata.namespace"}}]}}
                    ]}}])
                spec["containers"][0]["volumeMounts"].append({"name": "reviewer", "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount", "readOnly": True})
            apply(doc)
        password = private / "pg-password"
        if not password.exists():
            password.write_text(secrets.token_hex(32)); password.chmod(0o600)
        if not run(["docker", "ps", "-aq", "--filter", "name=^/ops-current-pg$"]).strip():
            data = root / "data/postgresql"; data.mkdir(exist_ok=True)
            image = json.loads(run(["docker", "image", "inspect", "ops.local/amd64-poc/postgresql-bookworm:17.11"]))[0]["Id"]
            run(["docker", "run", "-d", "--name", "ops-current-pg", "--pull=never", "--restart=unless-stopped",
                 "--label", "ops.platform.io/environment=current", "-p", "127.0.0.1:15554:5432",
                 "-v", str(data) + ":/var/lib/postgresql/data", "-v", str(password) + ":/run/secrets/pg-password:ro",
                 "-e", "POSTGRES_PASSWORD_FILE=/run/secrets/pg-password", image])
        for _ in range(60):
            try:
                sql("SELECT 1;"); break
            except RuntimeError:
                time.sleep(1)
        else:
            raise RuntimeError("current PostgreSQL did not become ready")
        print(json.dumps({"namespace": ns, "databaseContainer": "ops-current-pg", "port": 15554}))
        return

    marker = evidence / "first-tenant.json"
    if marker.exists():
        print("database bootstrap already complete; no database changes")
        return
    database = "ops_current"
    exists = sql(f"SELECT 1 FROM pg_database WHERE datname='{database}';")
    if "(0 rows)" not in exists:
        raise RuntimeError("database exists without completed bootstrap marker; inspect before resume")
    sql(f"CREATE DATABASE {database} OWNER postgres;")
    owner = (private / "pg-password").read_text()
    env = dict(os.environ, SP03_MIGRATION_DATABASE_URL=f"postgres://postgres:{quote(owner, safe='')}@127.0.0.1:15554/{database}?sslmode=disable")
    migration = [str(release / "bin/db-migrate"), "-dir", str(release / "migrations")]
    (evidence / "migration-bootstrap.log").write_text(run(migration + ["-to", "1"], env=env))
    sql(f"GRANT CREATE ON DATABASE {database} TO schema_owner;")
    (evidence / "migration.log").write_text(run(migration, env=env))
    urls = {}
    for duty, role in [("api", "api_runtime_role"), ("worker", "worker_runtime_role"), ("bootstrap", "migration_role"), ("cli", "ops_readonly_role")]:
        name, password = "current_" + duty, secrets.token_hex(32)
        sql(f"CREATE ROLE {name} LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{password}'; GRANT {role} TO {name}; GRANT CONNECT ON DATABASE {database} TO {name};")
        urls[duty] = f"postgres://{name}:{password}@127.0.0.1:15554/{database}?sslmode=disable"
    save("database-urls.json", urls)
    password = secrets.token_urlsafe(30)
    (private / "initial-password").write_text(password); (private / "initial-password").chmod(0o600)
    save("bootstrap.json", {"databaseUrl": urls["bootstrap"], "tenantSlug": "ops-current", "tenantDisplayName": "OPS Current",
         "adminLoginName": "ops.admin", "adminDisplayName": "Current 管理员"})
    receipt = json.loads(run([str(release / "bin/opsctl"), "bootstrap", "first-tenant-native", "--input", str(private / "bootstrap.json"),
                         "--password-file", str(private / "initial-password")], cwd=release))
    marker.write_text(json.dumps(receipt, indent=2) + "\n")
    save("browser-account.json", {"loginName": receipt["loginName"], "password": password, "userId": receipt["userId"]})
    print(json.dumps({"namespace": ns, "database": database, "tenantId": receipt["tenantId"]}))


if __name__ == "__main__":
    main()
