#!/usr/bin/env python3
"""Deterministic sample agent adapter reading JSON prompt from stdin."""

import json
import os
import sys
import time
import urllib.error
import urllib.request


def post_json(url, payload, headers):
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json", **headers},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=2.0) as resp:
            return resp.status, resp.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8")


def main():
    raw = sys.stdin.readline()
    if not raw:
        sys.exit(2)
    ctx = json.loads(raw)
    run_id = ctx["run_id"]
    base_url = os.environ["TOOL_API_BASE_URL"]
    mode = os.environ.get("AGENT_MODE", "idempotent")

    if mode == "hang":
        time.sleep(30)
        sys.exit(0)

    if mode == "no_op":
        sys.exit(0)

    if mode in ("idempotent", "buggy_duplicate", "no_retry", "loop"):
        op_id = f"op-{run_id}"
        max_attempts = 1 if mode == "no_retry" else (5 if mode == "loop" else 2)
        for attempt in range(max_attempts):
            headers = {"X-Operation-ID": op_id}
            if mode == "idempotent":
                headers["X-Idempotency-Key"] = op_id
            elif mode == "buggy_duplicate":
                headers["X-Idempotency-Key"] = f"{op_id}-attempt-{attempt}"

            status, _ = post_json(
                f"{base_url}/api/v1/orders",
                {"run_id": run_id, "customer_id": "cust_123", "amount_cents": 4900},
                headers,
            )
            if status in (200, 201) and mode != "loop":
                sys.exit(0)
        sys.exit(0 if mode == "no_retry" else 1)

    if mode in ("saga_compensated", "saga_broken"):
        s1, _ = post_json(f"{base_url}/api/v1/inventory/reserve", {"run_id": run_id}, {})
        if s1 not in (200, 201):
            sys.exit(1)
        s2, _ = post_json(f"{base_url}/api/v1/payments/charge", {"run_id": run_id}, {})
        if s2 not in (200, 201):
            if mode == "saga_compensated":
                post_json(f"{base_url}/api/v1/inventory/release", {"run_id": run_id}, {})
            sys.exit(0)
        sys.exit(0)

    sys.exit(2)


if __name__ == "__main__":
    main()
