#!/usr/bin/env python3
"""Export an explicit product manifest from a committed Git tree, never secrets."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
import sys
from datetime import datetime, timezone
import zipfile


# Keep this list explicit: new build inputs must be reviewed here. A recursive
# archive of the checkout could include credentials, local config and AI tooling.
REQUIRED_FILES = frozenset({
    ".dockerignore", ".env.example", ".cnb.yml", "Dockerfile",
    "docker-compose.yml", "docker-compose.build.yml",
    "config.example.json", "go.mod", "README.md", "README.zh-TW.md",
    "README.en.md", "LICENSE", "Makefile",
    "login.sh", "apply.sh", "credit.sh", "deploy/codearts2api.service",
    ".github/workflows/build-and-release.yml",
    "internal/server/panel.html", "scripts/release-source.py",
    "scripts/release-metadata.py", "scripts/verify-deployment.py",
    "scripts/test_release_tools.py", "scripts/test_release_metadata.py",
    "scripts/test_deployment_contracts.py",
})
OPTIONAL_FILES = frozenset({"go.sum", ".gitignore"})
COMMIT_PATTERN = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")


class ReleaseError(Exception):
    pass


def git(repo: Path, *args: str) -> bytes:
    result = subprocess.run(
        ["git", "-C", str(repo), *args], capture_output=True, check=False,
    )
    if result.returncode:
        # Do not echo arbitrary Git stderr or local file contents into CI logs.
        raise ReleaseError("Git operation failed; check the repository and commit.")
    return result.stdout


def product_path(path: str) -> bool:
    # Workflow and script inputs are enumerated exactly; nothing under .github or
    # scripts is opened up by prefix, so an accidentally committed workflow or
    # helper can never be archived just because of its directory.
    if path in REQUIRED_FILES or path in OPTIONAL_FILES:
        return True
    parts = PurePosixPath(path).parts
    if len(parts) < 2 or ".." in parts or "\\" in path or path.startswith("/"):
        return False
    return (
        parts[0] in {"cmd", "internal"} and path.endswith(".go")
        or parts[0] == "docs" and path.endswith(".md")
    )


def collect_release(repo: Path, reference: str):
    commit = git(repo, "rev-parse", "--verify", "--end-of-options", reference + "^{commit}").decode().strip()
    if not COMMIT_PATTERN.fullmatch(commit):
        raise ReleaseError("Git returned an invalid full commit identity.")
    timestamp = int(git(repo, "show", "-s", "--format=%ct", commit).decode().strip())
    committed_at = datetime.fromtimestamp(timestamp, timezone.utc)
    members = {}
    modes = {}
    entries = git(repo, "ls-tree", "-rz", "--full-tree", commit).split(b"\0")
    for entry in entries:
        if not entry:
            continue
        head, raw_path = entry.split(b"\t", 1)
        mode, kind, object_id = head.decode("ascii").split(" ")
        path = raw_path.decode("utf-8")
        if not product_path(path):
            continue
        if kind != "blob" or mode not in {"100644", "100755"}:
            raise ReleaseError("A product manifest member is not a regular file: " + path)
        members[path] = git(repo, "cat-file", "blob", object_id)
        modes[path] = 0o755 if mode == "100755" or path.endswith((".sh", ".py")) else 0o644
    missing = set(REQUIRED_FILES.difference(members))
    if missing:
        raise ReleaseError("Commit is missing release inputs; commit the finished work first: " + ", ".join(sorted(missing)))
    for source_root in ("cmd/server/", "internal/"):
        if not any(path.startswith(source_root) and path.endswith(".go") for path in members):
            raise ReleaseError("Commit has no Go source in " + source_root)

    # Build time is intentionally absent: the actual image build supplies it.
    members["release.env"] = ("CA2A_BUILD_COMMIT=" + commit + "\n").encode("ascii")
    modes["release.env"] = 0o644
    metadata = {
        "format": 1,
        "source_commit": commit,
        "source_committed_at": committed_at.isoformat().replace("+00:00", "Z"),
        "files": {path: hashlib.sha256(data).hexdigest() for path, data in sorted(members.items())},
    }
    members["release.json"] = (json.dumps(metadata, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode("utf-8")
    modes["release.json"] = 0o644
    return commit, committed_at, members, modes


def create_release(repo: Path, reference: str = "HEAD", output: Path | None = None):
    commit, committed_at, members, modes = collect_release(repo, reference)
    target = output or repo / ("codearts2api-source-" + commit[:12] + ".zip")
    target = target.resolve()
    if target.suffix.lower() != ".zip":
        raise ReleaseError("Output must use a .zip extension.")
    target.parent.mkdir(parents=True, exist_ok=True)
    if target.exists():
        raise ReleaseError("Output already exists; choose a new path: " + str(target))
    zip_time = committed_at
    if zip_time.year < 1980:
        zip_time = datetime(1980, 1, 1, tzinfo=timezone.utc)
    elif zip_time.year > 2107:
        zip_time = datetime(2107, 12, 31, 23, 59, 58, tzinfo=timezone.utc)
    created = False
    try:
        with target.open("xb") as stream:
            created = True
            # Stored members make the same commit byte-for-byte reproducible
            # across Python/zlib versions without depending on a compressor.
            with zipfile.ZipFile(stream, "w", compression=zipfile.ZIP_STORED) as archive:
                for path, data in sorted(members.items()):
                    info = zipfile.ZipInfo(path, zip_time.timetuple()[:6])
                    info.create_system = 3
                    info.external_attr = (stat.S_IFREG | modes[path]) << 16
                    archive.writestr(info, data)
    except Exception:
        if created:
            target.unlink(missing_ok=True)
        raise
    return target, commit, len(members)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--commit", default="HEAD", help="Finished work commit; export reads committed blobs only.")
    parser.add_argument("--output", type=Path, help="New ZIP path; existing files are never replaced.")
    args = parser.parse_args(argv)
    try:
        target, commit, count = create_release(args.repo.resolve(), args.commit, args.output)
    except (ReleaseError, OSError, UnicodeError, ValueError) as error:
        print("Release failed: " + str(error), file=sys.stderr)
        return 1
    print(f"Created {target}\nSource commit: {commit}\nFiles: {count}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
