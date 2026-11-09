#!/usr/bin/env python3
"""Compare 100 alternating direct/gateway max_tokens:1 calls, synthetic only."""
import argparse
import json
import math
import os
import statistics
from pathlib import Path

from acceptance_v2 import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--engine-base", required=True)
    parser.add_argument("--engine-model", required=True)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    gateway = Client(args.base, args.model)
    engine = Client(args.engine_base, args.engine_model)
    engine.key = os.environ.get("ENGINE_API_KEY", "")  # never disclose the gateway key to a backend
    samples = {"gateway": [], "engine": []}
    failures = 0
    for i in range(103):
        order = (("gateway", gateway), ("engine", engine)) if i % 2 else (("engine", engine), ("gateway", gateway))
        for name, client in order:
            try:
                status, _, _, duration = client.call(client.payload("Antworte OK.", max_tokens=1, temperature=0, chat_template_kwargs={"enable_thinking": False}))
                if status != 200:
                    failures += 1
                elif i >= 3:
                    samples[name].append(duration * 1000)
            except Exception:  # noqa: BLE001 — bounded diagnostic; never emit remote exception contents
                failures += 1
    metrics = {}
    if all(len(v) == 100 for v in samples.values()):
        p99 = lambda values: sorted(values)[math.ceil(0.99 * len(values)) - 1]
        median = statistics.median(samples["gateway"]) - statistics.median(samples["engine"])
        tail = p99(samples["gateway"]) - p99(samples["engine"])
        metrics = {"median_overhead_ms": median, "p99_overhead_ms": tail,
                   "passed": failures == 0 and median <= 100 and tail <= 500}
    Path(args.report).write_text(json.dumps({"failures": failures, "metrics": metrics, "samples_ms": samples}, indent=2) + "\n", encoding="utf-8")
    return 0 if metrics.get("passed") else 1


if __name__ == "__main__":
    raise SystemExit(main())
