#!/usr/bin/env python3
"""Trigger a data flow run and poll it to a terminal status.

Standard library only — no pip install. Exits 0 on success, 1 on a failed run,
and non-zero with a message on an HTTP error or timeout, so it drops into CI
as-is.

Usage:
    export BOOMI_API_URL=https://api.rivery.io
    export BOOMI_ACCOUNT_ID=...
    export BOOMI_ENVIRONMENT_ID=...
    export BOOMI_DATA_FLOW_ID=$(terraform output -raw discovery_driven_data_flow_id)
    export BOOMI_API_TOKEN=...
    python3 run.py

See the "Running data flows" guide for the endpoints and status values this
implements.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

# The only two statuses that mean "keep waiting". Everything else is terminal —
# treating unknown values as non-terminal makes a failed run hang until timeout.
NON_TERMINAL = {"pending", "running"}
SUCCESS = {"succeeded", "partially succeeded"}


def _call(method, url, token):
    req = urllib.request.Request(
        url, method=method, headers={"Authorization": f"Bearer {token}"}
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {url} -> HTTP {e.code}: {e.read().decode()[:400]}")


def run_and_wait(api, account, env, river, token, timeout=1800, interval=10):
    """Trigger one run of `river` and block until it reaches a terminal status.

    Returns (run_id, status). Exits non-zero if the run has not finished within
    `timeout` seconds.
    """
    base = f"{api}/v1/accounts/{account}/environments/{env}/rivers/{river}"

    # POST /run returns a run GROUP: one flow can produce several runs when it
    # has sub-flows. A single-flow run is the first entry.
    started = _call("POST", f"{base}/run", token)
    run_id = started["runs"][0]["run_id"]
    print(f"run_id={run_id} run_group_id={started['run_group_id']}", flush=True)

    deadline = time.time() + timeout
    status = None
    while time.time() < deadline:
        status = _call("GET", f"{base}/runs/{run_id}", token).get("status")
        if status not in NON_TERMINAL:
            print(f"terminal status: {status}", flush=True)
            return run_id, status
        time.sleep(interval)

    sys.exit(f"run {run_id} still '{status}' after {timeout}s")


def _env(name):
    value = os.environ.get(name)
    if not value:
        sys.exit(f"missing required environment variable: {name}")
    return value


if __name__ == "__main__":
    _, final_status = run_and_wait(
        _env("BOOMI_API_URL"),
        _env("BOOMI_ACCOUNT_ID"),
        _env("BOOMI_ENVIRONMENT_ID"),
        _env("BOOMI_DATA_FLOW_ID"),
        _env("BOOMI_API_TOKEN"),
    )
    # A succeeded run means the platform reported success — not that the rows
    # you expected are in the target. Assert on row counts separately.
    sys.exit(0 if final_status in SUCCESS else 1)
