#!/usr/bin/env python3
"""Build the final source ZIP and check an isolated localhost server, no real auth."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
from urllib.request import urlopen
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--go", required=True)
    args = parser.parse_args()
    archive_path = args.archive.resolve()
    with tempfile.TemporaryDirectory(prefix="codearts2api-release-smoke-") as temporary:
        source = Path(temporary) / "source"
        source.mkdir()
        with zipfile.ZipFile(archive_path) as archive:
            for name in archive.namelist():
                member = Path(name)
                if member.is_absolute() or ".." in member.parts or "\\" in name:
                    raise RuntimeError("Unexpected archive path")
            metadata = json.loads(archive.read("release.json"))
            for name, expected in metadata["files"].items():
                if hashlib.sha256(archive.read(name)).hexdigest() != expected:
                    raise RuntimeError("Source member checksum mismatch: " + name)
            archive.extractall(source)
        commit = metadata["source_commit"]
        environment = {key: value for key, value in os.environ.items() if not key.upper().startswith("CA2A_")}
        environment["CA2A_API_KEY"] = "fixture-release-smoke-key"
        binary = Path(temporary) / ("codearts2api.exe" if os.name == "nt" else "codearts2api")
        subprocess.run([
            args.go, "build", "-ldflags", "-X codearts2api/internal/server.BuildCommit=" + commit,
            "-o", str(binary), "./cmd/server",
        ], cwd=source, env=environment, check=True, timeout=90)
        auth_dir = Path(temporary) / "empty-auths"
        auth_dir.mkdir()
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        config = Path(temporary) / "isolated-config.json"
        config.write_text(json.dumps({
            "listen": "127.0.0.1:" + str(port), "auth_dir": str(auth_dir),
            "state_file": str(Path(temporary) / "data/state.json"),
            "watch": {"enabled": False}, "benefit_auto_claim": False, "credits_auto_claim": False,
        }), encoding="utf-8")
        base = "http://127.0.0.1:" + str(port)
        process = subprocess.Popen([
            str(binary), "-config", str(config),
        ], cwd=source, env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError("Isolated server exited before health check")
                try:
                    with urlopen(base + "/healthz", timeout=0.5) as response:
                        if response.read(16).strip() == b"ok":
                            break
                except OSError:
                    time.sleep(0.05)
            else:
                raise RuntimeError("Isolated server did not become healthy")
            subprocess.run([
                sys.executable, "-B", str(source / "scripts/verify-deployment.py"),
                "--url", base, "--expected-commit", commit,
            ], env=environment, check=True, timeout=45)
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        print(json.dumps({"passed": True, "source_commit": commit, "members": len(metadata["files"]),
                          "scope": "extracted source build and isolated localhost routes; no production traffic"}))


if __name__ == "__main__":
    main()
