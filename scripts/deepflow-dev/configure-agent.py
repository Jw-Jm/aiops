#!/usr/bin/env python3
"""Synchronize the isolated development Agent Group with checked-in defaults."""
import argparse
import copy
import ipaddress
import json
from pathlib import Path
import subprocess
import urllib.parse
import urllib.request

NAMESPACE = "ops-assistant-deepflow-20261013"
RELEASE = "assistant-network-20261013"


def kube(kind, name):
    value = json.loads(subprocess.check_output(
        ["kubectl", "-n", NAMESPACE, "get", kind, name, "-o", "json"], text=True))
    if value["metadata"].get("labels", {}).get("ops.platform.io/test-release") != RELEASE:
        raise ValueError("resource is outside the owned development release")
    return value


def merge(target, changes):
    for key, value in changes.items():
        if isinstance(value, dict):
            if not isinstance(target.get(key), dict):
                target[key] = {}
            merge(target[key], value)
        else:
            target[key] = value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--controller", default="http://127.0.0.1:21417")
    parser.add_argument("--defaults", type=Path, default=Path(__file__).with_name("agent-defaults.json"))
    args = parser.parse_args()
    endpoint = urllib.parse.urlsplit(args.controller)
    if endpoint.scheme != "http" or endpoint.hostname != "127.0.0.1" or endpoint.path not in ("", "/") or endpoint.query or endpoint.fragment or endpoint.username:
        raise ValueError("use the owned Controller's loopback port-forward")
    service = kube("service", "ops-assistant-deepflow-server")
    kube("daemonset", "deepflow-agent")
    address = str(ipaddress.ip_address(service["spec"]["clusterIP"]))
    defaults = json.loads(args.defaults.read_text())
    limits = defaults["global"]["limits"]
    if set(limits) != {"max_memory", "max_millicpus"} or any(type(v) is not int or v <= 0 for v in limits.values()):
        raise ValueError("invalid Agent Group limits")
    wanted = copy.deepcopy(defaults)
    wanted["global"]["communication"] = {
        "proxy_controller_ip": address, "proxy_controller_port": 30035,
        "ingester_ip": address, "ingester_port": 30033,
    }
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def request(path, method="GET", body=None):
        req = urllib.request.Request(args.controller.rstrip("/") + path,
            data=None if body is None else json.dumps(body).encode(), method=method,
            headers={"Content-Type": "application/json", "X-Org-Id": "1", "X-User-Type": "1", "X-User-Id": "1"})
        with opener.open(req, timeout=15) as response:
            result = json.loads(response.read(4 * 1024 * 1024))
        if result.get("OPT_STATUS") != "SUCCESS":
            raise ValueError("Controller rejected Agent Group configuration")
        return result.get("DATA")

    groups = [group for group in request("/v1/vtap-groups/") if group["NAME"] == "default"]
    if len(groups) != 1:
        raise ValueError("expected exactly one owned default Agent Group")
    group = groups[0]["LCUUID"]
    if any(c not in "0123456789abcdef-" for c in group):
        raise ValueError("invalid group identifier")
    path = f"/v1/agent-group-configuration/{group}/json"
    existing = request(path) or {}
    merged = copy.deepcopy(existing)
    merge(merged, wanted)
    if merged != existing:
        request(path, "PUT", merged)
    actual = request(path)
    for key, value in wanted["global"].items():
        if any(actual["global"][key].get(k) != v for k, v in value.items()):
            raise ValueError("Agent Group read-back differs from defaults")
    print(json.dumps({"namespace": NAMESPACE, "group": group,
                      "changed": merged != existing, "configured": wanted}, indent=2))


if __name__ == "__main__":
    main()
