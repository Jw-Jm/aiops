"""Run the catalog-locked PostgreSQL/Keycloak startup PoC without networking.

This is preparation evidence, not Component Catalog qualification or Kubernetes
offline installation acceptance. Only containers created by this invocation
are removed; credentials live in a private temporary directory outside Git.
"""
import json
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time


POSTGRES = "docker.io/library/postgres@sha256:75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257"
KEYCLOAK = "quay.io/keycloak/keycloak@sha256:1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c"


def docker(*args, check=True, timeout=240):
    return subprocess.run(
        ["docker", "--context", "orbstack", *args], capture_output=True,
        text=True, check=check, timeout=timeout,
    )


def main():
    for image in (POSTGRES, KEYCLOAK):
        docker("image", "inspect", image)
    temporary = Path(tempfile.mkdtemp(prefix="ops-task27-core-startup-"))
    containers = []
    owner = "ops-task27-" + secrets.token_hex(6)
    password = secrets.token_urlsafe(32)
    report = {"scope": "isolated Docker startup PoC", "network": "none",
              "qualification": "candidate", "postgresqlImage": POSTGRES,
              "keycloakImage": KEYCLOAK}
    try:
        data, distribution = temporary / "pgdata", temporary / "keycloak"
        for path in (data, distribution):
            path.mkdir()
            path.chmod(0o777)  # Disposable bind mount; processes use fixed non-root UIDs.
        pg_env, kc_env = temporary / "pg.env", temporary / "kc.env"
        pg_env.write_text(f"POSTGRES_USER=ops\nPOSTGRES_PASSWORD={password}\nPOSTGRES_DB=ops\nPGDATA=/var/lib/postgresql/data/pgdata\n")
        kc_env.write_text(
            f"KC_DB=postgres\nKC_DB_URL=jdbc:postgresql://127.0.0.1:5432/ops\n"
            f"KC_DB_USERNAME=ops\nKC_DB_PASSWORD={password}\nKC_HTTP_ENABLED=true\n"
            f"KC_BOOTSTRAP_ADMIN_USERNAME=admin\nKC_BOOTSTRAP_ADMIN_PASSWORD={password}\n"
        )
        for path in (pg_env, kc_env):
            path.chmod(0o600)
        common = ["--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
                  "--label", "ops.platform.io/poc-owner=" + owner]
        pg = docker("run", "-d", "--name", owner + "-pg", "--network=none", "--user=999:999",
                    *common, "--env-file", str(pg_env),
                    "--mount", f"type=bind,src={data},dst=/var/lib/postgresql/data",
                    "--tmpfs", "/tmp:uid=999,gid=999,mode=0700",
                    "--tmpfs", "/var/run/postgresql:uid=999,gid=999,mode=0770", POSTGRES).stdout.strip()
        containers.append(pg)
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            result = docker("exec", pg, "psql", "-U", "ops", "-d", "ops", "-Atc", "SELECT 1", check=False)
            if result.returncode == 0 and result.stdout.strip() == "1":
                report["postgresqlSQL"] = True
                break
            time.sleep(1)
        else:
            raise RuntimeError("PostgreSQL did not answer SELECT 1")
        docker("run", "--rm", "--network=none", "--user=1000:0", *common,
               "--memory=2g", "--cpus=2", "--mount", f"type=bind,src={distribution},dst=/work",
               "--tmpfs", "/tmp:uid=1000,gid=0,mode=0700", "--entrypoint=/bin/bash", KEYCLOAK,
               "-ec", "shopt -s dotglob && cp -R /opt/keycloak/* /work/ && /work/bin/kc.sh build --db=postgres --health-enabled=true")
        kc = docker("run", "-d", "--name", owner + "-kc", "--network=container:" + pg,
                    "--user=1000:0", *common, "--memory=2g", "--cpus=2", "--env-file", str(kc_env),
                    "--mount", f"type=bind,src={distribution},dst=/opt/keycloak,readonly",
                    "--tmpfs", "/tmp:uid=1000,gid=0,mode=0700",
                    "--tmpfs", "/opt/keycloak/data:uid=1000,gid=0,mode=0700",
                    KEYCLOAK, "start", "--optimized", "--hostname-strict=false").stdout.strip()
        containers.append(kc)
        request = (
            "exec 3<>/dev/tcp/127.0.0.1/9000; "
            "printf 'GET /health/ready HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n' >&3; "
            "IFS= read -r status <&3; [[ $status == *' 200 '* ]]"
        )
        deadline = time.monotonic() + 150
        while time.monotonic() < deadline:
            result = docker("exec", pg, "/bin/bash", "-ec", request, check=False)
            if result.returncode == 0:
                report["keycloakManagementReady"] = True
                break
            time.sleep(2)
        else:
            raise RuntimeError("Keycloak did not report ready on its management port")
        report["readOnlyRootFilesystem"] = True
        print(json.dumps(report, indent=2))
    finally:
        for container in reversed(containers):
            label = docker("inspect", "--format", '{{index .Config.Labels "ops.platform.io/poc-owner"}}', container).stdout.strip()
            if label != owner:
                raise RuntimeError("PoC cleanup ownership mismatch")
            docker("rm", "-f", container)
        shutil.rmtree(temporary)


if __name__ == "__main__":
    main()
