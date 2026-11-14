#!/usr/bin/env python3
"""Exercise the packaged service only on an isolated GitHub Actions Linux runner."""
import argparse
import http.client
import os
from pathlib import Path
import pwd
import shutil
import socket
import subprocess
import sys
import time
from metadata import load


def run(*args, check=True):
    result = subprocess.run(args, text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"{' '.join(args)} failed ({result.returncode})\n{result.stdout}{result.stderr}")
    return result


def wait_state(unit, expected):
    for _ in range(100):
        state = run("systemctl", "is-active", unit, check=False).stdout.strip()
        if state == expected:
            return
        time.sleep(0.1)
    raise AssertionError(f"service state is {state}, expected {expected}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("package", type=Path)
    args = parser.parse_args()
    if sys.platform != "linux" or os.geteuid() != 0 or os.environ.get("GITHUB_ACTIONS") != "true":
        parser.exit(1, "Run only as root on an isolated GitHub Actions Linux runner\n")
    app_id = load()["id"]
    app = Path("/usr/local") / app_id
    service, sock_unit = app_id + ".service", app_id + ".socket"
    socket_path = Path("/var/api") / (app_id + ".sock")
    if app.exists() or app.is_symlink() or socket_path.exists() or socket_path.is_symlink() or run("id", app_id, check=False).returncode == 0:
        parser.exit(1, "Refusing to replace an existing application or account\n")
    package = str(args.package.resolve())
    created_account = False
    try:
        run("dpkg", "--install", package)
        assert not (app / "data").exists(), "data initialization must wait for the TOS account"
        assert (app / "site/index.html").is_file()
        run("useradd", "--system", "--user-group", "--no-create-home", "--shell", "/usr/sbin/nologin", app_id)
        created_account = True
        account = pwd.getpwnam(app_id)
        run("systemctl", "reset-failed", service, sock_unit)
        run("systemctl", "start", service)
        wait_state(service, "active")
        connection = http.client.HTTPConnection("localhost", timeout=5)
        connection.sock = socket.socket(socket.AF_UNIX)
        connection.sock.settimeout(5)
        connection.sock.connect(str(socket_path))
        connection.request("GET", "/api/info")
        response = connection.getresponse()
        assert response.status == 401, "socket must enforce authentication"
        response.read()
        connection.close()
        data = app / "data"
        assert data.stat().st_uid == account.pw_uid and data.stat().st_mode & 0o777 == 0o700
        pid = int(run("systemctl", "show", "--property=MainPID", "--value", service).stdout)
        assert Path(f"/proc/{pid}").stat().st_uid == account.pw_uid
        assert socket_path.stat().st_mode & 0o777 == 0o660
        record = data / "retain.txt"
        record.write_text("user data")
        run("systemctl", "stop", service)
        assert not socket_path.exists()
        data.rename(app / "saved-data")
        data.symlink_to(app / "saved-data", target_is_directory=True)
        run("systemctl", "start", service, check=False)
        wait_state(service, "failed")
        assert record.read_text() == "user data"
        assert run("systemctl", "show", "--property=NRestarts", "--value", service).stdout.strip() == "0"
        data.unlink()
        (app / "saved-data").rename(data)
        run("systemctl", "reset-failed", service, sock_unit)
        run("systemctl", "start", service)
        wait_state(service, "active")
        (app / "site/stale.js").write_text("old frontend")
        run("dpkg", "--install", package)
        wait_state(service, "active")
        assert not (app / "site/stale.js").exists()
        assert record.read_text() == "user data"
        run("dpkg", "--remove", app_id)
        run("dpkg", "--purge", app_id)
        assert not (app / "site").exists() and not socket_path.exists()
        assert record.read_text() == "user data"
        assert run("systemctl", "is-enabled", service, check=False).returncode != 0
        assert run("systemctl", "is-enabled", sock_unit, check=False).returncode != 0
        print("PASS systemd: delayed account, non-root HTTP, socket, unsafe data rejection, recovery, upgrade, removal")
    except Exception:
        print(run("journalctl", "-u", service, "-n", "80", "--no-pager", check=False).stdout, file=sys.stderr)
        raise
    finally:
        run("systemctl", "stop", service, sock_unit, check=False)
        run("dpkg", "--purge", app_id, check=False)
        if created_account:
            run("userdel", app_id, check=False)
        if app.is_dir() and not app.is_symlink():
            shutil.rmtree(app)


if __name__ == "__main__":
    main()
