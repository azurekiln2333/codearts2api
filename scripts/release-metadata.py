#!/usr/bin/env python3
"""Shared release metadata for the GitHub and CNB pipelines.

The version-rule, image-tag, build-identity, checksum, notes and release-option
decisions live here once so the GitHub workflow, the CNB pipeline and the local
shell can never disagree about what a tag means. Everything in this module is
plain standard-library Python: the shipped product never imports it, only CI and
operators do.

Design rules encoded here:

* A release tag is ``vMAJOR.MINOR.PATCH`` with an optional SemVer 2.0.0
  pre-release. Build metadata (``+...``) is rejected because ``+`` is not a
  legal container tag: accepting it would let the source Release succeed while
  every image push failed. Anything else is rejected, so a typo can never
  silently publish.
* ``latest`` is derived only from a stable tag. A pre-release tag never produces
  a ``latest`` image tag and never becomes the "latest" release.
* The build identity is the *full* commit plus a UTC build time. The time is
  supplied by the caller (CI ``date -u``); it is never guessed from the commit
  date.
* The source archive keeps the exporter's own name; only the release tag, the
  checksums and the notes are versioned.
* Every container tag (the base tag, ``latest``, and each ``-<arch>``
  intermediate) is validated here before anything is exported or pushed, so an
  illegal tag can never reach a registry.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import sys
import tempfile
from datetime import datetime, timezone


# A release tag is ``vMAJOR.MINOR.PATCH`` with an optional SemVer 2.0.0
# pre-release. Build metadata (``+...``) is deliberately rejected: ``+`` is not a
# legal container tag character, so accepting it would let a source Release
# succeed while every image push failed. One rule, no alternate mapping.
_NUM = r"(?:0|[1-9][0-9]*)"
_PRE_IDENT = r"(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
SEMVER = re.compile(
    r"^v(?P<major>" + _NUM + r")\.(?P<minor>" + _NUM + r")\.(?P<patch>" + _NUM + r")"
    r"(?:-(?P<pre>" + _PRE_IDENT + r"(?:\." + _PRE_IDENT + r")*))?$"
)
COMMIT = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")
REPO_COMPONENT = re.compile(r"^[a-z0-9]+(?:[._/-]?[a-z0-9]+)*\Z")
REGISTRY_HOST = re.compile(r"^[A-Za-z0-9.-]+(?::[0-9]+)?\Z")
UTC_TIME = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\Z")
# Docker tag part: ``[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}``, i.e. at most 128
# characters. Checked for the base tag and for every arch-suffixed tag.
DOCKER_TAG = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}\Z")
ARCHITECTURES = ("amd64", "arm64")
ARCHIVE_PREFIX = "codearts2api-source-"


class ReleaseError(Exception):
    """A release input is invalid; the message is safe to print in CI logs."""


# --------------------------------------------------------------------------- #
# Version rules
# --------------------------------------------------------------------------- #
def check_docker_tag(tag: str) -> str:
    """Refuse a tag part Docker would reject (illegal characters or >128 chars)."""
    if not DOCKER_TAG.match(tag):
        raise ReleaseError(
            "not a legal container tag "
            "([A-Za-z0-9_][A-Za-z0-9_.-]{0,127}, at most 128 chars): " + repr(tag)
        )
    return tag


def parse_version(tag: str) -> dict:
    """Validate a supported release tag and return its decomposed parts.

    The only supported form is ``vMAJOR.MINOR.PATCH[-prerelease]``. Build
    metadata (``+...``) is refused up front, and the tag is checked to be a
    legal container tag part, because an illegal tag would otherwise only fail
    later at image-push time.
    """
    if not isinstance(tag, str) or not tag:
        raise ReleaseError("release tag is empty")
    if "+" in tag:
        raise ReleaseError(
            "build metadata (+...) is not supported for release tags: " + tag
        )
    match = SEMVER.match(tag)
    if not match:
        raise ReleaseError(
            "release tag must look like vMAJOR.MINOR.PATCH or "
            "vMAJOR.MINOR.PATCH-prerelease: " + tag
        )
    check_docker_tag(tag)
    pre = match.group("pre")
    return {
        "tag": tag,
        "version": tag[1:],
        "major": int(match.group("major")),
        "minor": int(match.group("minor")),
        "patch": int(match.group("patch")),
        "prerelease": pre is not None,
        "prerelease_identifiers": pre.split(".") if pre else [],
        "stable": pre is None,
    }


def is_prerelease(tag: str) -> bool:
    return parse_version(tag)["prerelease"]


def is_stable(tag: str) -> bool:
    return parse_version(tag)["stable"]


def latest_allowed(tag: str) -> bool:
    """Only a stable tag may move ``latest``; a pre-release never does."""
    return parse_version(tag)["stable"]


def validate_tag_for_release(tag: str) -> dict:
    """Full pre-publish gate: SemVer, base docker tag, and every arch tag.

    Called before anything is exported or pushed, so an illegal tag can never
    produce a successful source Release followed by a failed image push.
    """
    parsed = parse_version(tag)
    for arch in ARCHITECTURES:
        check_docker_tag(tag + "-" + arch)
    return parsed


# --------------------------------------------------------------------------- #
# Image naming and tags
# --------------------------------------------------------------------------- #
def image_name(registry: str, repository: str, override: str | None = None) -> str:
    """Resolve ``registry/repository`` with an explicit override.

    ``repository`` is lower-cased because container registries such as GHCR
    reject uppercase owners. ``override`` (``CA2A_IMAGE`` / ``CNB_IMAGE``) wins
    so a private mirror or a different tag can be selected without editing CI.
    """
    if override:
        candidate = override.strip()
        if not candidate:
            raise ReleaseError("image override is empty")
        return candidate
    if not registry:
        raise ReleaseError("registry is empty")
    if not repository:
        raise ReleaseError("repository is empty")
    if not REGISTRY_HOST.match(registry):
        raise ReleaseError("registry is not a plain host[:port]: " + registry)
    lowered = repository.strip().lower()
    if not REPO_COMPONENT.match(lowered):
        raise ReleaseError("repository path is not a valid image path: " + repository)
    return registry.rstrip("/") + "/" + lowered


def _reject_tagged_image(image: str) -> None:
    # Reject an already-tagged image so the caller passes a bare repository
    # reference; a registry port (``host:5000/repo``) is still allowed.
    if not image or ":" in image.rsplit("/", 1)[-1]:
        raise ReleaseError("image must be a repository without a tag: " + repr(image))


def image_tags(image: str, tag: str) -> list:
    """Return the image tags for a release tag, newest first.

    The version tag always uses the git tag verbatim; ``latest`` is appended only
    for a stable tag. Every produced tag is validated as a legal container tag.
    """
    _reject_tagged_image(image)
    parsed = parse_version(tag)
    candidates = [tag]
    if parsed["stable"]:
        candidates.append("latest")
    tags = []
    for candidate in candidates:
        check_docker_tag(candidate)
        tags.append(image + ":" + candidate)
    return tags


def arch_image_tags(image: str, tag: str, architectures=ARCHITECTURES) -> list:
    """Per-architecture intermediate refs for a multi-arch buildx assembly.

    Returns ``["<image>:<tag>-amd64", "<image>:<tag>-arm64"]`` (fixed order) so
    the CNB pipeline builds and pushes exactly the refs the helper validated.
    The ``-<arch>`` suffix is included in the <=128 container-tag length check.
    """
    _reject_tagged_image(image)
    parsed = parse_version(tag)
    result = []
    for arch in architectures:
        candidate = parsed["tag"] + "-" + arch
        check_docker_tag(candidate)
        result.append(image + ":" + candidate)
    return result


def registry_of(image: str) -> str:
    """Return the registry host of a container reference (its first path segment)."""
    if not image:
        raise ReleaseError("image is empty")
    return image.split("/", 1)[0]


def guard_custom_registry(image: str, platform_registry: str, has_password: bool) -> None:
    """Fail before any build when a non-platform registry has no credentials.

    CNB's ``services: [docker]`` only auto-authenticates to the platform
    registry. Pushing to any other registry without ``CNB_REGISTRY_PASSWORD``
    would otherwise fail at the very end of the pipeline with ``unauthorized``;
    this surfaces the real cause before a single docker command runs.
    """
    if not platform_registry:
        raise ReleaseError("platform registry is empty")
    target = registry_of(image)
    if target != platform_registry and not has_password:
        raise ReleaseError(
            "image registry " + repr(target) + " is not the platform registry "
            + repr(platform_registry) + " and CNB_REGISTRY_PASSWORD is not set; "
            "log in first (CNB_REGISTRY_USER / CNB_REGISTRY_PASSWORD) or use the "
            "platform registry"
        )


# --------------------------------------------------------------------------- #
# Build identity
# --------------------------------------------------------------------------- #
def utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def build_identity(commit: str, build_time: str | None = None) -> dict:
    """Validate the full commit and normalise the build time to UTC ``Z``.

    An empty or abbreviated commit is refused: a short SHA is not a reproducible
    build identity. ``build_time`` defaults to the current UTC clock but is
    always normalised (seconds precision, ``Z`` suffix).
    """
    if not isinstance(commit, str) or not COMMIT.match(commit):
        raise ReleaseError(
            "build identity requires the full 40/64 character lowercase commit SHA"
        )
    when = utc_now() if build_time is None else normalise_time(build_time)
    return {"commit": commit, "build_time": when}


def normalise_time(value: str) -> str:
    if not isinstance(value, str) or not value:
        raise ReleaseError("build time is empty")
    text = value.strip()
    if UTC_TIME.match(text):
        return text
    # Accept an explicit offset / ISO 8601 from `date --iso-8601=seconds` but
    # refuse a naive timestamp: an ambiguous local time is not a build identity.
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    try:
        parsed = datetime.fromisoformat(text)
    except ValueError as error:
        raise ReleaseError("build time is not ISO 8601: " + value) from error
    if parsed.tzinfo is None:
        raise ReleaseError("build time must carry a UTC offset: " + value)
    return parsed.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


# --------------------------------------------------------------------------- #
# Checksums, notes and manifests
# --------------------------------------------------------------------------- #
def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def checksum_line(digest: str, name: str) -> str:
    """Coreutils ``sha256sum --check`` compatible line (two spaces)."""
    if len(digest) != 64 or not re.fullmatch(r"[0-9a-f]{64}", digest):
        raise ReleaseError("checksum is not a lowercase SHA-256 digest")
    if not name or "\n" in name or "\r" in name:
        raise ReleaseError("checksum file name is invalid")
    return digest + "  " + name


def write_checksum(archive: Path, output: Path | None = None) -> Path:
    """Write ``<archive>.sha256`` (or ``output``) for a source archive."""
    archive = Path(archive)
    if not archive.is_file():
        raise ReleaseError("archive to checksum does not exist: " + str(archive))
    target = Path(output) if output else archive.with_name(archive.name + ".sha256")
    target.write_text(checksum_line(sha256_file(archive), archive.name) + "\n", encoding="utf-8")
    return target


def release_notes(
    tag: str,
    commit: str,
    build_time: str,
    archive: str,
    digest: str,
    image: str | None = None,
) -> str:
    """Human-readable release body shared by GitHub and CNB."""
    parsed = parse_version(tag)
    lines = [
        "# codearts2api " + tag,
        "",
    ]
    if parsed["prerelease"]:
        lines.append("> Pre-release: this tag never updates `latest`.")
        lines.append("")
    lines.extend(
        [
            "- Version: `" + parsed["version"] + "`",
            "- Source commit: `" + commit + "`",
            "- Built at: `" + normalise_time(build_time) + "` (UTC)",
            "- Source archive: `" + archive + "`",
            "- SHA-256: `" + digest + "`",
        ]
    )
    if image:
        lines.append("- Container image: `" + image + "`")
    lines.extend(
        [
            "",
            "The source archive is the explicit product manifest exported from the "
            "tagged commit. Real `.env`, `config.json`, credentials, `auths/`, "
            "`data/` and local ZIPs are never included.",
            "",
            "Default deployment pulls the published image; use the local-build "
            "Compose override to build from this source instead.",
            "",
        ]
    )
    return "\n".join(lines)


def release_options(
    tag: str,
    title: str | None = None,
    notes_file: str | None = None,
    description: str | None = None,
) -> dict:
    """Options for the CNB ``git:release`` task.

    ``overlying`` is true so a re-run only updates the existing release instead of
    deleting it first (idempotent re-release of the same tag). ``latest`` is set
    only for a stable tag.
    """
    parsed = parse_version(tag)
    if notes_file and description:
        raise ReleaseError("choose either notes_file or description, not both")
    options: dict = {
        "overlying": True,
        "tag": tag,
        "title": title or ("codearts2api " + tag),
        "preRelease": parsed["prerelease"],
        "latest": parsed["stable"],
    }
    if notes_file:
        options["descriptionFromFile"] = notes_file
    elif description:
        options["description"] = description
    return options


def release_manifest(
    tag: str,
    commit: str,
    build_time: str,
    archive: str,
    digest: str,
    image: str | None = None,
) -> dict:
    """Versioned metadata written next to the archive for later inspection."""
    parsed = parse_version(tag)
    identity = build_identity(commit, build_time)
    if len(digest) != 64 or not re.fullmatch(r"[0-9a-f]{64}", digest):
        raise ReleaseError("checksum is not a lowercase SHA-256 digest")
    return {
        "format": 1,
        "tag": parsed["tag"],
        "version": parsed["version"],
        "prerelease": parsed["prerelease"],
        "stable": parsed["stable"],
        "latest": parsed["stable"],
        "commit": identity["commit"],
        "build_time": identity["build_time"],
        "archive": Path(archive).name,
        "sha256": digest,
        "image": image,
    }


# --------------------------------------------------------------------------- #
# Source archive path selection (reuses scripts/release-source.py naming)
# --------------------------------------------------------------------------- #
def source_archive_name(commit: str) -> str:
    if not isinstance(commit, str) or not COMMIT.match(commit):
        raise ReleaseError("archive name needs a full lowercase commit SHA")
    return ARCHIVE_PREFIX + commit[:12] + ".zip"


def archive_path(
    repo: Path,
    commit: str,
    output: Path | None = None,
    tmp_root: Path | None = None,
) -> Path:
    """Choose an explicit output path for the source archive.

    An explicit ``output`` wins. Otherwise the exporter's default name is used,
    and when that file already exists (a same-commit re-run in a dirty
    workspace) a fresh temporary directory is used so the existing artifact is
    never overwritten. The directory is created so ``release-source.py`` can
    open the file exclusively.
    """
    if not isinstance(commit, str) or not COMMIT.match(commit):
        raise ReleaseError("archive needs a full lowercase commit SHA")
    if output is not None:
        target = Path(output)
        if target.suffix.lower() != ".zip":
            raise ReleaseError("output must be a .zip path")
        target.parent.mkdir(parents=True, exist_ok=True)
        return target
    repo = Path(repo)
    default = repo / source_archive_name(commit)
    if not default.exists():
        return default
    root = Path(tmp_root) if tmp_root is not None else Path(tempfile.mkdtemp(prefix="codearts2api-release-"))
    root.mkdir(parents=True, exist_ok=True)
    return root / default.name


def load_release_source(script_dir: Path | None = None):
    """Import ``scripts/release-source.py`` for direct reuse of its exporter."""
    base = Path(script_dir) if script_dir else Path(__file__).resolve().parent
    spec = importlib.util.spec_from_file_location("codearts2api_release_source", base / "release-source.py")
    if spec is None or spec.loader is None:  # pragma: no cover - defensive
        raise ReleaseError("cannot load scripts/release-source.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #
def _emit_key_values(pairs: dict, stream) -> None:
    for key in sorted(pairs):
        value = pairs[key]
        if isinstance(value, bool):
            value = "true" if value else "false"
        stream.write(str(key) + "=" + str(value) + "\n")


def _write_output(path: Path, text: str) -> None:
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text(text, encoding="utf-8")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="Shared release metadata for CI.")
    sub = parser.add_subparsers(dest="command", required=True)

    def add_tag(p):
        p.add_argument("--tag", required=True, help="Release tag such as v1.2.3")

    p = sub.add_parser("validate-tag", help="Validate a release tag and print its parts as JSON.")
    add_tag(p)
    p.add_argument("--output", type=Path, help="Write the JSON here instead of stdout.")

    p = sub.add_parser("is-prerelease", help="Exit 0 for a pre-release tag, 1 for stable.")
    add_tag(p)

    p = sub.add_parser("tag-outputs", help="Emit GITHUB_OUTPUT key=value lines.")
    add_tag(p)

    p = sub.add_parser("image-name", help="Resolve registry/repository with an optional override.")
    p.add_argument("--registry", default="")
    p.add_argument("--repository", default="")
    p.add_argument("--override", default="")

    p = sub.add_parser("image-tags", help="Print image tags, one per line (latest only when stable).")
    p.add_argument("--image", required=True)
    add_tag(p)

    p = sub.add_parser("arch-tags", help="Print per-arch intermediate refs as 'arch ref' lines.")
    p.add_argument("--image", required=True)
    add_tag(p)

    p = sub.add_parser("guard-registry", help="Fail when a non-platform image registry lacks credentials.")
    p.add_argument("--image", required=True)
    p.add_argument("--platform-registry", required=True)
    p.add_argument("--has-password", action="store_true")

    p = sub.add_parser("build-info", help="Validate the commit and normalise the UTC build time.")
    p.add_argument("--commit", required=True)
    p.add_argument("--time", default=None)
    p.add_argument("--emit", choices=("json", "env"), default="json")
    p.add_argument("--output", type=Path, help="Append key=value lines to this file (e.g. $GITHUB_ENV).")

    p = sub.add_parser("checksum", help="Write a sha256sum --check compatible checksum file.")
    p.add_argument("archive", type=Path)
    p.add_argument("--output", type=Path)

    p = sub.add_parser("archive-path", help="Print an explicit source-archive output path.")
    p.add_argument("--commit", required=True)
    p.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[1])
    p.add_argument("--output", type=Path)
    p.add_argument("--tmp-root", type=Path)

    p = sub.add_parser("archive-name", help="Print the commit-scoped source-archive file name.")
    p.add_argument("--commit", required=True)

    p = sub.add_parser("notes", help="Write release notes markdown.")
    add_tag(p)
    p.add_argument("--commit", required=True)
    p.add_argument("--time", required=True)
    p.add_argument("--archive", required=True)
    p.add_argument("--sha256", required=True)
    p.add_argument("--image", default=None)
    p.add_argument("--output", type=Path)

    p = sub.add_parser("release-options", help="Write CNB git:release options JSON.")
    add_tag(p)
    p.add_argument("--title", default=None)
    p.add_argument("--notes-file", default=None)
    p.add_argument("--description", default=None)
    p.add_argument("--output", type=Path)

    p = sub.add_parser("manifest", help="Write versioned release manifest JSON.")
    add_tag(p)
    p.add_argument("--commit", required=True)
    p.add_argument("--time", required=True)
    p.add_argument("--archive", required=True)
    p.add_argument("--sha256", required=True)
    p.add_argument("--image", default=None)
    p.add_argument("--output", type=Path)

    args = parser.parse_args(argv)
    try:
        if args.command == "validate-tag":
            payload = validate_tag_for_release(args.tag)
            text = json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
            if args.output:
                _write_output(args.output, text)
            else:
                sys.stdout.write(text)
        elif args.command == "is-prerelease":
            return 0 if is_prerelease(args.tag) else 1
        elif args.command == "tag-outputs":
            parsed = parse_version(args.tag)
            _emit_key_values(
                {
                    "tag": parsed["tag"],
                    "version": parsed["version"],
                    "prerelease": parsed["prerelease"],
                    "stable": parsed["stable"],
                    "latest": parsed["stable"],
                },
                sys.stdout,
            )
        elif args.command == "image-name":
            sys.stdout.write(image_name(args.registry, args.repository, args.override) + "\n")
        elif args.command == "image-tags":
            for tag in image_tags(args.image, args.tag):
                sys.stdout.write(tag + "\n")
        elif args.command == "arch-tags":
            refs = arch_image_tags(args.image, args.tag)
            for arch, ref in zip(ARCHITECTURES, refs):
                sys.stdout.write(arch + " " + ref + "\n")
        elif args.command == "guard-registry":
            guard_custom_registry(args.image, args.platform_registry, args.has_password)
        elif args.command == "build-info":
            identity = build_identity(args.commit, args.time)
            if args.emit == "env":
                pairs = {"CA2A_BUILD_COMMIT": identity["commit"], "CA2A_BUILD_TIME": identity["build_time"]}
                if args.output:
                    with Path(args.output).open("a", encoding="utf-8") as stream:
                        _emit_key_values(pairs, stream)
                else:
                    _emit_key_values(pairs, sys.stdout)
            else:
                text = json.dumps(identity, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
                if args.output:
                    _write_output(args.output, text)
                else:
                    sys.stdout.write(text)
        elif args.command == "checksum":
            target = write_checksum(args.archive, args.output)
            sys.stdout.write(str(target) + "\n")
        elif args.command == "archive-path":
            sys.stdout.write(str(archive_path(args.repo, args.commit, args.output, args.tmp_root)) + "\n")
        elif args.command == "archive-name":
            sys.stdout.write(source_archive_name(args.commit) + "\n")
        elif args.command == "notes":
            text = release_notes(
                args.tag, args.commit, args.time, Path(args.archive).name, args.sha256, args.image
            )
            if args.output:
                _write_output(args.output, text)
            else:
                sys.stdout.write(text)
        elif args.command == "release-options":
            options = release_options(args.tag, args.title, args.notes_file, args.description)
            text = json.dumps(options, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
            if args.output:
                _write_output(args.output, text)
            else:
                sys.stdout.write(text)
        elif args.command == "manifest":
            payload = release_manifest(
                args.tag, args.commit, args.time, args.archive, args.sha256, args.image
            )
            text = json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
            if args.output:
                _write_output(args.output, text)
            else:
                sys.stdout.write(text)
        return 0
    except (ReleaseError, OSError, UnicodeError, ValueError) as error:
        print("release-metadata: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
