"""Replay the eight frozen non-virtual upstream PoCs without download paths."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

SOURCES = {
    "k8sgpt-analyzer": "k8sgpt",
    "node-problem-detector-rules": "node-problem-detector",
    "coroot-community-check": "coroot",
    "keep-community-model": "keep",
    "metal3-bmo-model": "baremetal-operator",
    "gofish-redfish": "gofish",
    "ipmi-exporter": "ipmi_exporter",
    "smartctl-exporter": "smartctl_exporter",
}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def checked_file(root, relative, sha):
    path = root / relative
    if Path(relative).is_absolute() or ".." in Path(relative).parts:
        raise ValueError(f"non-local locked file: {relative}")
    if digest(path) != sha:
        raise ValueError(f"locked file digest mismatch: {relative}")
    return path


def command(argv, log, **kwargs):
    log.write("command: " + json.dumps([str(a) for a in argv]) + "\n")
    log.flush()
    result = subprocess.run(argv, stdout=log, stderr=subprocess.STDOUT, timeout=600, **kwargs)
    log.write(f"exit_code={result.returncode}\n")
    log.flush()
    if result.returncode:
        raise RuntimeError(f"command failed with exit {result.returncode}; see {log.name}")


def container_keep():
    requirements, selected = sys.argv[2:4]
    subprocess.run([
        sys.executable, "-m", "pip", "install", "--no-index", "--find-links=/wheels",
        "--require-hashes", "--no-deps", "--target=/tmp/site", "-r", requirements,
    ], check=True)
    env = {**os.environ, "PYTHONPATH": f"/tmp/site:{selected}", "PYTHONDONTWRITEBYTECODE": "1"}
    subprocess.run([sys.executable, "/fixtures/verify-keep-community-model.py", "--source-root", selected], check=True, env=env)
    subprocess.run([sys.executable, "/fixtures/keep-community-model/keep-model-poc.py.fixture"], check=True, env=env)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--only", choices=tuple(SOURCES))
    parser.add_argument("--sources", type=Path, required=True)
    parser.add_argument("--module-cache", type=Path)
    parser.add_argument("--keep-wheels", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--check-inputs", action="store_true")
    args = parser.parse_args()
    if sys.version_info[:3] != (3, 12, 14):
        raise RuntimeError(f"expected Python 3.12.14, got {sys.version.split()[0]}")
    root = Path(__file__).resolve().parents[3]
    fixtures = root / "test/fixtures/upstream-inspection"
    lock = json.loads((root / "docs/poc/inspection-reuse-lock.yaml").read_text())
    closure = json.loads((fixtures / "dependency-closures.json").read_text())
    records = {r["id"]: r for r in closure["generatedFrom"]["records"]}
    capabilities = {c["id"]: c for c in lock["capabilities"]}
    selected_ids = [args.only] if args.only else list(SOURCES)
    for key in selected_ids:
        if not (args.sources / SOURCES[key] / ".git").is_dir():
            raise RuntimeError(f"frozen source repository unavailable: {args.sources / SOURCES[key]}")
        cap = capabilities[key]
        for f in cap["selectedFiles"] + cap.get("conformanceFiles", []):
            checked_file(root, f["path"], f["sha256"])
        checked_file(root, cap["licenseEvidence"], cap["licenseEvidenceSHA256"])
        evidence = cap["dependencyClosureEvidence"]
        checked_file(root, evidence["path"], evidence["sha256"])
    if "keep-community-model" in selected_ids:
        if args.keep_wheels is None:
            raise RuntimeError("--keep-wheels is required for the locked Keep import closure")
        for m in records["keep-community-model"]["modules"]:
            checked_file(args.keep_wheels, m["artifact"], m["artifactSHA256"])
    if args.check_inputs:
        print("frozen nonvirtual source, fixture, license and wheel inputs checked; no containers executed")
        return
    if args.module_cache is None:
        args.module_cache = Path(subprocess.check_output(["go", "env", "GOMODCACHE"], text=True).strip())
    args.module_cache = args.module_cache.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=True)
    if list(args.output.iterdir()):
        raise RuntimeError("output directory must be empty to preserve earlier replay evidence")
    summary = {"scope": "SP-02 Task 2.9 nonvirtual PoC only", "network": "none", "pull": "never", "results": []}
    with tempfile.TemporaryDirectory(prefix="ops-inspection-replay-") as directory:
        work = Path(directory)
        buildcache = work / "cache"
        buildcache.mkdir()
        for key in selected_ids:
            cap = capabilities[key]
            log_path = args.output / (key + ".log")
            with log_path.open("w") as log:
                try:
                    log.write(f"capability={key} source_commit={cap['commit']} runtime_enabled=false\n")
                    log.write(f"locked_state={cap['state']} disabled_reason={cap.get('disabledReason', '')}\n")
                    archive = work / (key + ".tar")
                    command(["git", "-C", args.sources / SOURCES[key], "archive", "--format=tar", f"--output={archive}", cap["commit"]], log)
                    if digest(archive) != cap["sourceArchiveSHA256"]:
                        raise RuntimeError(f"source archive digest mismatch: {key}")
                    source = work / key
                    source.mkdir()
                    with tarfile.open(archive) as tar:
                        tar.extractall(source, filter="data")
                    for f in cap["selectedFiles"]:
                        checked_file(source, f["sourcePath"], f["sha256"])
                    images = {i["name"]: f"{i['name']}:{i['version']}@{i['digest']}" for i in cap["offlineReplay"]["containerImages"]}
                    output = source / "replay-output"
                    output.mkdir()

                    def docker(image, argv, cwd=f"/work/{key}", extra=()):
                        command([
                            "docker", "--context", "orbstack", "run", "--rm", "--network", "none", "--pull=never",
                            "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777",
                            "--mount", f"type=bind,src={work},dst=/work,readonly",
                            "--mount", f"type=bind,src={fixtures},dst=/fixtures,readonly",
                            "--mount", f"type=bind,src={args.module_cache},dst=/gomodcache,readonly",
                            "--mount", f"type=bind,src={buildcache},dst=/buildcache",
                            "--mount", f"type=bind,src={output},dst=/output",
                            "-e", "GOPROXY=off", "-e", "GOSUMDB=off", "-e", "GOMODCACHE=/gomodcache", "-e", "GOCACHE=/buildcache",
                            "-e", "CGO_ENABLED=0", "-e", "PYTHONDONTWRITEBYTECODE=1", "-e", "OPS_TASK29_FIXTURES=/fixtures",
                            "-w", cwd, *extra, image, *argv,
                        ], log)

                    def go_test(packages, pattern, cwd=f"/work/{key}"):
                        docker(images["golang"], ["go", "test", "-mod=readonly", "-p", "1", "-count=1", "-v", "-timeout=5m", *packages, "-run", pattern], cwd)

                    if key == "k8sgpt-analyzer":
                        go_test(["./pkg/analyzer"], "^TestPod")
                        docker(images["golang"], ["go", "build", "-mod=readonly", "-p", "1", "-o", "/output/k8sgpt", "./"])
                        docker(images["python"], ["python", "/fixtures/verify-k8sgpt-cli.py", "--binary", f"/work/{key}/replay-output/k8sgpt"])
                    elif key == "node-problem-detector-rules":
                        # Replay the selected matcher, not the unselected NPD
                        # daemon/watchers and their additional dependency roots.
                        go_test(["./pkg/systemlogmonitor/log_buffer.go", "./pkg/systemlogmonitor/log_buffer_test.go"], "^Test(Push|Match)$")
                        image = images["victoriametrics/victoria-logs"]
                        entrypoint = json.loads(subprocess.check_output(["docker", "--context", "orbstack", "image", "inspect", "--format={{json .Config.Entrypoint}}", image], text=True))
                        if len(entrypoint) != 1 or not entrypoint[0].startswith("/"):
                            raise RuntimeError("VictoriaLogs image entrypoint is not an absolute binary")
                        cid = subprocess.check_output(["docker", "--context", "orbstack", "create", "--network", "none", "--pull=never", image], text=True).strip()
                        try:
                            command(["docker", "--context", "orbstack", "cp", f"{cid}:{entrypoint[0]}", source / "victoria-logs"], log)
                        finally:
                            command(["docker", "--context", "orbstack", "rm", cid], log)
                        docker(images["golang"], ["go", "run", "/fixtures/verify-npd-victorialogs.go", "-victoria-logs", f"/work/{key}/victoria-logs", "-rules", "/fixtures/node-problem-detector-rules/config/kernel-monitor.json", "-cases", "/fixtures/npd-victorialogs-cases.json"])
                    elif key == "coroot-community-check":
                        shutil.copyfile(fixtures / "coroot-community-check/model/coroot-boundary-poc_test.go.fixture", source / "model/task29_boundary_test.go")
                        go_test(["./model"], "^(TestCheckConfigs_getRaw|TestTask29CorootCheckAuditBoundary)$")
                    elif key == "keep-community-model":
                        selected = source / "selected"
                        selected.mkdir()
                        for f in cap["selectedFiles"]:
                            target = selected / f["sourcePath"]
                            target.parent.mkdir(parents=True, exist_ok=True)
                            shutil.copyfile(source / f["sourcePath"], target)
                        requirements = source / "requirements.txt"
                        requirements.write_text("".join(f"{m['path']}=={m['version']} --hash=sha256:{m['artifactSHA256']}\n" for m in records[key]["modules"]))
                        docker(images["python"], ["python", "/fixtures/replay-nonvirtual.py", "--container-keep", f"/work/{key}/requirements.txt", f"/work/{key}/selected"], extra=["--mount", f"type=bind,src={args.keep_wheels.resolve()},dst=/wheels,readonly"])
                    elif key == "metal3-bmo-model":
                        go_test(["./metal3.io/v1alpha1"], ".", cwd=f"/work/{key}/apis")
                    elif key == "gofish-redfish":
                        go_test(["./schemas"], "^Test(Chassis|ComputerSystem|Manager|LogEntry)")
                    elif key == "ipmi-exporter":
                        shutil.copyfile(fixtures / "ipmi-exporter/ipmi-collector-poc_test.go.fixture", source / "task29_fixture_test.go")
                        go_test(["./"], "^TestTask29IPMICollectorFixture$")
                    elif key == "smartctl-exporter":
                        shutil.copyfile(fixtures / "smartctl-exporter/smartctl-fixture-poc_test.go.fixture", source / "task29_fixture_test.go")
                        # The upstream fake-data reader looks up debug/<device>.json.
                        # Bind its expected path to the already hash-checked
                        # upstream testdata fixture, without a block-device scan.
                        (source / "debug").mkdir(exist_ok=True)
                        shutil.copyfile(fixtures / "smartctl-exporter/sat-Hitachi_Ultrastar_A7K2000-Hitachi_HUA722010CLA330-sdr.json", source / "debug/sdr.json")
                        go_test(["./"], "^(TestCollectDevices|TestReadSMARTctl|TestTask29SMARTctlFixture)$")
                    log.write("replay_exit_code=0\n")
                except Exception as error:
                    log.write(f"replay_exit_code=1 error={error}\n")
                    summary["results"].append({"id": key, "exitCode": 1, "log": str(log_path)})
                    (args.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
                    raise
            summary["results"].append({"id": key, "exitCode": 0, "log": str(log_path), "logSHA256": digest(log_path), "runtimeEnabled": False})
            print(f"{key}: exit 0; runtime remains disabled; log={log_path}", flush=True)
    (args.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--container-keep":
        container_keep()
    else:
        main()
