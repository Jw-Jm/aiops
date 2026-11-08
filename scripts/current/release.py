#!/usr/bin/env python3
"""Inspect, back up and record the single current development environment.

Run on the deployment host. Recording a release does not deploy it or migrate
its database: those steps must be completed and accepted beforehand.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import ssl
import subprocess
import urllib.request
from urllib.parse import urlsplit


def command(args):
    return subprocess.check_output(args, stderr=subprocess.PIPE)


def kube(namespace, resource):
    return json.loads(command(["kubectl", "get", resource, "-n", namespace, "-o", "json"]))


def schema():
    return int(command(["docker", "exec", "ops-current-pg", "psql", "-X", "-U", "postgres", "-d", "ops_current", "-At", "-c",
                        "SELECT max(version_id) FROM goose_db_version WHERE is_applied;"]).strip())


def private_write(path, content):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as output:
        output.write(content)


def verify_runtime_images(images):
    """Never deploy a digest that only exists under an unrelated local tag."""
    targets = {}
    for line in command(["ctr", "-n", "k8s.io", "images", "ls"]).decode().splitlines()[1:]:
        fields = line.split()
        if len(fields) >= 3:
            targets[fields[0]] = fields[2]
    ready = set(command(["ctr", "-n", "k8s.io", "images", "check", "--quiet"]).decode().splitlines())
    verified = []
    for component in ["api", "worker", "investigator"]:
        reference = images.get(component, {}).get("image", "")
        if not re.fullmatch(r"[^\s@]+@sha256:[0-9a-f]{64}", reference):
            raise ValueError(component + " requires a pinned image digest")
        if targets.get(reference) != reference.split("@", 1)[1]:
            raise ValueError(component + " pinned image is not registered with its exact digest in containerd k8s.io")
        if reference not in ready:
            raise ValueError(component + " pinned image content is incomplete or not unpacked")
        verified.append(reference)
    return verified


def web_runtime_config(environment):
    mode = environment.get("humanAuthMode")
    if mode not in ("native", "oidc"):
        raise ValueError("explicit environment humanAuthMode required")
    config = {"auth": {"mode": mode}}
    if mode == "oidc":
        oidc = environment.get("oidc", {})
        if not isinstance(oidc, dict) or set(oidc) - {"authority", "clientId", "proxyPath"}:
            raise ValueError("only public OIDC client configuration may enter the Web release")
        authority = urlsplit(str(oidc.get("authority", "")))
        if authority.scheme != "https" or not authority.hostname or authority.username or authority.password or authority.query or authority.fragment or not oidc.get("clientId"):
            raise ValueError("explicit HTTPS OIDC public configuration required")
        config["oidc"] = oidc
    return config


def verify_web_runtime(release, environment):
    path = release / "web/runtime-config.json"
    if not path.is_file() or path.is_symlink():
        raise ValueError("prepared Web runtime-config.json required; run prepare-web before deployment")
    raw = path.read_bytes()
    if json.loads(raw) != web_runtime_config(environment):
        raise ValueError("Web authentication configuration differs from the environment")
    return hashlib.sha256(raw).hexdigest()


def prepare_web_runtime(release, environment):
    path = release / "web/runtime-config.json"
    if not path.exists():
        if (release / "manifest.json").exists():
            raise ValueError("accepted release is immutable; prepare another candidate")
        if not (release / "web/index.html").is_file():
            raise ValueError("built Web assets required before prepare-web")
        with path.open("x") as output:
            output.write(json.dumps(web_runtime_config(environment), indent=2) + "\n")
    return verify_web_runtime(release, environment)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=["status", "backup", "prepare-web", "preflight", "record"])
    parser.add_argument("--root", type=Path, default=Path("/data/ops-current"))
    parser.add_argument("--namespace", default="ops-current")
    parser.add_argument("--release")
    args = parser.parse_args()
    root, ns = args.root.resolve(), args.namespace
    if not (root / "private/database-urls.json").is_file():
        parser.error("existing independent current environment required")
    namespace = json.loads(command(["kubectl", "get", "namespace", ns, "-o", "json"]))
    if namespace["metadata"].get("labels", {}).get("ops.platform.io/managed-by") != "opsctl-bootstrap":
        parser.error("namespace must have been created by the formal bootstrap entry")
    expected = json.loads((root / "evidence/environment-bootstrap.json").read_text())
    if expected["namespace"] != ns or expected["namespaceUid"] != namespace["metadata"]["uid"]:
        parser.error("namespace identity differs from this current environment")
    if args.operation in ("prepare-web", "preflight", "record"):
        if not args.release or not re.fullmatch(r"[0-9a-f]{40}", args.release):
            parser.error(args.operation + " requires a full 40-character Git commit")
        release = root / "releases" / args.release
        if not release.is_dir() or release.is_symlink():
            parser.error("prepared release directory required")
        environment = json.loads((root / "config/environment.json").read_text())
        if args.operation == "prepare-web":
            try:
                digest = prepare_web_runtime(release, environment)
            except ValueError as error:
                parser.error(str(error))
            print(json.dumps({"release": args.release, "webRuntimeConfigSHA256": digest}))
            return
        images = json.loads((release / "images.json").read_text())
        try:
            verified = verify_runtime_images(images)
            web_runtime_digest = verify_web_runtime(release, environment)
        except ValueError as error:
            parser.error(str(error))
        if args.operation == "preflight":
            print(json.dumps({"release": args.release, "namespace": ns, "readyImages": verified}))
            return
    version = schema()  # A real authenticated query, not container/Pod readiness.
    now = datetime.datetime.now(datetime.timezone.utc)
    if args.operation == "status":
        pointer = root / "current"
        print(json.dumps({"namespace": ns, "namespaceUID": namespace["metadata"]["uid"], "database": "ops_current",
                          "schema": version, "activeRelease": pointer.resolve().name if pointer.is_symlink() else None}))
        return
    if args.operation == "backup":
        backup = root / "backups" / now.strftime("%Y%m%dT%H%M%SZ")
        backup.mkdir(mode=0o700, parents=True, exist_ok=False)
        # Credentials and MFA recovery material never enter a release or Git.
        shutil.copytree(root / "private", backup / "private", symlinks=False)
        private_write(backup / "postgres.dump", command(["docker", "exec", "ops-current-pg", "pg_dump", "-Fc", "-U", "postgres", "ops_current"]))
        private_write(backup / "postgres-globals.sql", command(["docker", "exec", "ops-current-pg", "pg_dumpall", "-U", "postgres", "--globals-only"]))
        resources = command(["kubectl", "get", "deploy,sts,ds,svc,cm,secret,sa,role,rolebinding", "-n", ns, "-o", "json"])
        private_write(backup / "namespace-private.json", resources)
        # Persist the new Bao state as well as the encrypted database.
        host = "ops-openbao." + ns + ".svc.cluster.local"
        address = command(["kubectl", "get", "svc", "ops-openbao", "-n", ns, "-o", "jsonpath={.spec.clusterIP}"]).decode()
        lookup = socket.getaddrinfo
        socket.getaddrinfo = lambda name, *a, **kw: lookup(address if name == host else name, *a, **kw)
        try:
            recovery = json.loads((root / "private/bao-recovery.json").read_text())
            request = urllib.request.Request("https://" + host + ":8200/v1/sys/storage/raft/snapshot", headers={"X-Vault-Token": recovery["rootToken"]})
            with urllib.request.urlopen(request, context=ssl.create_default_context(cafile=str(root / "private/bao-ca.pem")), timeout=30) as response:
                private_write(backup / "openbao-raft.snap", response.read())
        finally:
            socket.getaddrinfo = lookup
        manifest = {"at": now.isoformat(), "namespaceUID": namespace["metadata"]["uid"], "schema": version,
                    "files": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in backup.iterdir() if p.is_file()},
                    "archiveData": "retained independently under data/archive; payload copy is not part of this backup"}
        private_write(backup / "backup.json", (json.dumps(manifest, indent=2) + "\n").encode())
        print(json.dumps({"backup": str(backup), "schema": version, "private": True}))
        return
    accepted = json.loads((root / "evidence/acceptance.json").read_text())
    if accepted.get("status") != "passed_core" or accepted.get("commit") != args.release or accepted.get("namespaceUID") != namespace["metadata"]["uid"]:
        parser.error("matching current core acceptance receipt required")
    deployments = kube(ns, "deployments")
    required = {"ops-api": "api", "ops-worker": "worker", "ops-investigator": "investigator"}
    selected = []
    for deployment in deployments["items"]:
        name = deployment["metadata"]["name"]
        if name not in required:
            continue
        spec = deployment["spec"]["template"]["spec"]
        if deployment["status"].get("availableReplicas", 0) < 1:
            parser.error(name + " is not available")
        if spec["containers"][0]["image"] != images[required[name]]["image"]:
            parser.error(name + " does not match the pinned release image")
        for volume in spec.get("volumes", []):
            host = volume.get("hostPath", {}).get("path")
            if host and not Path(host).is_relative_to(root):
                parser.error(name + " still depends on an external hostPath")
        selected.append({"apiVersion": deployment["apiVersion"], "kind": deployment["kind"],
                         "metadata": {key: deployment["metadata"][key] for key in ["name", "namespace", "labels", "annotations"] if key in deployment["metadata"]},
                         "spec": deployment["spec"]})
    if len(selected) != len(required):
        parser.error("complete current application deployments required")
    web = json.loads(command(["docker", "inspect", "ops-current-web"]))[0]
    # Compare resolved paths: the mount source may use any prefix of the same
    # backing directory (for example /data before a storage symlink).
    if not web["State"]["Running"] or not any(m["Destination"] == "/preview/web" and Path(m["Source"]).resolve() == (release / "web").resolve() for m in web["Mounts"]):
        parser.error("current Web does not serve the selected immutable release")
    cm = kube(ns, "configmap/ops-runtime")
    config_bytes = json.dumps(cm["data"], sort_keys=True, separators=(",", ":")).encode()
    manifest = {"schemaVersion": "ops-current-release/v1", "commit": args.release, "namespace": ns,
                "namespaceUID": namespace["metadata"]["uid"], "database": "ops_current", "databaseSchema": version,
                "images": images, "runtimeConfigSHA256": hashlib.sha256(config_bytes).hexdigest(),
                "webIndexSHA256": hashlib.sha256((release / "web/index.html").read_bytes()).hexdigest(),
                "webRuntimeConfigSHA256": web_runtime_digest,
                "acceptedAt": accepted["at"], "knownLimitations": accepted.get("knownLimitations", [])}
    target = release / "manifest.json"
    if target.exists() and json.loads(target.read_text()) != manifest:
        parser.error("release manifest is immutable; use another commit for a new release")
    if not target.exists():
        target.write_text(json.dumps(manifest, indent=2) + "\n")
        (release / "runtime-config.json").write_bytes(config_bytes)
        (release / "deployments.json").write_text(json.dumps({"apiVersion": "v1", "kind": "List", "items": selected}, indent=2) + "\n")
    pointer = root / ".current-next"
    if pointer.is_symlink():
        pointer.unlink()
    elif pointer.exists():
        parser.error("unexpected staging pointer; inspect before recording")
    pointer.symlink_to(Path("releases") / args.release, target_is_directory=True)
    os.replace(pointer, root / "current")
    print(json.dumps({"current": args.release, "namespace": ns, "schema": version}))


if __name__ == "__main__":
    main()
