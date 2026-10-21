"""Regression checks use temporary Git repositories and synthetic localhost HTTP."""

from contextlib import redirect_stderr, redirect_stdout
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch
from urllib import request
import zipfile


SCRIPT_DIR = Path(__file__).resolve().parent


def load_tool(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), SCRIPT_DIR / (name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


release = load_tool("release-source")
verify = load_tool("verify-deployment")
REVISION = "a" * 40
OTHER_REVISION = "b" * 40
FIXTURE_TOKEN = "synthetic-localhost-token"


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.run_git("init", "--quiet")
        for path in release.REQUIRED_FILES:
            target = self.repo / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("product fixture\n", encoding="utf-8")
        self.original_source = b"package main\nfunc main() {}\n"
        for path in ("cmd/server/main.go", "internal/server/core.go"):
            target = self.repo / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(self.original_source)
        # Include tracked private/platform files to prove the manifest excludes
        # them even when an operator accidentally tracked them in their fork.
        for path in (".env", "config.json", "auths/account.json", "data/audit/log.jsonl", ".git/config-copy", ".trellis/workflow.md", ".codex/config.toml", "codearts2api.zip"):
            target = self.repo / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("private-fixture\n", encoding="utf-8")
        (self.repo / "docs").mkdir()
        (self.repo / "docs" / "guide.md").write_text("product docs\n", encoding="utf-8")
        self.commit()

    def run_git(self, *args, data=None):
        result = subprocess.run(["git", "-C", str(self.repo), *args], input=data, check=True, capture_output=True)
        return result.stdout.decode().strip()

    def commit(self):
        self.run_git("add", "--all")
        self.run_git("-c", "user.name=Release Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture")

    def test_manifest_commit_identity_reproducibility_and_private_exclusions(self):
        # Dirty and new source files must never be mislabeled as the work commit.
        (self.repo / "cmd/server/main.go").write_text("uncommitted change\n", encoding="utf-8")
        (self.repo / "cmd/server/untracked.go").write_text("uncommitted addition\n", encoding="utf-8")
        first, commit, _ = release.create_release(self.repo, output=self.repo / "first.zip")
        second, _, _ = release.create_release(self.repo, output=self.repo / "second.zip")
        self.assertEqual(first.read_bytes(), second.read_bytes())
        with zipfile.ZipFile(first) as archive:
            names = set(archive.namelist())
            self.assertTrue(release.REQUIRED_FILES.issubset(names))
            self.assertIn("docs/guide.md", names)
            self.assertNotIn("cmd/server/untracked.go", names)
            self.assertFalse(any(path in names for path in (".env", "config.json", "codearts2api.zip")))
            self.assertFalse(any(path.startswith(("auths/", "data/", ".git/", ".trellis/", ".codex/")) for path in names))
            self.assertEqual(archive.read("cmd/server/main.go"), self.original_source)
            metadata = json.loads(archive.read("release.json"))
            self.assertEqual(metadata["source_commit"], commit)
            self.assertEqual(commit, self.run_git("rev-parse", "HEAD"))
            self.assertEqual(archive.read("release.env"), ("CA2A_BUILD_COMMIT=" + commit + "\n").encode())
            for path, digest in metadata["files"].items():
                self.assertEqual(hashlib.sha256(archive.read(path)).hexdigest(), digest)

    def test_existing_zip_is_never_overwritten(self):
        target = self.repo / "codearts2api.zip"
        original = target.read_bytes()
        with self.assertRaisesRegex(release.ReleaseError, "already exists"):
            release.create_release(self.repo, output=target)
        self.assertEqual(target.read_bytes(), original)

    def test_missing_compose_or_uncommitted_work_does_not_make_a_release(self):
        self.run_git("rm", "--quiet", "docker-compose.yml")
        self.commit()
        target = self.repo / "missing.zip"
        with self.assertRaisesRegex(release.ReleaseError, "commit the finished work first"):
            release.create_release(self.repo, output=target)
        self.assertFalse(target.exists())

    def test_manifest_rejects_a_symlink_blob(self):
        object_id = self.run_git("hash-object", "-w", "--stdin", data=b"../../auths/account.json")
        self.run_git("update-index", "--cacheinfo", "120000," + object_id + ",internal/server/panel.html")
        self.run_git("-c", "user.name=Release Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "symlink fixture")
        with self.assertRaisesRegex(release.ReleaseError, "not a regular file"):
            release.create_release(self.repo, output=self.repo / "symlink.zip")


def current_panel_fixture():
    ids = "".join('<div id="' + value + '"></div>' for value in sorted(verify.REQUIRED_IDS))
    views = "".join('<section data-view="' + value + '"></section>' for value in ("overview", "accounts", "requests", "models", "connections", "settings"))
    return ("<!doctype html><html><body>" + ids + views + "</body></html>").encode()


class HttpFixture:
    def __init__(self, prefix=""):
        self.prefix = prefix
        self.requests = []
        self.responses = {
            "/healthz": (200, b"ok"),
            "/panel/": (200, current_panel_fixture()),
            "/admin/api/overview": (200, {"build": {"known": True, "commit": REVISION}}),
            "/admin/api/requests?limit=1": (200, {"events": [], "disabled": False}),
            "/admin/api/audit?limit=1": (200, {"events": [], "disabled": False}),
            "/admin/api/models": (200, {"models": [], "accounts": [], "exclusions": [], "refresh": {"state": "idle"}}),
            "/admin/api/connections": (200, {"active": [], "recent": [], "disabled": False}),
        }
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                owner.requests.append((self.command, self.path, self.headers.get("Authorization")))
                path = self.path[len(owner.prefix):] if self.path.startswith(owner.prefix) else ""
                if path.startswith("/admin/api/") and self.headers.get("Authorization") != "Bearer " + FIXTURE_TOKEN:
                    status, body = 401, b"private body must not be logged"
                else:
                    status, body = owner.responses.get(path, (404, b"private body must not be logged"))
                if path == "/slow":
                    time.sleep(0.1)
                data = body if isinstance(body, bytes) else json.dumps(body).encode()
                self.send_response(status)
                if status == 302:
                    self.send_header("Location", "/credential-trap")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                    pass

            def log_message(self, *args):
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_port) + prefix

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=1)


class VerifierTests(unittest.TestCase):
    def setUp(self):
        self.fixture = HttpFixture(prefix="/gateway")
        self.addCleanup(self.fixture.close)

    def verify(self, key=FIXTURE_TOKEN):
        return verify.verify(self.fixture.url, REVISION, key, timeout=0.5)

    def test_current_revision_routes_prefix_and_get_only(self):
        results = self.verify()
        self.assertEqual(len(results), 7)
        self.assertTrue(all(result["ok"] for result in results), results)
        self.assertTrue(all(method == "GET" and path.startswith("/gateway/") for method, path, _ in self.fixture.requests))
        self.assertFalse(any("/v1/" in path or "refresh" in path for _, path, _ in self.fixture.requests))
        self.assertTrue(all(auth is None for _, path, auth in self.fixture.requests if "/admin/api/" not in path))

    def test_missing_routes_and_unauthorized_are_explicit_without_response_bodies(self):
        self.fixture.responses["/admin/api/models"] = (404, b"private body must not be logged")
        results = self.verify()
        failed = next(result for result in results if result["check"] == "models")
        self.assertFalse(failed["ok"])
        self.assertIn("404", failed["error"])
        self.assertNotIn("private body", str(results))
        results = self.verify(key="wrong-synthetic-token")
        self.assertTrue(all("401" in result.get("error", "") for result in results if result["check"] in {"build", "models", "connections", "requests", "audit"}))

    def test_unknown_and_wrong_running_revision_fail(self):
        for build, message in (({"known": False, "commit": ""}, "unknown"), ({"known": True, "commit": OTHER_REVISION}, "mismatch")):
            with self.subTest(build=build):
                self.fixture.responses["/admin/api/overview"] = (200, {"build": build})
                result = next(result for result in self.verify() if result["check"] == "build")
                self.assertFalse(result["ok"])
                self.assertIn(message, result["error"])

    def test_old_panel_and_removed_content_fail(self):
        for html in (b"<html>old panel</html>", current_panel_fixture() + b'<div id="sidebarBuild"></div>'):
            with self.subTest(html=html):
                self.fixture.responses["/panel/"] = (200, html)
                result = next(result for result in self.verify() if result["check"] == "console")
                self.assertFalse(result["ok"])

    def test_malformed_metadata_and_disabled_state_are_honest(self):
        self.fixture.responses["/admin/api/models"] = (200, b"<html>proxy page</html>")
        self.fixture.responses["/admin/api/connections"] = (200, {"active": [], "recent": [], "disabled": True})
        results = {result["check"]: result for result in self.verify()}
        self.assertFalse(results["models"]["ok"])
        self.assertTrue(results["connections"]["disabled"])

    def test_redirect_is_not_followed_and_no_key_reaches_the_target(self):
        self.fixture.responses["/admin/api/overview"] = (302, b"")
        result = next(result for result in self.verify() if result["check"] == "build")
        self.assertFalse(result["ok"])
        self.assertIn("Redirect refused", result["error"])
        self.assertFalse(any("credential-trap" in path for _, path, _ in self.fixture.requests))

    def test_empty_key_invalid_revision_and_credential_url_stop_before_network(self):
        for url, revision, key in ((self.fixture.url, REVISION, ""), (self.fixture.url, "unknown", FIXTURE_TOKEN), ("https://user:password@example.invalid", REVISION, FIXTURE_TOKEN)):
            with self.subTest(url=url, revision=revision):
                with self.assertRaises(verify.VerificationError):
                    verify.verify(url, revision, key)
        self.assertEqual(self.fixture.requests, [])

    def test_bounded_body_and_socket_timeout(self):
        opener = request.build_opener(verify.NoRedirect())
        self.fixture.responses["/oversize"] = (200, b"x" * 512)
        with patch.object(verify, "MAX_BODY_BYTES", 128):
            with self.assertRaisesRegex(verify.VerificationError, "bounded"):
                verify.get(opener, self.fixture.url + "/oversize", None, 0.5)
        with self.assertRaisesRegex(verify.VerificationError, "timed out"):
            verify.get(opener, self.fixture.url + "/slow", None, 0.02)

    def test_cli_status_does_not_print_key_or_error_body(self):
        self.fixture.responses["/admin/api/models"] = (404, b"private body must not be logged")
        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, {"CA2A_API_KEY": FIXTURE_TOKEN}), redirect_stdout(stdout), redirect_stderr(stderr):
            code = verify.main(["--url", self.fixture.url, "--expected-commit", REVISION])
        self.assertEqual(code, 1)
        self.assertIn("FAIL models", stdout.getvalue())
        self.assertNotIn(FIXTURE_TOKEN, stdout.getvalue() + stderr.getvalue())
        self.assertNotIn("private body", stdout.getvalue() + stderr.getvalue())

    def test_checked_in_panel_has_current_markers_and_removals(self):
        verify.check_panel((SCRIPT_DIR.parent / "internal/server/panel.html").read_bytes())


if __name__ == "__main__":
    unittest.main()
