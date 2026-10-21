import argparse, json, os, socket, ssl, subprocess, tempfile, time, urllib.request, urllib.error
from pathlib import Path
from playwright.sync_api import sync_playwright

parser = argparse.ArgumentParser(
    description="Browser enrollment, mTLS join and audit integration smoke test"
)
parser.add_argument("--gateway", type=Path, required=True)
parser.add_argument("--chromium", default="/usr/bin/chromium")
parser.add_argument("--screenshot", type=Path)
parser.add_argument(
    "--runtime",
    type=Path,
    required=True,
    help="A runnable local llama-server for setup inspection",
)
args = parser.parse_args()
binary = str(args.gateway.resolve(strict=True))


def port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


processes = []
with tempfile.TemporaryDirectory(prefix="ki-dashboard-") as temp:
    root = Path(temp)
    gp, bp, ap = port(), port(), port()

    def start(args, name):
        output = (root / (name + ".log")).open("w")
        p = subprocess.Popen([binary, *args], stdout=output, stderr=subprocess.STDOUT)
        processes.append(p)
        return p

    try:
        start(
            [
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
        agent_process = start(
            [
                "agent-ui",
                "--dir",
                str(root / "agent"),
                "--port",
                str(ap),
                "--open-browser=false",
            ],
            "agent",
        )
        for _ in range(100):
            try:
                with urllib.request.urlopen(
                    f"http://127.0.0.1:{bp}/healthz", timeout=1
                ):
                    pass
                text = (root / "agent.log").read_text()
                if "/#" in text:
                    break
            except Exception:
                pass
            time.sleep(0.1)
        key = json.loads((root / "central" / "access.json").read_text())["key"]
        agent_url = (root / "agent.log").read_text().strip().split()[-1]
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{ap}/setup/status")
            raise AssertionError("Setup API accessible without session token")
        except urllib.error.HTTPError as e:
            assert e.code == 401
        req = urllib.request.Request(
            f"http://127.0.0.1:{ap}/setup/status", headers={"Host": "evil.example"}
        )
        try:
            urllib.request.urlopen(req)
            raise AssertionError("Setup API vulnerable to host rebinding")
        except urllib.error.HTTPError as e:
            assert e.code == 403
        with sync_playwright() as pw:
            browser = pw.chromium.launch(
                executable_path=args.chromium, headless=True, args=["--no-sandbox"]
            )
            page = browser.new_page(viewport={"width": 1400, "height": 1100})
            errors = []
            page.on("pageerror", lambda e: errors.append(str(e)))
            page.goto(f"http://127.0.0.1:{bp}")
            page.locator("#api-key").fill(key)
            page.locator("#user-id").fill("browser-admin")
            page.locator("#connect-button").click()
            page.locator("#admin-tools").wait_for(state="visible")
            assert page.locator("#api-key").input_value() == ""
            assert page.evaluate("Object.keys(localStorage).length") == 0
            page.locator("#add-server").evaluate("(e)=>e.open=true")
            page.locator("#server-name").fill("browser-node")
            page.locator("#gateway-address").fill(f"https://127.0.0.1:{gp}")
            page.locator("#loopback-ack").check()
            page.locator("#server-next").click()
            page.locator("#server-next").click()
            page.locator("#enroll-button").click()
            page.locator("#bundle-download").wait_for(state="visible")
            with page.expect_download() as pending:
                page.locator("#bundle-download").click()
            bundle = root / "connection.json"
            pending.value.save_as(bundle)
            setup = browser.new_page()
            setup.on("pageerror", lambda e: errors.append(str(e)))
            setup.goto(agent_url)
            setup.locator("#bundle").set_input_files(str(bundle))
            setup.wait_for_function(
                "() => document.getElementById('bundle-info').textContent.includes('browser-node')"
            )
            setup.locator("#agent-next").click()
            setup.locator("#advanced-settings").evaluate("(e)=>e.open=true")
            setup.locator("#binary").fill(str(args.runtime.resolve(strict=True)))
            setup.locator("#agent-next").click()
            setup.locator("#run-checks").click()
            try:
                setup.wait_for_function(
                    "() => !document.getElementById('agent-next').disabled",
                    timeout=20000,
                )
            except Exception:
                print(setup.locator("#setup-checks").inner_text())
                print(setup.locator("#result").inner_text())
                raise
            setup.locator("#agent-next").click()
            setup.locator("#submit").click()
            setup.wait_for_function(
                "() => document.getElementById('result').textContent.includes('Verbunden')",
                timeout=15000,
            )
            setup.reload()
            setup.locator("#edit-settings").wait_for(state="visible")
            setup.locator("#edit-settings").click()
            setup.locator("#save-settings").click()
            setup.wait_for_function(
                "() => document.getElementById('result').textContent.includes('Einstellungen gespeichert')"
            )
            with setup.expect_download() as backup_pending:
                setup.locator("#backup-agent").click()
            backup_pending.value.save_as(root / "agent-backup.zip")
            with page.expect_download() as backup_pending:
                page.locator("#backup-central").click()
            backup_pending.value.save_as(root / "central-backup.zip")
            # Restore the ZIP snapshots, not just their gzip/container checksums.
            subprocess.run(
                [
                    binary,
                    "restore",
                    "--archive",
                    str(root / "central-backup.zip"),
                    "--dir",
                    str(root / "restored-central"),
                ],
                check=True,
            )
            verified = subprocess.check_output(
                [
                    binary,
                    "audit-verify",
                    "--config",
                    str(root / "restored-central/config.json"),
                ]
            )
            assert json.loads(verified)["valid"]
            agent_process.terminate()
            agent_process.wait(timeout=10)
            restored_port = port()
            start(
                [
                    "agent-ui",
                    "--dir",
                    str(root / "restored-agent"),
                    "--port",
                    str(restored_port),
                    "--open-browser=false",
                ],
                "restored-agent",
            )
            for _ in range(100):
                restored_log = root / "restored-agent.log"
                if restored_log.exists() and "/#" in restored_log.read_text():
                    break
                time.sleep(0.1)
            restored_page = browser.new_page()
            restored_page.on("pageerror", lambda e: errors.append(str(e)))
            restored_page.goto(restored_log.read_text().strip().split()[-1])
            restored_page.locator("#restore-file").locator("..").evaluate(
                "(e)=>e.open=true"
            )
            restored_page.locator("#restore-file").set_input_files(
                str(root / "agent-backup.zip")
            )
            restored_page.locator("#restore-confirm").check()
            restored_page.locator("#restore-agent").click()
            restored_page.wait_for_function(
                "() => document.getElementById('result').textContent.includes('wiederhergestellt')",
                timeout=15000,
            )
            page.locator("#refresh-top").click()
            page.locator("#managed-devices").get_by_role(
                "heading", name="browser-node"
            ).wait_for()
            page.locator("#load-audit").click()
            page.wait_for_function(
                "() => document.getElementById('audit-rows').textContent.includes('node.joined') && document.getElementById('audit-rows').textContent.includes('agent.settings_change_requested')"
            )
            if args.screenshot:
                page.screenshot(path=str(args.screenshot), full_page=True)
            page.locator("#disconnect-top").click()
            assert page.locator("#admin-tools").is_hidden()
            assert page.locator("#audit-rows").text_content() == ""
            assert not errors, errors
            browser.close()
        print(
            json.dumps(
                {
                    "browser_enrollment": True,
                    "guided_steps": True,
                    "runtime_preflight": True,
                    "reload_keeps_setup_session": True,
                    "agent_and_central_backup": True,
                    "agent_gui_restore": True,
                    "central_restore_audit_verified": True,
                    "real_mtls_join": True,
                    "agent_ui_import": True,
                    "audit_visible": True,
                    "setup_api_token_required": True,
                    "setup_host_rebinding_rejected": True,
                    "local_settings_audited": True,
                    "credentials_memory_only": True,
                    "disconnect_clears_data": True,
                }
            )
        )
    finally:
        for p in reversed(processes):
            p.terminate()
        for p in reversed(processes):
            try:
                p.wait(timeout=10)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
