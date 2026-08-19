#!/usr/bin/env python3
"""Private Statsig signer relay using FlareSolverr for Cloudflare clearance."""
from __future__ import annotations

import json
import os
import threading
import time
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


def env(name: str, default: str) -> str:
    return os.environ.get(name, "").strip() or default


BIND = env("STATSIG_RELAY_BIND", "127.0.0.1")
PORT = int(env("STATSIG_RELAY_PORT", "8788"))
FLARESOLVERR_URL = env("STATSIG_RELAY_FLARESOLVERR_URL", "http://flaresolverr:8191/v1")
UPSTREAM_URL = env("STATSIG_RELAY_UPSTREAM_URL", "https://grok.wodf.de/sign")
UPSTREAM_TIMEOUT = float(env("STATSIG_RELAY_UPSTREAM_TIMEOUT", "30"))
SOLVE_TIMEOUT_MS = int(env("STATSIG_RELAY_SOLVE_TIMEOUT_MS", "120000"))
CLEARANCE_TTL = float(env("STATSIG_RELAY_CLEARANCE_TTL", "1800"))
MAX_BODY = 4 * 1024 * 1024
MAX_META = 4 * 1024 * 1024
_lock = threading.Lock()
_clearance: dict[str, Any] | None = None


class RelayError(Exception):
    def __init__(self, status: int, message: str):
        super().__init__(message)
        self.status = status


def json_post(url: str, payload: dict[str, Any], timeout: float) -> tuple[int, dict[str, Any]]:
    body = json.dumps(payload, separators=(",", ":")).encode()
    request = Request(url, data=body, method="POST", headers={"Content-Type": "application/json"})
    try:
        with urlopen(request, timeout=timeout) as response:
            raw, status = response.read(MAX_BODY + 1), response.status
    except HTTPError as exc:
        raw, status = exc.read(MAX_BODY + 1), exc.code
    except (URLError, TimeoutError, OSError) as exc:
        raise RelayError(502, "签名上游连接失败") from exc
    if len(raw) > MAX_BODY:
        raise RelayError(502, "签名上游响应过大")
    try:
        result = json.loads(raw.decode())
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RelayError(502, "签名上游响应不是 JSON") from exc
    if not isinstance(result, dict):
        raise RelayError(502, "签名上游响应格式无效")
    return status, result


def get_clearance(force: bool = False) -> dict[str, Any]:
    global _clearance
    now = time.time()
    with _lock:
        if not force and _clearance and _clearance["expires_at"] > now:
            return dict(_clearance)
        status, result = json_post(
            FLARESOLVERR_URL,
            {"cmd": "request.get", "url": UPSTREAM_URL, "maxTimeout": SOLVE_TIMEOUT_MS},
            max(10, SOLVE_TIMEOUT_MS / 1000),
        )
        solution = result.get("solution") if status == 200 and result.get("status") == "ok" else None
        if not isinstance(solution, dict):
            raise RelayError(502, "FlareSolverr 未能获取有效通行凭据")
        user_agent = str(solution.get("userAgent") or "").strip()
        cookies = solution.get("cookies")
        pairs = [f"{item['name']}={item['value']}" for item in cookies or [] if isinstance(item, dict) and item.get("name") and item.get("value") is not None]
        if not user_agent or not pairs:
            raise RelayError(502, "FlareSolverr 响应缺少 User-Agent 或 Cookie")
        _clearance = {"user_agent": user_agent, "cookie": "; ".join(pairs), "expires_at": now + CLEARANCE_TTL}
        return dict(_clearance)


def upstream_sign(method: str, path: str, meta: str, clearance: dict[str, Any]) -> tuple[int, dict[str, Any]]:
    body = json.dumps({"method": method, "path": path, "environment": {"metaContent": meta}}, separators=(",", ":")).encode()
    request = Request(UPSTREAM_URL, data=body, method="POST", headers={"Content-Type": "application/json", "Accept": "application/json", "User-Agent": clearance["user_agent"], "Cookie": clearance["cookie"]})
    try:
        with urlopen(request, timeout=UPSTREAM_TIMEOUT) as response:
            raw = response.read(16 * 1024)
            return response.status, json.loads(raw.decode())
    except HTTPError as exc:
        try:
            result = json.loads(exc.read(16 * 1024).decode())
        except (UnicodeDecodeError, json.JSONDecodeError):
            result = {}
        return exc.code, result if isinstance(result, dict) else {}
    except (URLError, TimeoutError, OSError) as exc:
        raise RelayError(502, "签名上游连接失败") from exc


def sign(method: str, path: str, meta: str) -> str:
    status, result = upstream_sign(method, path, meta, get_clearance())
    if status == HTTPStatus.FORBIDDEN:
        status, result = upstream_sign(method, path, meta, get_clearance(True))
    if status < 200 or status >= 300:
        raise RelayError(502, "签名上游拒绝请求")
    value = result.get("x-statsig-id")
    if not isinstance(value, str) or not 32 <= len(value) <= 256:
        raise RelayError(502, "签名上游返回无效 x-statsig-id")
    return value


class Handler(BaseHTTPRequestHandler):
    server_version = "grok2api-statsig-relay/1"

    def log_message(self, _format: str, *_args: Any) -> None:
        pass

    def respond(self, status: int, value: dict[str, Any]) -> None:
        encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/sign":
            self.respond(404, {"error": "not_found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "-1"))
            if length < 0 or length > MAX_BODY:
                raise RelayError(413, "request_too_large")
            payload = json.loads(self.rfile.read(length).decode())
            method = str(payload.get("method") or "").strip().upper()
            path = str(payload.get("path") or "").strip()
            environment = payload.get("environment")
            meta = environment.get("metaContent") if isinstance(environment, dict) else None
            if method not in {"GET", "POST", "PUT", "PATCH", "DELETE"} or not path.startswith("/"):
                raise RelayError(400, "请求 method/path 无效")
            if not isinstance(meta, str) or not meta or len(meta.encode()) > MAX_META:
                raise RelayError(400, "请求 metaContent 无效")
            self.respond(200, {"x-statsig-id": sign(method, path, meta)})
        except RelayError as exc:
            self.respond(exc.status, {"error": str(exc)})
        except (UnicodeDecodeError, json.JSONDecodeError, AttributeError, TypeError, ValueError):
            self.respond(400, {"error": "invalid_json"})
        except Exception:
            self.respond(500, {"error": "internal_error"})


def main() -> None:
    ThreadingHTTPServer((BIND, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
