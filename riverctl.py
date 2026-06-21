#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["requests"]
# ///
"""riverctl — minimal "rivers-as-code" loop POC.

Desired state  : rivers/*.json   (one river definition per file)
Actual state   : a state file (JSON) mapping logical name -> {cross_id, last_applied_hash}
Backend        : Rivery public API (RiveryClient)

Commands:
  plan     show what apply would do (create / update / no-op) per river file
  apply    reconcile: create new rivers, deep-merge-update changed ones; write state
  destroy  delete every river tracked in state; clear state
  list     list rivers currently in the target account/env (read-only smoke test)

State dir defaults to ./.state (override with RIVERY_IAC_STATE_DIR). This stands in
for the future Bitbucket-backed state repo — swap the read/write of state.json for a
git clone/commit/push and the loop is unchanged.

Usage:
  RIVERY_IAC_ENV_FILE=/path/to/integration.local.env uv run riverctl.py plan
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path
from typing import Any

from rivery_client import RiveryClient, RiveryAPIError

RIVERS_DIR = Path(__file__).parent / "rivers"


def _state_dir() -> Path:
    d = Path(os.environ.get("RIVERY_IAC_STATE_DIR", Path(__file__).parent / ".state"))
    d.mkdir(parents=True, exist_ok=True)
    return d


def _state_file() -> Path:
    return _state_dir() / "state.json"


def _load_state() -> dict[str, Any]:
    f = _state_file()
    return json.loads(f.read_text()) if f.exists() else {}


def _save_state(state: dict[str, Any]) -> None:
    _state_file().write_text(json.dumps(state, indent=2, sort_keys=True))


def _hash(config: dict[str, Any]) -> str:
    return hashlib.sha256(json.dumps(config, sort_keys=True).encode()).hexdigest()[:12]


def _load_desired() -> dict[str, dict[str, Any]]:
    out: dict[str, dict[str, Any]] = {}
    for f in sorted(RIVERS_DIR.glob("*.json")):
        cfg = json.loads(f.read_text())
        name = cfg.get("name") or f.stem
        out[name] = cfg
    return out


def cmd_list(client: RiveryClient, _: argparse.Namespace) -> int:
    rivers = client.list_rivers()
    print(
        f"{len(rivers)} river(s) in account {client.account_id} / env {client.env_id}:"
    )
    for r in rivers[:25]:
        print(f"  {r.get('id')}  {r.get('title')}")
    if len(rivers) > 25:
        print(f"  … +{len(rivers) - 25} more")
    return 0


def cmd_plan(client: RiveryClient, _: argparse.Namespace) -> int:
    desired = _load_desired()
    state = _load_state()
    for name, cfg in desired.items():
        tracked = state.get(name)
        h = _hash(cfg)
        if not tracked:
            print(f"  + CREATE  {name}")
        elif tracked.get("last_applied_hash") != h:
            print(f"  ~ UPDATE  {name}  (cross_id={tracked.get('cross_id')})")
        else:
            print(f"  = NO-OP   {name}  (cross_id={tracked.get('cross_id')})")
    orphans = set(state) - set(desired)
    for name in sorted(orphans):
        print(
            f"  - DESTROY {name}  (cross_id={state[name].get('cross_id')}) — no longer in rivers/"
        )
    return 0


def cmd_apply(client: RiveryClient, _: argparse.Namespace) -> int:
    desired = _load_desired()
    state = _load_state()
    for name, cfg in desired.items():
        tracked = state.get(name)
        h = _hash(cfg)
        if not tracked:
            created = client.create_river(cfg)
            cid = created.get("id")
            state[name] = {"cross_id": cid, "last_applied_hash": h}
            _save_state(state)
            print(f"  + CREATED {name}  cross_id={cid}")
        elif tracked.get("last_applied_hash") != h:
            cid = tracked["cross_id"]
            client.update_river(cid, cfg)
            state[name]["last_applied_hash"] = h
            _save_state(state)
            print(f"  ~ UPDATED {name}  cross_id={cid}")
        else:
            print(f"  = NO-OP   {name}  cross_id={tracked.get('cross_id')}")
    return 0


def cmd_destroy(client: RiveryClient, _: argparse.Namespace) -> int:
    state = _load_state()
    for name, rec in list(state.items()):
        cid = rec.get("cross_id")
        try:
            client.delete_river(cid)
            print(f"  - DELETED {name}  cross_id={cid}")
        except RiveryAPIError as e:
            print(f"  ! FAILED  {name}  cross_id={cid}: {e}")
        del state[name]
        _save_state(state)
    return 0


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description="rivers-as-code POC")
    sub = p.add_subparsers(dest="cmd", required=True)
    for name in ("plan", "apply", "destroy", "list"):
        sub.add_parser(name)
    args = p.parse_args(argv)
    try:
        client = RiveryClient()
    except ValueError as e:
        print(f"credential error: {e}", file=sys.stderr)
        return 2
    print(f"# state: {_state_file()}")
    return {
        "plan": cmd_plan,
        "apply": cmd_apply,
        "destroy": cmd_destroy,
        "list": cmd_list,
    }[args.cmd](client, args)


if __name__ == "__main__":
    raise SystemExit(main())
