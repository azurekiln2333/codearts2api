#!/usr/bin/env python3
"""Verify deployed console/routes/revision using GET only and an environment key."""

from __future__ import annotations

import argparse
import http.client
from html.parser import HTMLParser
import json
import os
import re
import socket
import sys
from urllib import error, parse, request


MAX_BODY_BYTES = 20 * 1024 * 1024
COMMIT_PATTERN = re.compile(r"(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})\Z")
REQUIRED_IDS = frozenset({
    "reqBody", "btnModelsRefresh", "modelRefreshStatus", "modelSourceDetails",
    "modelExclusionBody", "connActiveBody", "connRecentBody",
})
REMOVED_IDS = frozenset({"sidebarBuild", "subTitle", "btnAddAccount2"})


class VerificationError(Exception):
    pass


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never forward the service key to a redirected host/path.
        return None


class PanelInspector(HTMLParser):
    def __init__(self):
        super().__init__()
        self.ids = set()
        self.views = set()
        self.classes = set()
        self.text = []

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if values.get("id"):
            self.ids.add(values["id"])
        if values.get("data-view"):
            self.views.add(values["data-view"])
        self.classes.update((values.get("class") or "").split())

    def handle_data(self, data):
        self.text.append(data)


def base_url(value: str) -> str:
    if any(ord(char) < 33 for char in value):
        raise VerificationError("URL contains whitespace/control characters.")
    parsed = parse.urlsplit(value)
    if parsed.scheme not in {"https", "http"} or not parsed.hostname:
        raise VerificationError("URL must use http:// or https://.")
    if parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment or "\\" in value:
        raise VerificationError("Use a gateway base URL without credentials, query or fragment.")
    return value.rstrip("/")


def get(opener, url: str, key: str | None, timeout: float) -> bytes:
    headers = {"Accept": "application/json, text/html, text/plain", "Cache-Control": "no-cache"}
    if key:
        headers["Authorization"] = "Bearer " + key
    try:
        with opener.open(request.Request(url, headers=headers, method="GET"), timeout=timeout) as response:
            data = response.read(MAX_BODY_BYTES + 1)
            if len(data) > MAX_BODY_BYTES:
                raise VerificationError("Response exceeded the bounded metadata limit.")
            return data
    except error.HTTPError as exc:
        exc.close()
        if exc.code == 401:
            raise VerificationError("Authentication failed (401); check the API key environment variable.") from None
        if exc.code == 404:
            raise VerificationError("Route missing (404); rebuild/recreate the expected service.") from None
        if 300 <= exc.code < 400:
            raise VerificationError("Redirect refused; use the final gateway base URL.") from None
        raise VerificationError("Unexpected HTTP status " + str(exc.code) + ".") from None
    except (error.URLError, TimeoutError, socket.timeout, OSError, http.client.HTTPException):
        # Do not echo a provider body, headers or a credential-bearing URL.
        raise VerificationError("Network/TLS request failed or timed out.") from None


def decode_json(data: bytes):
    try:
        value = json.loads(data)
    except (ValueError, UnicodeError):
        raise VerificationError("Route did not return valid JSON metadata.") from None
    if not isinstance(value, dict):
        raise VerificationError("Route did not return a JSON object.")
    return value


def check_panel(data: bytes):
    try:
        html = data.decode("utf-8")
    except UnicodeError:
        raise VerificationError("Console HTML is not UTF-8.") from None
    panel = PanelInspector()
    panel.feed(html)
    if not REQUIRED_IDS.issubset(panel.ids) or not {"overview", "accounts", "requests", "models", "connections", "settings"}.issubset(panel.views):
        raise VerificationError("Console is missing the model-pull or connection-log interface.")
    if REMOVED_IDS.intersection(panel.ids) or {"sidebar-footer", "shortcut-list"}.intersection(panel.classes) or "工作台快捷入口" in "".join(panel.text):
        raise VerificationError("Console still contains the removed sidebar/header/shortcut content.")


def check_health(data: bytes):
    if data.strip() != b"ok":
        raise VerificationError("Health route did not return ok.")


def check_build(data: bytes, expected: str):
    value = decode_json(data)
    build = value.get("build")
    if not isinstance(build, dict) or build.get("known") is not True:
        raise VerificationError("Running build identity is unknown; inject the source commit at image build.")
    actual = build.get("commit")
    if not isinstance(actual, str) or not COMMIT_PATTERN.fullmatch(actual):
        raise VerificationError("Running build has an invalid full commit identity.")
    if actual.lower() != expected.lower():
        raise VerificationError("Running revision mismatch: expected " + expected[:12] + ", got " + actual[:12] + ".")


def check_requests(data: bytes):
    value = decode_json(data)
    if not isinstance(value.get("events"), list) or not isinstance(value.get("disabled"), bool):
        raise VerificationError("Request history route returned an incompatible metadata schema.")
    return value["disabled"]


def check_audit(data: bytes):
    value = decode_json(data)
    stats = value.get("audit")
    if not isinstance(stats, dict) or not isinstance(stats.get("enabled"), bool):
        raise VerificationError("Audit statistics route returned an incompatible metadata schema.")
    return not stats["enabled"]


def check_models(data: bytes):
    value = decode_json(data)
    if any(not isinstance(value.get(name), list) for name in ("models", "accounts", "exclusions")) or not isinstance(value.get("refresh"), dict):
        raise VerificationError("Model-catalog route returned an incompatible metadata schema.")
    if value["refresh"].get("state") not in {"idle", "running", "completed", "partial", "failed"}:
        raise VerificationError("Model-catalog route returned an invalid refresh state.")


def check_connections(data: bytes):
    value = decode_json(data)
    if any(not isinstance(value.get(name), list) for name in ("active", "recent")) or not isinstance(value.get("disabled"), bool):
        raise VerificationError("Connection route returned an incompatible metadata schema.")
    return value["disabled"]


def verify(url: str, expected: str, key: str, timeout: float = 5.0):
    base = base_url(url)
    if not COMMIT_PATTERN.fullmatch(expected):
        raise VerificationError("Expected commit must be the full 40/64-character Git revision.")
    if not key or any(ord(char) < 33 or ord(char) > 126 for char in key):
        raise VerificationError("API key environment variable is empty or invalid.")
    if not 0 < timeout <= 60:
        raise VerificationError("Timeout must be greater than zero and at most 60 seconds.")
    opener = request.build_opener(NoRedirect())
    results = []
    checks = [
        ("health", "/healthz", False, check_health),
        ("console", "/panel/", False, check_panel),
        ("build", "/admin/api/overview", True, lambda data: check_build(data, expected)),
        ("requests", "/admin/api/requests?limit=1", True, check_requests),
        ("audit", "/admin/api/audit", True, check_audit),
        ("models", "/admin/api/models", True, check_models),
        ("connections", "/admin/api/connections", True, check_connections),
    ]
    for name, path, authenticated, validate in checks:
        try:
            disabled = validate(get(opener, base + path, key if authenticated else None, timeout))
            results.append({"check": name, "ok": True, "disabled": disabled is True})
        except VerificationError as exc:
            results.append({"check": name, "ok": False, "error": str(exc)})
    return results


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True, help="Gateway base URL, including any reverse-proxy prefix.")
    parser.add_argument("--expected-commit", required=True, help="Full revision from Git/release.env.")
    parser.add_argument("--key-env", default="CA2A_API_KEY", help="Environment variable name, never the key itself.")
    parser.add_argument("--timeout", type=float, default=5.0, help="Socket timeout per GET in seconds (default: 5).")
    args = parser.parse_args(argv)
    try:
        results = verify(args.url, args.expected_commit, os.environ.get(args.key_env, ""), args.timeout)
    except (VerificationError, ValueError) as exc:
        print("Verification failed: " + str(exc), file=sys.stderr)
        return 1
    for result in results:
        message = "OK" if result["ok"] else "FAIL"
        print(message + " " + result["check"] + (": " + result["error"] if "error" in result else ""))
        if result.get("disabled"):
            print("WARN " + result["check"] + ": auditing/tracking is disabled by runtime configuration.")
    passed = all(result["ok"] for result in results)
    if passed:
        print("Verified console/routes and expected revision. No chat or refresh operation was sent.")
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
