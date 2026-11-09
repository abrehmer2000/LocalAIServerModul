#!/usr/bin/env python3
"""Read-only V2 API acceptance using synthetic prompts. Never records contents/keys.

Server/host/reboot/soak evidence remains explicitly open in the resulting report.
Credentials: OPENAI_API_KEY, optional OPENAI_CA_BUNDLE. Redirects are forbidden.
"""
import argparse
import concurrent.futures
import json
import os
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, base, model, timeout=700):
        parsed = urllib.parse.urlsplit(base)
        if parsed.scheme != "https" and not (parsed.scheme == "http" and parsed.hostname in {"127.0.0.1", "::1", "localhost"}):
            raise ValueError("TLS required except on loopback")
        if parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError("Invalid base URL")
        self.base, self.model, self.timeout = base.rstrip("/"), model, timeout
        self.key = os.environ["OPENAI_API_KEY"]
        context = ssl.create_default_context(cafile=os.environ.get("OPENAI_CA_BUNDLE") or None)
        self.opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=context), NoRedirect())

    def call(self, body=None, path="/chat/completions", method=None, headers=None, key=True):
        h = {"Content-Type": "application/json", "X-Request-ID": "v2-acceptance"}
        if key:
            h["Authorization"] = "Bearer " + self.key
        h.update(headers or {})
        req = urllib.request.Request(self.base + path, data=json.dumps(body, ensure_ascii=False).encode() if body is not None else None,
                                     headers=h, method=method or ("POST" if body is not None else "GET"))
        start = time.monotonic()
        try:
            response = self.opener.open(req, timeout=self.timeout)
        except urllib.error.HTTPError as exc:
            response = exc
        with response:
            data = response.read(128 * 1024 * 1024 + 1)
            return response.code, dict(response.headers), json.loads(data.decode("utf-8")) if data else {}, time.monotonic() - start

    def payload(self, prompt="Antworte kurz mit OK.", **options):
        return {"model": self.model, "messages": [{"role": "user", "content": prompt}], **options}


def run(client):
    results = []

    def check(ids, name, operation, predicate):
        try:
            result = operation()
            ok = bool(predicate(result))
            row = {"ids": ids, "name": name, "passed": ok}
            if isinstance(result, tuple):
                row.update(status=result[0], duration_seconds=round(result[3], 3))
        except Exception as exc:  # noqa: BLE001 — report exception class only, never remote contents
            row = {"ids": ids, "name": name, "passed": False, "failure_class": type(exc).__name__}
        results.append(row)

    def complete(result):
        status, _, body, _ = result
        return status == 200 and isinstance(body.get("id"), str) and body.get("model") == client.model and isinstance(body["choices"][0]["message"]["content"], str) and body["choices"][0]["finish_reason"] in {"stop", "length"} and all(isinstance(body["usage"].get(k), int) for k in ("prompt_tokens", "completion_tokens"))

    payload = client.payload(max_tokens=32, chat_template_kwargs={"enable_thinking": False})
    check(["F-01", "F-03"], "chat_without_user_header", lambda: client.call(payload), complete)
    check(["F-02"], "no_key", lambda: client.call(payload, key=False), lambda r: r[0] == 401)
    check(["F-02"], "wrong_key", lambda: client.call(payload, headers={"Authorization": "Bearer synthetic-invalid"}), lambda r: r[0] == 401)
    for user in ("-invalid", "a:b", "x" * 65):
        check(["F-03"], "invalid_user_id", lambda user=user: client.call(payload, headers={"X-User-ID": user}), lambda r: r[0] == 400)
    check(["F-03"], "at_sign_user", lambda: client.call(payload, headers={"X-User-ID": "synthetic@example"}), complete)
    check(["F-04"], "trailing_slash", lambda: client.call(payload, path="/chat/completions/"), lambda r: not 300 <= r[0] < 400)
    check(["F-04"], "dot_segment", lambda: client.call(payload, path="/../v1/chat/completions"), lambda r: not 300 <= r[0] < 400)
    check(["F-05"], "unknown_alias", lambda: client.call({**payload, "model": "v2-unknown-alias"}), lambda r: r[0] == 404 and r[2].get("error", {}).get("code") == "model_not_found")
    check(["F-06"], "unknown_field", lambda: client.call({**payload, "invented_field": True}), lambda r: r[0] in {200, 400})
    check(["F-07"], "models", lambda: client.call(path="/models"), lambda r: r[0] == 200 and client.model in {m["id"] for m in r[2]["data"]})
    check(["F-10"], "five_output_tokens", lambda: client.call(client.payload("Zähle die Zahlen von 1 bis 200 auf.", max_tokens=5, chat_template_kwargs={"enable_thinking": False})), lambda r: complete(r) and r[2]["choices"][0]["finish_reason"] == "length")
    check(["F-41", "F-22"], "capabilities", lambda: client.call(path="/models/" + urllib.parse.quote(client.model, safe="") + "/capabilities"), lambda r: r[0] == 200 and r[2]["capabilities"]["context_per_slot"] >= 32768 and isinstance(r[2]["capabilities"]["vision"], bool))
    check(["F-42"], "request_id", lambda: client.call(payload), lambda r: r[1].get("X-Request-ID") == "v2-acceptance")
    for i in range(50):
        check(["F-32"], "sequential_" + str(i + 1), lambda: client.call(payload), complete)
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
        parallel = list(executor.map(lambda _: client.call(payload), range(2)))
    results.append({"ids": ["F-34"], "name": "two_parallel", "passed": all(complete(r) for r in parallel)})
    parseable = 0
    no_5xx = True
    for _ in range(20):
        try:
            r = client.call(client.payload('Antworte ausschließlich mit einem JSON-Objekt {"ok":true}.', response_format={"type": "json_object"}, max_tokens=128, chat_template_kwargs={"enable_thinking": False}))
            no_5xx &= r[0] < 500
            if r[0] == 200 and isinstance(json.loads(r[2]["choices"][0]["message"]["content"]), dict):
                parseable += 1
        except (ValueError, KeyError, IndexError, TypeError):
            pass
    results.append({"ids": ["F-11"], "name": "json_20", "passed": no_5xx and parseable >= 19, "parseable": parseable, "no_5xx": no_5xx})
    for required in ("F-08", "F-13", "F-14", "F-15", "F-16", "F-20", "F-21", "F-30", "F-33", "F-35", "F-37", "N-01", "N-02", "N-03", "N-12", "N-13", "N-20", "N-21", "N-23", "N-30", "N-31", "N-32", "N-33", "N-34"):
        results.append({"ids": [required], "name": "dedicated_host_engine_or_fault_injection_evidence", "passed": None})
    return {"schema_version": 1, "api_checks_passed": all(r["passed"] is not False for r in results), "customer_release": False,
            "note": "Open evidence requires the documented engine, host, reboot and 24-hour procedures.", "checks": results}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True, help="Gateway URL ending in /v1")
    parser.add_argument("--model", required=True)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    report = run(Client(args.base, args.model))
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return 0 if report["api_checks_passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
