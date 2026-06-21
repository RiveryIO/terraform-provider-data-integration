"""Slim Rivery public-API client for the rivers-as-code POC.

Ports the load-bearing patterns from the BDI plugin's `RiveryAPI`
(tools/rivery_api.py): bearer-token auth, account/env-scoped paths,
read-modify-write edit via deep-merge, forbidden-field stripping,
list/detail field normalization, and typed errors + 5xx retry.

Credentials are resolved in order:
  1. env vars  DATA_INTEGRATION_API_TOKEN / _URL / _ACCOUNT_ID / _ENVIRONMENT_ID
  2. an env file (KEY=VALUE) pointed to by RIVERY_IAC_ENV_FILE, using the
     api-service integration keys: CLI_TOKEN / API_URL / ACCOUNT_ID / ENVIRONMENT_ID

No secret is ever printed.
"""

from __future__ import annotations

import os
import time
from pathlib import Path
from typing import Any

import requests

DEFAULT_TIMEOUT_S = 60
DEFAULT_MAX_RETRIES = 3
DEFAULT_BACKOFF_S = 1.0

# Stripped before every write — the API rejects these as extra_forbidden.
# Lifted verbatim from BDI _RIVER_WRITE_FORBIDDEN_FIELDS.
_WRITE_FORBIDDEN_FIELDS: tuple[str, ...] = (
    "title",
    "id",
    "cross_id",
    "_id",
    "account_id",
    "environment_name",
    "group_name",
)


class RiveryAPIError(Exception):
    def __init__(self, status_code: int, message: str, details: Any = None) -> None:
        self.status_code = status_code
        self.message = message
        self.details = details
        super().__init__(f"API error {status_code}: {message}")


class AuthError(RiveryAPIError):
    """401 / 403."""


class NotFoundError(RiveryAPIError):
    """404."""


class ValidationError(RiveryAPIError):
    """422 / 400 validation."""


_STATUS_TO_EXCEPTION: dict[int, type[RiveryAPIError]] = {
    401: AuthError,
    403: AuthError,
    404: NotFoundError,
    422: ValidationError,
}


def _read_env_file(path: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    if not path.exists():
        return out
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        out[k.strip()] = v.strip().strip('"').strip("'")
    return out


def _deep_merge(base: dict[str, Any], patch: dict[str, Any]) -> dict[str, Any]:
    """Recursive merge; patch wins on leaves, lists/scalars replace wholesale.

    Matches BDI semantics: schedulers / logic_steps are full-replace fields.
    """
    out = dict(base)
    for k, v in patch.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge(out[k], v)
        else:
            out[k] = v
    return out


def normalize_river(raw: dict[str, Any]) -> dict[str, Any]:
    """Add stable `id`/`title` aliases across list vs detail responses."""
    out = dict(raw)
    out["id"] = (
        raw.get("id")
        or raw.get("river_cross_id")
        or raw.get("cross_id")
        or raw.get("_id")
    )
    out["title"] = raw.get("title") or raw.get("name")
    return out


def _strip_forbidden(config: dict[str, Any]) -> dict[str, Any]:
    return {k: v for k, v in config.items() if k not in _WRITE_FORBIDDEN_FIELDS}


class RiveryClient:
    def __init__(
        self,
        *,
        token: str | None = None,
        base_url: str | None = None,
        account_id: str | None = None,
        env_id: str | None = None,
        max_retries: int = DEFAULT_MAX_RETRIES,
        backoff_s: float = DEFAULT_BACKOFF_S,
        timeout: int = DEFAULT_TIMEOUT_S,
    ) -> None:
        env_file = os.environ.get("RIVERY_IAC_ENV_FILE")
        filevals = _read_env_file(Path(env_file)) if env_file else {}
        self.token = (
            token
            or os.environ.get("DATA_INTEGRATION_API_TOKEN")
            or filevals.get("CLI_TOKEN")
        )
        self.base_url = (
            base_url
            or os.environ.get("DATA_INTEGRATION_API_URL")
            or filevals.get("API_URL")
            or ""
        ).rstrip("/")
        self.account_id = (
            account_id
            or os.environ.get("DATA_INTEGRATION_ACCOUNT_ID")
            or filevals.get("ACCOUNT_ID")
        )
        self.env_id = (
            env_id
            or os.environ.get("DATA_INTEGRATION_ENVIRONMENT_ID")
            or filevals.get("ENVIRONMENT_ID")
        )
        self.max_retries = max_retries
        self.backoff_s = backoff_s
        self.timeout = timeout
        missing = [
            n
            for n, v in (
                ("token", self.token),
                ("base_url", self.base_url),
                ("account_id", self.account_id),
                ("env_id", self.env_id),
            )
            if not v
        ]
        if missing:
            raise ValueError(f"Missing credentials: {', '.join(missing)}")

    @property
    def headers(self) -> dict[str, str]:
        return {
            "Authorization": f"Bearer {self.token}",
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "rivery-iac-poc/0.1.0",
            "X-Boomi-Plugin": f"rivery-iac-poc/0.1.0 (account={self.account_id})",
        }

    def _scoped(self, endpoint: str) -> str:
        return (
            f"{self.base_url}/v1/accounts/{self.account_id}"
            f"/environments/{self.env_id}{endpoint}"
        )

    def _request(
        self,
        method: str,
        endpoint: str,
        *,
        data: dict[str, Any] | None = None,
        params: dict[str, Any] | None = None,
    ) -> Any:
        url = self._scoped(endpoint)
        attempt = 0
        while True:
            try:
                r = requests.request(
                    method,
                    url,
                    headers=self.headers,
                    json=data,
                    params=params,
                    timeout=self.timeout,
                )
            except requests.RequestException as e:
                attempt += 1
                if attempt > self.max_retries:
                    raise RiveryAPIError(0, f"Request error: {e}") from e
                time.sleep(self.backoff_s * attempt)
                continue
            if 200 <= r.status_code < 300:
                return r.json() if r.text else {}
            if r.status_code >= 500:
                attempt += 1
                if attempt > self.max_retries:
                    raise RiveryAPIError(
                        r.status_code, f"5xx after retries: {r.text[:200]}"
                    )
                time.sleep(self.backoff_s * attempt)
                continue
            details: Any = r.text
            try:
                details = r.json()
            except ValueError:
                pass
            exc = _STATUS_TO_EXCEPTION.get(r.status_code, RiveryAPIError)
            raise exc(r.status_code, f"{method} {endpoint}", details)

    # -- river operations ----------------------------------------------------

    def list_rivers(self) -> list[dict[str, Any]]:
        resp = self._request("GET", "/rivers")
        if isinstance(resp, dict):
            items = resp.get("items", resp.get("data", []))
        else:
            items = resp
        return [normalize_river(x) for x in (items or [])]

    def get_river(self, cross_id: str) -> dict[str, Any]:
        return normalize_river(self._request("GET", f"/rivers/{cross_id}"))

    def create_river(self, config: dict[str, Any]) -> dict[str, Any]:
        return normalize_river(
            self._request("POST", "/rivers", data=_strip_forbidden(config))
        )

    def update_river(self, cross_id: str, patch: dict[str, Any]) -> dict[str, Any]:
        """Read-modify-write: GET current, deep-merge patch, PUT full body."""
        current = self._request("GET", f"/rivers/{cross_id}")
        merged = _strip_forbidden(_deep_merge(current, patch))
        return normalize_river(self._request("PUT", f"/rivers/{cross_id}", data=merged))

    def delete_river(self, cross_id: str) -> None:
        self._request("DELETE", f"/rivers/{cross_id}")
