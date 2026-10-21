"""Validate actual Compose interpolation in an isolated fixture directory."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

import requests


def main() -> None:
    root = next(p for p in Path(__file__).resolve().parents if (p / "go.mod").is_file())
    release = "https://github.com/docker/compose/releases/download/v2.39.4/"
    name = "docker-compose-windows-x86_64.exe"
    clean_env = {k: v for k, v in os.environ.items() if not k.startswith("CA2A_") and k != "TZ"}
    with tempfile.TemporaryDirectory(prefix="codearts2api-compose-check-") as temporary:
        work = Path(temporary)
        executable = work / name
        checksum_response = requests.get(release + name + ".sha256", timeout=30)
        checksum_response.raise_for_status()
        expected = checksum_response.text.split()[0]
        response = requests.get(release + name, timeout=(20, 120))
        response.raise_for_status()
        if hashlib.sha256(response.content).hexdigest() != expected:
            raise RuntimeError("official Compose asset checksum mismatch")
        executable.write_bytes(response.content)
        fixture = work / "fixture.env"
        compose = root / "docker-compose.yml"

        def run(values: dict[str, str], overrides: dict[str, str] | None = None, valid: bool = True) -> dict:
            fixture.write_text("\n".join(f"{key}={value}" for key, value in values.items()) + "\n", encoding="utf-8")
            command = [str(executable), "--project-directory", str(work), "--env-file", str(fixture), "-f", str(compose), "config", "--format", "json"]
            result = subprocess.run(command, env=clean_env | (overrides or {}), capture_output=True, text=True, encoding="utf-8", timeout=30)
            if not valid:
                assert result.returncode != 0, "missing/empty API key was accepted"
                return {}
            if result.returncode:
                raise RuntimeError(result.stderr)
            return json.loads(result.stdout)["services"]["codearts2api"]

        key = {"CA2A_API_KEY": "fixture-compose-key"}
        base = run(key)
        assert base["ports"][0]["target"] == 7866 and str(base["ports"][0]["published"]) == "7866"
        assert base["environment"]["CA2A_LISTEN"] == ":7866"
        expected_keys = set(re.findall(r'"(CA2A_[A-Z0-9_]+)"', (root / "cmd/server/config.go").read_text(encoding="utf-8")))
        assert expected_keys <= base["environment"].keys(), sorted(expected_keys - base["environment"].keys())
        assert base["environment"]["CA2A_AUDIT_ENABLED"] == ""
        assert base["build"]["args"] == {"BUILD_COMMIT": "", "BUILD_TIME": ""}
        configured = key | {
            "CA2A_PORT": "21508", "TZ": "UTC", "CA2A_CONTAINER_NAME": "fixture-gateway",
            "CA2A_RESTART_POLICY": "on-failure", "CA2A_BUILD_CONTEXT": "./source tree",
            "CA2A_BUILD_COMMIT": "fixture-build-identity", "CA2A_BUILD_TIME": "2026-10-21T10:00:00Z",
            "CA2A_AUTHS_PATH": "./existing credentials", "CA2A_DATA_PATH": "./existing data",
            "CA2A_CONFIG_PATH": "./existing config.json", "CA2A_MAX_CONCURRENT": "1",
            "CA2A_AUDIT_ENABLED": "false", "CA2A_AUDIT_DIR": "./data/custom-audit",
            "CA2A_AUDIT_MAX_QUEUE": "16", "CA2A_AUDIT_RECENT": "10",
            "CA2A_AUDIT_MAX_FILE_BYTES": "4096", "CA2A_AUDIT_MAX_FILES": "2",
            "CA2A_AUDIT_MAX_TOTAL_BYTES": "8192", "CA2A_AUDIT_RETENTION_DAYS": "2",
            "CA2A_AUDIT_FLUSH_INTERVAL": "100ms", "CA2A_CREDITS_AUTO_CLAIM": "0",
        }
        custom = run(configured)
        assert custom["ports"][0]["target"] == 21508 and str(custom["ports"][0]["published"]) == "21508"
        assert custom["environment"]["CA2A_LISTEN"] == ":21508"
        for name, value in configured.items():
            if name in expected_keys or name == "TZ":
                assert custom["environment"][name] == value, name
        assert custom["container_name"] == "fixture-gateway" and custom["restart"] == "on-failure"
        mounts = {mount["target"]: mount for mount in custom["volumes"]}
        assert mounts["/app/auths"]["source"].replace("\\", "/").endswith("/existing credentials")
        assert mounts["/app/data"]["source"].replace("\\", "/").endswith("/existing data")
        assert mounts["/app/config.json"]["source"].replace("\\", "/").endswith("/existing config.json")
        assert mounts["/app/config.json"]["read_only"] is True
        assert custom["build"]["args"]["BUILD_COMMIT"] == "fixture-build-identity"
        assert custom["build"]["context"].replace("\\", "/").endswith("/source tree")
        overridden = run(configured, {"CA2A_PORT": "21509", "CA2A_AUDIT_ENABLED": "1"})
        assert overridden["environment"]["CA2A_LISTEN"] == ":21509"
        assert overridden["environment"]["CA2A_AUDIT_ENABLED"] == "1"
        run({}, valid=False)
        run({"CA2A_API_KEY": ""}, valid=False)
        print(json.dumps({"compose": "v2.39.4", "sha256_verified": True, "forwarded_application_variables": len(expected_keys), "checks": ["defaults", "all environment keys forwarded", "port-listener synchronization", "audit overrides and false/zero", "existing credential mounts with spaces", "build args", "shell precedence", "missing and empty key rejection"]}))


if __name__ == "__main__":
    main()
