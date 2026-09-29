"""Exercise the real core Chart's policy in a disposable OrbStack namespace.

Requires the separately prepared policy controller and cached pinned probe
image. This checks network policy enforcement and release isolation; it is not
the signed-Bundle installation/reinstallation acceptance test.
"""
import hashlib
import json
from pathlib import Path
import secrets
import subprocess
import time


PROBE_IMAGE = "docker.io/library/postgres@sha256:75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257"
CONTROLLER_IMAGE = "docker.io/cloudnativelabs/kube-router@sha256:fec5ac13d36a812636d545263fda75e5b729ac9dac624f1f19f1170d3372324b"


def command(*args, input=None, check=True):
    return subprocess.run(args, input=input, capture_output=True, text=True,
                          check=check, timeout=120)


def kube(*args, **kwargs):
    return command("kubectl", "--context", "orbstack", *args, **kwargs)


def main():
    controller = json.loads(kube("-n", "ops-dev-network", "get", "daemonset", "ops-dev-network", "-o", "json").stdout)
    if controller["spec"]["template"]["spec"]["containers"][0]["image"] != CONTROLLER_IMAGE:
        raise RuntimeError("policy controller differs from the reviewed pin")
    if controller["status"].get("numberReady", 0) != controller["status"].get("desiredNumberScheduled", 0) or not controller["status"].get("numberReady"):
        raise RuntimeError("policy controller is not ready")
    owner = "ops-task27-network-" + secrets.token_hex(4)
    namespace = {"apiVersion": "v1", "kind": "Namespace", "metadata": {
        "name": owner, "labels": {"ops.platform.io/poc-owner": owner}}}
    kube("create", "-f", "-", input=json.dumps(namespace))
    uid = json.loads(kube("get", "namespace", owner, "-o", "json").stdout)["metadata"]["uid"]
    report = {"scope": "isolated core Chart NetworkPolicy PoC",
              "controllerImage": CONTROLLER_IMAGE, "probeImage": PROBE_IMAGE,
              "coreOfflineInstallationPassed": False}
    try:
        def pod(name, component, release):
            resource = {"apiVersion": "v1", "kind": "Pod", "metadata": {
                "name": name, "namespace": owner, "labels": {
                    "ops.platform.io/poc-owner": owner,
                    "ops.platform.io/component": component,
                    "ops.platform.io/release": release}}, "spec": {
                "restartPolicy": "Never", "automountServiceAccountToken": False,
                "securityContext": {"runAsNonRoot": True, "runAsUser": 999},
                "containers": [{"name": "probe", "image": PROBE_IMAGE,
                    "imagePullPolicy": "Never", "command": ["sleep", "600"],
                    "securityContext": {"readOnlyRootFilesystem": True,
                        "allowPrivilegeEscalation": False,
                        "capabilities": {"drop": ["ALL"]}}}]}}
            kube("create", "-f", "-", input=json.dumps(resource))
            kube("-n", owner, "wait", "--for=condition=Ready", "pod/" + name, "--timeout=90s")

        pod("platform", "api", owner)
        pod("dependency", "postgresql", owner + "-deps")
        pod("external", "openbao", "ops-core")

        def connect(name, host, port):
            script = "if timeout 4 bash -c '</dev/tcp/" + host + "/" + str(port) + "'; then echo REACHABLE; else echo BLOCKED; exit 7; fi"
            result = kube("-n", owner, "exec", name, "--", "bash", "-ec", script, check=False)
            if result.returncode == 0 and result.stdout.strip() == "REACHABLE":
                return True
            if result.returncode == 7 and result.stdout.strip() == "BLOCKED":
                return False
            raise RuntimeError("network probe could not execute: " + result.stderr)

        for name in ("platform", "dependency", "external"):
            if not connect(name, "1.1.1.1", 443):
                raise RuntimeError("public positive control failed before policy for " + name)
        report["publicBeforePolicy"] = True
        root = Path(__file__).resolve().parents[3]
        victoria = json.loads(kube("-n", "monitoring", "get", "service", "vmsingle-vm", "-o", "json").stdout)
        service_port = next(port for port in victoria["spec"]["ports"] if port["port"] == 8429 and port.get("protocol", "TCP") == "TCP")
        external_services = [{"namespace": "monitoring", "podLabels": victoria["spec"]["selector"], "port": service_port["targetPort"]}]
        if not external_services[0]["podLabels"]:
            raise RuntimeError("Victoria Service selector is empty")
        rendered = command("helm", "template", owner, str(root / "deploy/charts/ops-platform"),
            "--namespace", owner, "--show-only", "templates/network-policies.yaml",
            "--set-string", "networkPolicy.managedDependencyReleases[0]=" + owner + "-deps",
            "--set-json", "networkPolicy.externalServices=" + json.dumps(external_services)).stdout
        report["renderedPolicySHA256"] = hashlib.sha256(rendered.encode()).hexdigest()
        kube("-n", owner, "apply", "-f", "-", input=rendered)
        dns = json.loads(kube("-n", "kube-system", "get", "service", "kube-dns", "-o", "json").stdout)["spec"]["clusterIP"]
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if not connect("platform", "1.1.1.1", 443) and not connect("dependency", "1.1.1.1", 443):
                break
            time.sleep(2)
        else:
            raise RuntimeError("core policies did not reject managed Pod public egress")
        for name in ("platform", "dependency"):
            if not connect(name, dns, 53):
                raise RuntimeError("internal DNS positive control failed for " + name)
            if not connect(name, victoria["spec"]["clusterIP"], 8429):
                raise RuntimeError("registered external Victoria Service was blocked for " + name)
        if not connect("external", "1.1.1.1", 443):
            raise RuntimeError("core policies interfered with the protected external release")
        report.update({"managedPlatformPublicBlocked": True,
                       "managedDependencyPublicBlocked": True,
                       "managedInternalDNSReachable": True,
                       "registeredVictoriaServiceReachable": True,
                       "externalReleaseUnaffected": True})
        print(json.dumps(report, indent=2))
    finally:
        live = json.loads(kube("get", "namespace", owner, "-o", "json").stdout)
        if live["metadata"]["uid"] != uid or live["metadata"]["labels"].get("ops.platform.io/poc-owner") != owner:
            raise RuntimeError("namespace cleanup ownership mismatch")
        kube("delete", "namespace", owner, "--wait=true", "--timeout=90s")


if __name__ == "__main__":
    main()
