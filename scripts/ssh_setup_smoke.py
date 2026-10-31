"""Provider-neutral provisioning against an isolated, real local OpenSSH server.

Requires sshd/ssh/ssh-keyscan, a runnable llama.cpp archive, and loopback access.
No cloud account, existing SSH credentials or system services are modified.
"""

import argparse, getpass, json, os, shlex, signal, socket, subprocess, tempfile, time, urllib.request, urllib.error
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--gateway", type=Path, required=True)
parser.add_argument("--runtime-archive", type=Path, required=True)
args = parser.parse_args()
binary = str(args.gateway.resolve(strict=True))
runtime_archive = str(args.runtime_archive.resolve(strict=True))


def port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


with tempfile.TemporaryDirectory(prefix="ki-ssh-smoke-") as temp:
    root = Path(temp)
    home = root / "target-home"
    home.mkdir()
    processes = []

    def start(command, name):
        log = (root / (name + ".log")).open("w")
        process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
        processes.append(process)
        return process

    def request(url, token=None, body=None, key=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["X-Setup-Token"] = token
        if key:
            headers.update({"Authorization": "Bearer " + key, "X-User-ID": "ssh-smoke"})
        data = json.dumps(body).encode() if body is not None else None
        try:
            with urllib.request.urlopen(
                urllib.request.Request(url, data=data, headers=headers), timeout=170
            ) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(
                f"HTTP {error.code}: " + error.read().decode()
            ) from error

    try:
        for name in ["host", "client"]:
            subprocess.run(
                ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(root / name)],
                check=True,
            )
        authorized = root / "authorized_keys"
        authorized.write_text((root / "client.pub").read_text())
        wrapper = root / "target-shell"
        wrapper.write_text(
            "#!/bin/sh\nexport HOME="
            + shlex.quote(str(home))
            + '\nexec /bin/sh -c "$SSH_ORIGINAL_COMMAND"\n'
        )
        wrapper.chmod(0o700)
        sp, gp, bp, ap = port(), port(), port(), port()
        config = root / "sshd_config"
        config.write_text(
            f"""Port {sp}
ListenAddress 127.0.0.1
HostKey {root / 'host'}
PidFile {root / 'sshd.pid'}
AuthorizedKeysFile {authorized}
StrictModes no
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
AllowUsers {getpass.getuser()}
ForceCommand {wrapper}
LogLevel VERBOSE
"""
        )
        start(["/usr/sbin/sshd", "-D", "-e", "-f", str(config)], "sshd")
        start(
            [
                binary,
                "quickstart",
                "--dir",
                str(root / "central"),
                "--gateway-port",
                str(gp),
                "--port",
                str(bp),
            ],
            "central",
        )
        start(
            [
                binary,
                "agent-ui",
                "--dir",
                str(root / "manager"),
                "--port",
                str(ap),
                "--open-browser=false",
            ],
            "manager",
        )
        for _ in range(100):
            try:
                manager_text = (root / "manager.log").read_text()
                key = json.loads((root / "central/access.json").read_text())["key"]
                if "/#" in manager_text:
                    request(f"http://127.0.0.1:{bp}/healthz")
                    break
            except Exception:
                pass
            time.sleep(0.1)
        token = manager_text.strip().split("#")[-1]
        manager = f"http://127.0.0.1:{ap}"
        central = f"http://127.0.0.1:{bp}"
        target = {
            "host": "127.0.0.1",
            "port": sp,
            "user": getpass.getuser(),
            "key": str(root / "client"),
        }
        scan = request(manager + "/setup/ssh/check", token, target)
        known = root / "known_hosts"
        known.write_bytes(
            subprocess.check_output(
                ["ssh-keyscan", "-p", str(sp), "-t", "ed25519", "127.0.0.1"],
                stderr=subprocess.DEVNULL,
            )
        )
        probe = subprocess.run(
            [
                "ssh",
                "-F",
                os.devnull,
                "-vv",
                "-T",
                "-p",
                str(sp),
                "-o",
                "BatchMode=yes",
                "-o",
                "StrictHostKeyChecking=yes",
                "-o",
                "UserKnownHostsFile=" + str(known),
                "-i",
                str(root / "client"),
                "-o",
                "IdentitiesOnly=yes",
                getpass.getuser() + "@127.0.0.1",
                "uname -s; uname -m",
            ],
            capture_output=True,
        )
        if probe.returncode:
            raise RuntimeError(probe.stderr.decode()[-4000:])
        bundle = request(
            central + "/admin/enroll",
            key=key,
            body={
                "id": "ssh-installed",
                "gateway": f"https://127.0.0.1:{gp}",
                "model": "chat",
                "revision": "ssh-smoke",
                "capacity": 1,
            },
        )
        # A failed target preflight must explain the missing network path without consuming enrollment.
        bad_bundle = dict(bundle, gateway=f"https://127.0.0.1:{port()}")
        try:
            request(
                manager + "/setup/ssh/install",
                token,
                {
                    "target": target,
                    "fingerprint": scan["fingerprint"],
                    "bundle": bad_bundle,
                    "runtime_archive": runtime_archive,
                },
            )
            raise AssertionError("Unreachable gateway passed preflight")
        except RuntimeError as error:
            assert "setup_checks_failed" in str(error) and "connection" in str(
                error
            ), str(error)
        assert not (home / ".local/share/KI-Basis/agent/agent.json").exists()
        result = request(
            manager + "/setup/ssh/install",
            token,
            {
                "target": target,
                "fingerprint": scan["fingerprint"],
                "bundle": bundle,
                "runtime_archive": runtime_archive,
            },
        )
        assert result["connected"], result
        state_dir = home / ".local/share/KI-Basis/agent"
        assert (state_dir / "agent.json").exists()
        settings = json.loads((state_dir / "settings.json").read_text())
        assert Path(settings["binary"]).exists()
        assert not (
            home / ".local/share/KI-Basis/connection.json"
        ).exists(), "Consumed token retained"
        assert not (
            home / ".local/share/KI-Basis/client"
        ).exists(), "Manager private key copied"
        for _ in range(100):
            overview = request(central + "/admin/overview", key=key)
            device = next(d for d in overview["devices"] if d["id"] == "ssh-installed")
            if device["report"].get("os") == "linux":
                break
            time.sleep(0.1)
        assert (
            device["report"].get("os") == "linux"
        ), "Installed agent did not heartbeat"
        # The same file can safely resume after expiry without re-enrollment.
        identity_before = (state_dir / "agent.json").read_bytes()
        expired = dict(bundle, expires_at="2000-01-01T00:00:00Z")
        retry_body = {
            "target": target,
            "fingerprint": scan["fingerprint"],
            "bundle": expired,
            "resume": True,
        }
        resumed = request(manager + "/setup/ssh/install", token, retry_body)
        assert resumed["connected"]
        assert (state_dir / "agent.json").read_bytes() == identity_before
        session = json.loads((state_dir / ".ui-session.json").read_text())
        # Quit the remote process, then restart it through the exact same recovery path.
        request(session["url"] + "/setup/quit", session["token"], {})
        for _ in range(100):
            if not (state_dir / ".ui-session.json").exists():
                break
            time.sleep(0.1)
        resumed = request(manager + "/setup/ssh/install", token, retry_body)
        assert resumed["connected"]
        assert (state_dir / "agent.json").read_bytes() == identity_before
        assert (
            json.loads((state_dir / "deployment.json").read_text())["mode"] == "manual"
        )
        # A different identity must never replace this enrolled server.
        wrong = dict(retry_body, bundle=dict(expired, node_id="different-node"))
        try:
            request(manager + "/setup/ssh/install", token, wrong)
            raise AssertionError("Mismatched resume identity accepted")
        except RuntimeError as error:
            assert "agent_identity_mismatch" in str(error), str(error)
        assert (state_dir / "agent.json").read_bytes() == identity_before
        tunnel = request(
            manager + "/setup/tunnel",
            token,
            {
                "target": target,
                "fingerprint": scan["fingerprint"],
                "web_port": bp,
                "gateway_port": gp,
            },
        )
        for _ in range(30):
            try:
                assert request(tunnel["browser"] + "/healthz")["status"] == "ok"
                break
            except Exception:
                time.sleep(0.1)
        else:
            raise AssertionError("Managed tunnel did not forward the dashboard")
        # A second launch must reuse the same manager rather than start duplicate agents.
        reopened = subprocess.check_output(
            [
                binary,
                "agent-ui",
                "--dir",
                str(root / "manager"),
                "--open-browser=false",
            ],
            timeout=5,
        ).decode()
        assert "bereits geöffnet" in reopened
        request(manager + "/setup/tunnel/disconnect", token, {})
        print(
            json.dumps(
                {
                    "managed_ssh_tunnel": True,
                    "single_instance_reuse": True,
                    "safe_resume_and_restart": True,
                    "target_network_diagnostics": True,
                    "mismatched_identity_refused": True,
                    "real_openssh": True,
                    "host_key_verified": True,
                    "runtime_transfer_and_probe": True,
                    "remote_mtls_enrollment": True,
                    "background_agent_heartbeat": True,
                    "manager_key_not_copied": True,
                }
            )
        )
    except Exception:
        for name in ["sshd", "manager"]:
            print(
                name
                + ": "
                + (root / (name + ".log"))
                .read_text()[-2000:]
                .replace(token if "token" in locals() else "not-a-token", "[redacted]")
            )
        raise
    finally:
        # Only terminate detached children of this fixture's isolated target home.
        for process in Path("/proc").iterdir():
            if not process.name.isdigit():
                continue
            try:
                command = (process / "cmdline").read_bytes()
                if str(home).encode() in command and b"agent-ui" in command:
                    os.kill(int(process.name), signal.SIGTERM)
            except (OSError, ValueError):
                pass
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
