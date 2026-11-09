#!/usr/bin/env python3
"""Bounded, synthetic 24h workload: +10% concurrent, 2% disconnected clients.

No prompt/response bodies enter reports. Optional --pids enables RSS/children
sampling with psutil on the same host as gateway/agent/engine.
"""
import argparse
import concurrent.futures
import http.client
import json
import os
import random
import ssl
import time
import urllib.parse
from pathlib import Path

from acceptance_v2 import Client


def disconnect(client):
    parsed = urllib.parse.urlsplit(client.base)
    connection = (http.client.HTTPSConnection(parsed.hostname, parsed.port, context=ssl.create_default_context(cafile=os.environ.get("OPENAI_CA_BUNDLE") or None))
                  if parsed.scheme == "https" else http.client.HTTPConnection(parsed.hostname, parsed.port))
    payload = json.dumps(client.payload("Zähle fortlaufend bis 10000.", max_tokens=8192))
    connection.request("POST", parsed.path.rstrip("/") + "/chat/completions", payload.encode(),
                       headers={"Content-Type": "application/json", "Authorization": "Bearer " + client.key})
    try:
        time.sleep(3)
    finally:
        connection.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--hours", type=float, default=24)
    parser.add_argument("--interval", type=float, default=10)
    parser.add_argument("--report", required=True)
    parser.add_argument("--pids", type=int, nargs="*", default=[])
    args = parser.parse_args()
    if args.hours <= 0 or args.interval <= 0:
        parser.error("hours and interval must be positive")
    client = Client(args.base, args.model)
    rng = random.Random(20261109)
    counts = {"attempted": 0, "completed": 0, "intentional_disconnect": 0, "overload": 0, "gateway_error": 0, "transport_error": 0}
    samples = []
    start = time.monotonic()
    processes = []
    if args.pids:
        import psutil
        processes = [psutil.Process(pid) for pid in args.pids]
    def request():
        try:
            status, _, body, duration = client.call(client.payload(max_tokens=32, chat_template_kwargs={"enable_thinking": False}))
            code = body.get("error", {}).get("code")
            return ("completed" if status == 200 else "overload" if code == "model_busy" else "gateway_error"), duration
        except Exception:  # noqa: BLE001 — bounded diagnostic; never emit remote exception contents
            return "transport_error", None

    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
        while time.monotonic() - start < args.hours * 3600:
            batch_start = time.monotonic()
            if rng.random() < 0.02:
                counts["intentional_disconnect"] += 1
                try:
                    disconnect(client)
                except OSError:
                    counts["transport_error"] += 1
            else:
                jobs = [executor.submit(request)]
                if rng.random() < 0.10:
                    jobs.append(executor.submit(request))
                for job in jobs:
                    key, _ = job.result()
                    counts["attempted"] += 1
                    counts[key] += 1
            sample = {"elapsed_seconds": round(time.monotonic() - start), "processes": []}
            for process in processes:
                try:
                    sample["processes"].append({"pid": process.pid, "rss": process.memory_info().rss, "children": sorted(p.pid for p in process.children(recursive=True))})
                except Exception:  # noqa: BLE001 — bounded diagnostic; never emit remote exception contents
                    sample["processes"].append({"pid": process.pid, "missing": True})
            try:
                _, _, status, _ = client.call(path="/status")
                sample["nodes"] = status.get("nodes", [])
                sample["queue_length"] = status.get("queue_length")
            except Exception:  # noqa: BLE001 — bounded diagnostic; never emit remote exception contents
                sample["status_unavailable"] = True
            samples.append(sample)
            report = {"duration_seconds": round(time.monotonic() - start), "counts": counts, "samples": samples, "customer_release": False}
            Path(args.report).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
            time.sleep(max(0, min(args.interval - (time.monotonic() - batch_start), args.hours * 3600 - (time.monotonic() - start))))
    return 0 if not counts["gateway_error"] and not counts["transport_error"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
