"""Exercise the pinned, already-built CLI against a local synthetic API only.

No live cluster credentials are used. This is Analyzer JSON fixture evidence,
not an offline binary/dependency qualification or a platform adapter test.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    args = parser.parse_args()
    interfaces = {name for _, name in socket.if_nameindex()}
    network_isolated = interfaces == {"lo"}
    if not network_isolated:
        parser.error(f"fixture requires Docker network none; interfaces are {sorted(interfaces)}")
    # Upstream runs legacy-config migration during package initialization,
    # even with --config. Refuse any possibility of moving a user's file.
    if (Path.home() / ".k8sgpt.yaml").exists():
        parser.error("legacy K8sGPT config exists; run the fixture in an isolated container")
    binary = str(Path(args.binary).resolve(strict=True))
    pods = json.loads((Path(__file__).parent / "k8sgpt-analyzer/pod-unschedulable.json").read_text())
    requests, unexpected = [], []

    class API(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            path = self.path.split("?", 1)[0]
            requests.append(path)
            if path == "/version":
                result = {"major": "1", "minor": "35", "gitVersion": "v1.35.6", "platform": "linux/arm64"}
            elif path == "/api/v1/namespaces/fixture/pods":
                result = pods
            else:
                unexpected.append(path)
                self.send_error(404)
                return
            body = json.dumps(result).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    with http.server.ThreadingHTTPServer(("127.0.0.1", 0), API) as server, tempfile.TemporaryDirectory(prefix="ops-k8sgpt-fixture-") as temporary:
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        work = Path(temporary)
        config = work / "config.yaml"
        config.write_text("{}\n")
        kubeconfig = work / "kubeconfig.json"
        kubeconfig.write_text(json.dumps({
            "apiVersion": "v1", "kind": "Config", "current-context": "fixture",
            "clusters": [{"name": "fixture", "cluster": {"server": f"http://127.0.0.1:{server.server_port}"}}],
            "contexts": [{"name": "fixture", "context": {"cluster": "fixture", "user": "fixture"}}],
            "users": [{"name": "fixture", "user": {}}],
        }))
        # Keep HOME intact for upstream's safe, checked migration precondition.
        # Remove provider/proxy/in-cluster overrides and isolate XDG writes.
        environment = {key: os.environ[key] for key in ("PATH", "HOME", "TMPDIR") if key in os.environ}
        environment.update(XDG_CONFIG_HOME=str(work / "config"), XDG_CACHE_HOME=str(work / "cache"), XDG_DATA_HOME=str(work / "data"))
        command = [binary, "analyze", "--kubeconfig", str(kubeconfig), "--kubecontext", "fixture", "--config", str(config), "--namespace", "fixture", "--filter", "Pod", "--output", "json", "--no-cache", "--max-concurrency", "1"]
        try:
            outputs = []
            for _ in range(2):
                completed = subprocess.run(command, env=environment, cwd=work, capture_output=True, text=True, timeout=30, check=True)
                result = json.loads(completed.stdout)
                assert result["status"] == "ProblemDetected" and result["problems"] == 1, result
                assert result["provider"] == "" and not result["errors"], result
                assert len(result["results"]) == 1, result
                finding = result["results"][0]
                assert finding["kind"] == "Pod" and finding["name"] == "fixture/unschedulable", finding
                assert finding["error"][0]["Text"] == "fixture: insufficient memory", finding
                outputs.append(result)
            assert outputs[0] == outputs[1], "same fixture produced different JSON"
            assert not unexpected, unexpected
            print(json.dumps({"status": "fixture-pass", "runs": 2, "llmRequested": False, "liveCluster": False, "networkIsolationVerified": network_isolated, "interfaces": sorted(interfaces), "requests": requests, "result": outputs[0]}, indent=2))
        finally:
            server.shutdown()
            thread.join(timeout=5)


if __name__ == "__main__":
    main()
