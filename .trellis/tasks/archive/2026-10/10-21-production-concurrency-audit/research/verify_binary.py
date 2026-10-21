"""Build and run the real binary against isolated local fixtures only."""
from __future__ import annotations

from datetime import datetime, timezone
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

import requests


def main() -> None:
    root = next(p for p in Path(__file__).resolve().parents if (p / "go.mod").is_file())
    go = Path("D:/AppData/Local/Temp/codearts2api-toolchain-go1.27.3/go/bin/go.exe")
    isolated_env = {k: v for k, v in os.environ.items() if not k.startswith("CA2A_")}
    isolated_env["CGO_ENABLED"] = "0"
    with tempfile.TemporaryDirectory(prefix="codearts2api-binary-check-") as temporary:
        work = Path(temporary)
        executable = work / "gateway.exe"
        built_at = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        commit = "local-validation-fixture"
        subprocess.run([str(go), "build", "-ldflags", f"-X codearts2api/internal/server.BuildCommit={commit} -X codearts2api/internal/server.BuildTime={built_at}", "-o", str(executable), "./cmd/server"], cwd=root, env=isolated_env, check=True, timeout=90)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        auths = work / "empty-auths"
        auths.mkdir()
        config = work / "fixture.json"
        config.write_text(json.dumps({"listen": f"127.0.0.1:{port}", "api_key": "fixture-local-api-key", "auth_dir": str(auths), "state_file": str(work / "data/state.json"), "watch": {"enabled": False}, "max_concurrent": 1, "max_pending": 4, "startup_timeout_seconds": 2, "account_wait_seconds": 1}), encoding="utf-8")
        client = requests.Session()
        client.trust_env = False
        base = f"http://127.0.0.1:{port}"
        headers = {"Authorization": "Bearer fixture-local-api-key"}
        with (work / "process.log").open("w", encoding="utf-8") as log:
            process = subprocess.Popen([str(executable), "-config", str(config)], cwd=work, env=isolated_env, stdout=log, stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW)
            try:
                for _ in range(100):
                    if process.poll() is not None:
                        raise RuntimeError("fixture gateway exited before becoming ready")
                    try:
                        if client.get(base + "/healthz", timeout=1, allow_redirects=False).status_code == 200:
                            break
                    except requests.RequestException:
                        pass
                    time.sleep(0.05)
                else:
                    raise RuntimeError("fixture gateway did not become healthy")
                for route in ("/admin/api/overview", "/admin/api/audit", "/admin/api/requests"):
                    assert client.get(base + route, timeout=2, allow_redirects=False).status_code == 401, route
                overview = client.get(base + "/admin/api/overview", headers=headers, timeout=2, allow_redirects=False).json()
                assert overview["build"] == {"commit": commit, "known": True, "built_at": built_at}
                assert overview["effective"]["startup_timeout"] == "2s" and overview["effective"]["max_pending"] == 4
                assert overview["audit"]["enabled"] and overview["audit"]["max_total_bytes"] == 67108864
                assert Path(overview["audit"]["dir"]) == work / "data/audit"
                panel = client.get(base + "/", timeout=2, allow_redirects=False)
                panel.encoding = "utf-8"
                assert panel.status_code == 200 and "运行配置" in panel.text and "最近请求" in panel.text
                response = client.post(base + "/v1/chat/completions", headers=headers, json={"model": "fixture-unavailable", "messages": [{"role": "user", "content": "fixture-content-must-not-be-audited"}]}, timeout=2, allow_redirects=False)
                assert response.status_code == 503
                request_id = response.headers["X-Request-Id"]
                page = client.get(base + "/admin/api/requests", headers=headers, params={"model": "fixture-unavailable"}, timeout=2, allow_redirects=False).json()
                event = page["events"][0]
                assert event["id"] == request_id and event["status"] == 503 and event["outcome"] == "no_healthy_account"
                assert event["account"] is None and event["first_data_ms"] is None and event["attempts"] == 0
                assert "fixture-local-api-key" not in json.dumps(page) and "fixture-content-must-not-be-audited" not in json.dumps(page)
                stats = client.get(base + "/admin/api/audit", headers=headers, timeout=2, allow_redirects=False).json()["audit"]
                assert stats["enabled"] and stats["dropped"] == 0
            finally:
                if process.poll() is None:
                    process.terminate()
                process.wait(timeout=10)
        linux_env = isolated_env | {"GOOS": "linux", "GOARCH": "amd64"}
        subprocess.run([str(go), "build", "-o", str(work / "gateway-linux"), "./cmd/server"], cwd=root, env=linux_env, check=True, timeout=90)
        print(json.dumps({"passed": True, "checks": ["actual executable startup", "isolated auth/data/config", "health", "all audit routes authenticated", "injected build identity", "effective limits", "derived persistent audit path", "served embedded panel", "HTTP request ID and 503 audit correlation", "honest absent metadata", "fixture secrets/content absent", "Linux amd64 build"]}))


if __name__ == "__main__":
    main()
