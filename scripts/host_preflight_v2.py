#!/usr/bin/env python3
"""Read-only deployment evidence. Unknown host privacy settings fail closed.

Run as the actual service account on every target host. This script never edits
registry, swap, dump, firewall or service settings and never prints credentials.
"""
import argparse
import json
import platform
import subprocess
from pathlib import Path


def inspect():
    result = {"platform": platform.system(), "checks": [], "customer_release": False}
    def add(name, passed, note):
        result["checks"].append({"name": name, "passed": passed, "note": note})
    if platform.system() == "Linux":
        import resource
        soft, hard = resource.getrlimit(resource.RLIMIT_CORE)
        add("core_dumps_disabled", soft == 0 and hard == 0, "Both limits must be zero for the service, not just the installer shell.")
        try:
            swaps = Path("/proc/swaps").read_text(encoding="utf-8").splitlines()[1:]
            add("swap_disabled_or_encrypted", True if not swaps else None, "No swap active." if not swaps else "Active swap requires independent backing-volume encryption evidence.")
        except OSError:
            add("swap_disabled_or_encrypted", None, "Cannot inspect /proc/swaps.")
        for name in ("ki-basis-gateway.service", "ki-basis-agent.service"):
            try:
                proc = subprocess.run(["systemctl", "show", name, "--property=User,ActiveState,UnitFileState,LimitCORE,Restart"], capture_output=True, text=True, timeout=5, check=False)
                values = dict(line.split("=", 1) for line in proc.stdout.splitlines() if "=" in line)
                add(name, values.get("UnitFileState") == "enabled" and values.get("ActiveState") == "active" and values.get("Restart") == "on-failure" and values.get("User") not in {None, "", "root"}, "Service enabled, active, restart policy and dedicated account required; reboot proof remains separate.")
            except (OSError, subprocess.TimeoutExpired):
                add(name, None, "systemd unavailable.")
    elif platform.system() == "Windows":
        command = "$s=Get-CimInstance Win32_Service | Where-Object {$_.Name -in @('KI-Basis-Gateway','KI-Basis-Agent')}; $s | Select-Object Name,State,StartMode,StartName | ConvertTo-Json"
        try:
            proc = subprocess.run(["powershell", "-NoProfile", "-NonInteractive", "-Command", command], capture_output=True, text=True, timeout=10, check=False)
            services = json.loads(proc.stdout or "[]")
            if isinstance(services, dict):
                services = [services]
            add("unattended_services", bool(services) and all(s.get("StartMode") == "Auto" and s.get("State") == "Running" and str(s.get("StartName", "")).startswith("NT SERVICE\\") for s in services), "Reboot without login is an additional test.")
        except (OSError, ValueError, subprocess.TimeoutExpired):
            add("unattended_services", None, "Unable to inspect Windows services.")
        add("wer_localdumps_disabled", None, "Inspect WER/LocalDumps for gateway, agent and llama-server; include effective machine policy.")
        add("pagefile_and_hibernation_encrypted_or_disabled", None, "Document BitLocker/pagefile/hibernation and crash-dump policy on the target host.")
    else:
        add("host_privacy", None, "Dedicated host review required for this platform.")
    result["checks_passed"] = all(c["passed"] is True for c in result["checks"])
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    report = inspect()
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return 0 if report["checks_passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
