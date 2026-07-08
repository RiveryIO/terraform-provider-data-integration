"""Unit tests for the pure helpers in rivery_client (no network)."""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from rivery_client import _deep_merge, _strip_forbidden, normalize_river  # noqa: E402


def test_deep_merge_leaf_wins() -> None:
    base = {"a": 1, "nested": {"x": 1, "y": 2}}
    patch = {"a": 2, "nested": {"y": 9}}
    assert _deep_merge(base, patch) == {"a": 2, "nested": {"x": 1, "y": 9}}


def test_deep_merge_lists_replace_wholesale() -> None:
    base = {"schedulers": [{"id": 1}, {"id": 2}]}
    patch = {"schedulers": [{"id": 3}]}
    assert _deep_merge(base, patch) == {"schedulers": [{"id": 3}]}


def test_strip_forbidden_removes_server_fields() -> None:
    cfg = {"name": "x", "id": "1", "cross_id": "2", "account_id": "3", "type": "logic"}
    out = _strip_forbidden(cfg)
    assert out == {"name": "x", "type": "logic"}


def test_normalize_river_list_shape() -> None:
    out = normalize_river({"river_cross_id": "abc", "name": "Flow A"})
    assert out["id"] == "abc"
    assert out["title"] == "Flow A"


def test_normalize_river_detail_shape() -> None:
    out = normalize_river({"cross_id": "def", "name": "Flow B"})
    assert out["id"] == "def"
    assert out["title"] == "Flow B"


def test_normalize_river_is_idempotent() -> None:
    once = normalize_river({"river_cross_id": "abc", "name": "Flow A"})
    twice = normalize_river(once)
    assert once == twice
