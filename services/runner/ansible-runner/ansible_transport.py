"""Locked Ansible Runner transport. Input is created inside one isolated Job."""
import json
import os
import pathlib
import sys
import ansible_runner


def main():
    data = json.loads(pathlib.Path(sys.argv[1]).read_text())
    root = pathlib.Path(data["privateDataDir"])
    # Never inherit ANSIBLE_*, SSH_AUTH_SOCK, user configuration, plugins, or CLI args.
    cfg = root / "ansible.cfg"
    cfg.write_text("[defaults]\nhost_key_checking=True\nretry_files_enabled=False\nlocal_tmp=" + str(root / "local") + "\n[ssh_connection]\npipelining=True\nssh_args=-C -o ControlMaster=no -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + str(root / "known_hosts") + " -o GlobalKnownHostsFile=/dev/null -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o CertificateFile=" + str(root / "identity-cert.pub") + "\n")
    os.chmod(cfg, 0o600)
    env = {"ANSIBLE_CONFIG": str(cfg), "ANSIBLE_HOST_KEY_CHECKING": "True", "ANSIBLE_NOCOLOR": "1"}
    inventory = {"all": {"hosts": {"execution_target": {
        "ansible_host": data["address"], "ansible_port": data["port"],
        "ansible_user": data["principal"], "ansible_ssh_private_key_file": str(root / "identity"),
        "ansible_connection": "ssh", "ansible_shell_executable": "/bin/bash",
        "ansible_python_interpreter": "/usr/bin/python3", "ansible_become": False,
    }}}}
    play = [{"hosts": "execution_target", "gather_facts": False, "become": False,
             "tasks": [{"name": "operator command", "ansible.builtin.shell": {
                 "cmd": data["command"], "executable": "/bin/bash"}, "no_log": False,
                 "register": "operator_result", "ignore_errors": True, "environment": {"BASH_ENV": "/dev/null", "SHELLOPTS":"pipefail"}}]}]
    result = {"certain": False}
    def event(e):
        if e.get("event") in ("runner_on_ok", "runner_on_failed", "runner_on_unreachable"):
            res = e.get("event_data", {}).get("res", {})
            if e.get("event") == "runner_on_unreachable":
                result["rc"] = 255
            elif "rc" in res:
                result["rc"] = max(0, min(255, int(res["rc"])))
                result["certain"] = True
            # Invocation, command, environment and transport fields stay in the
            # memory-backed private data directory, removed by the Job Runner.
            for name, stream in (("stdout", sys.stdout), ("stderr", sys.stderr)):
                value = res.get(name, "")
                if isinstance(value, str):
                    stream.write(value)
                    stream.flush()
        return True
    r = ansible_runner.run(private_data_dir=str(root), inventory=inventory, playbook=play,
                           envvars=env, quiet=True, event_handler=event,
                           settings={"pexpect_timeout": 10, "idle_timeout": 900},
                           suppress_env_files=True)
    result_path = root / "transport-result.json"
    result_path.write_text(json.dumps({"certain": result["certain"], "exitCode": result.get("rc", 255)}))
    os.chmod(result_path, 0o600)
    return result.get("rc", 255)

if __name__ == "__main__":
    sys.exit(main())
