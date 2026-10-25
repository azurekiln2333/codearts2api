#!/usr/bin/env python3
"""Pure-logic unit tests for the shared release metadata helper.

These tests never touch the network, Git or the working tree: every decision the
CI pipelines rely on (SemVer rules, ``latest`` semantics, image naming, build
identity, checksums and release options) is exercised as a plain function.
"""

from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT_DIR = Path(__file__).resolve().parent
STABLE = "v1.2.3"
PRERELEASE = "v2.0.0-rc.1"
COMMIT = "a" * 40
COMMIT64 = "b" * 64


def load_helper():
    spec = importlib.util.spec_from_file_location(
        "codesarts2api_release_metadata", SCRIPT_DIR / "release-metadata.py"
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


meta = load_helper()


def run_cli(argv):
    out, err = io.StringIO(), io.StringIO()
    with redirect_stdout(out), redirect_stderr(err):
        code = meta.main(argv)
    return code, out.getvalue(), err.getvalue()


class SemVerTests(unittest.TestCase):
    def test_stable_and_prerelease_and_build_tags_parse(self):
        stable = meta.parse_version(STABLE)
        self.assertEqual(stable["major"], 1)
        self.assertEqual(stable["minor"], 2)
        self.assertEqual(stable["patch"], 3)
        self.assertEqual(stable["version"], "1.2.3")
        self.assertTrue(stable["stable"])
        self.assertFalse(stable["prerelease"])

        pre = meta.parse_version(PRERELEASE)
        self.assertTrue(pre["prerelease"])
        self.assertFalse(pre["stable"])
        self.assertEqual(pre["prerelease_identifiers"], ["rc", "1"])

        alpha_numeric = meta.parse_version("v1.0.0-0a")
        self.assertTrue(alpha_numeric["prerelease"])

    def test_build_metadata_is_rejected(self):
        for tag in ("v1.2.3+build", "v1.2.3+build.5", "v1.2.3-beta.2+build.5"):
            with self.subTest(tag=tag), self.assertRaises(meta.ReleaseError):
                meta.parse_version(tag)

    def test_invalid_tags_are_rejected(self):
        invalid = [
            "",
            "1.2.3",
            "v1.2",
            "v1.2.3.4",
            "v01.2.3",
            "v1.02.3",
            "v1.2.03",
            "v1.2.3-",
            "v1.2.3-01",
            "v1.2.3-rc..1",
            "v1.2.3+",
            "v1.2.3-rc_1",
            "v1.2.3 beta",
            "release-1.2.3",
            "v1.2.3-rc.1+",
            "v1.2.3+build",
            "v1.2.3-beta.2+build.5",
        ]
        for tag in invalid:
            with self.subTest(tag=tag), self.assertRaises(meta.ReleaseError):
                meta.parse_version(tag)

    def test_latest_only_for_stable(self):
        self.assertTrue(meta.latest_allowed(STABLE))
        self.assertFalse(meta.latest_allowed(PRERELEASE))
        self.assertTrue(meta.is_stable(STABLE))
        self.assertTrue(meta.is_prerelease(PRERELEASE))


class ImageTests(unittest.TestCase):
    def test_default_image_lowercases_repository(self):
        name = meta.image_name("ghcr.io", "AzureKiln2333/CodeArts2API")
        self.assertEqual(name, "ghcr.io/azurekiln2333/codearts2api")

    def test_registry_port_is_preserved(self):
        self.assertEqual(
            meta.image_name("docker.cnb.cool:443", "team/Repo"),
            "docker.cnb.cool:443/team/repo",
        )

    def test_override_wins_and_keeps_its_own_case(self):
        self.assertEqual(
            meta.image_name("ghcr.io", "team/repo", "registry.example/Private/Thing:stable"),
            "registry.example/Private/Thing:stable",
        )

    def test_missing_registry_or_repository_is_an_error(self):
        for registry, repository in (("", "team/repo"), ("ghcr.io", "")):
            with self.subTest(registry=registry), self.assertRaises(meta.ReleaseError):
                meta.image_name(registry, repository)

    def test_stable_tag_gets_latest_and_prerelease_does_not(self):
        stable = meta.image_tags("ghcr.io/team/app", STABLE)
        self.assertEqual(stable, ["ghcr.io/team/app:" + STABLE, "ghcr.io/team/app:latest"])
        prerelease = meta.image_tags("ghcr.io/team/app", PRERELEASE)
        self.assertEqual(prerelease, ["ghcr.io/team/app:" + PRERELEASE])
        self.assertNotIn("latest", " ".join(prerelease))

    def test_already_tagged_image_is_rejected(self):
        with self.assertRaises(meta.ReleaseError):
            meta.image_tags("ghcr.io/team/app:latest", STABLE)

    def test_arch_intermediate_tags_come_from_the_helper(self):
        self.assertEqual(
            meta.arch_image_tags("ghcr.io/team/app", STABLE),
            ["ghcr.io/team/app:" + STABLE + "-amd64", "ghcr.io/team/app:" + STABLE + "-arm64"],
        )
        self.assertEqual(
            meta.arch_image_tags("ghcr.io/team/app", PRERELEASE),
            ["ghcr.io/team/app:" + PRERELEASE + "-amd64", "ghcr.io/team/app:" + PRERELEASE + "-arm64"],
        )

    def test_overlong_docker_tags_are_refused_before_publish(self):
        arch_overflow = "v1.2.3-" + "a" * 120  # 127 base, 133 with "-amd64"
        self.assertEqual(len(meta.check_docker_tag(arch_overflow)), 127)
        with self.assertRaises(meta.ReleaseError):
            meta.validate_tag_for_release(arch_overflow)
        with self.assertRaises(meta.ReleaseError):
            meta.arch_image_tags("ghcr.io/team/app", arch_overflow)
        too_long = "v1.2.3-" + "a" * 122  # 129 base
        with self.assertRaises(meta.ReleaseError):
            meta.parse_version(too_long)

    def test_registry_guard_requires_credentials_for_custom_registry(self):
        # Platform registry: no credentials needed (services:[docker] auto-logs in).
        meta.guard_custom_registry("docker.cnb.cool/ns/repo", "docker.cnb.cool", has_password=False)
        # Custom registry with credentials: allowed.
        meta.guard_custom_registry("ghcr.io/team/app", "docker.cnb.cool", has_password=True)
        # Custom registry without credentials: refused before any build.
        with self.assertRaises(meta.ReleaseError) as caught:
            meta.guard_custom_registry("ghcr.io/team/app", "docker.cnb.cool", has_password=False)
        self.assertIn("CNB_REGISTRY_PASSWORD", str(caught.exception))
        self.assertEqual(meta.registry_of("docker.cnb.cool:443/ns/repo"), "docker.cnb.cool:443")
        self.assertEqual(meta.registry_of("ghcr.io/team/app"), "ghcr.io")


class BuildIdentityTests(unittest.TestCase):
    def test_full_commit_and_utc_time(self):
        identity = meta.build_identity(COMMIT, "2026-10-26T04:05:06Z")
        self.assertEqual(identity["commit"], COMMIT)
        self.assertEqual(identity["build_time"], "2026-10-26T04:05:06Z")
        self.assertEqual(meta.build_identity(COMMIT64)["commit"], COMMIT64)

    def test_time_offsets_are_normalised_to_utc(self):
        self.assertEqual(
            meta.build_identity(COMMIT, "2026-10-26T12:05:06+08:00")["build_time"],
            "2026-10-26T04:05:06Z",
        )

    def test_short_or_uppercase_or_empty_commit_is_rejected(self):
        for commit in ("", "abc1234", COMMIT.upper(), "z" * 40):
            with self.subTest(commit=commit), self.assertRaises(meta.ReleaseError):
                meta.build_identity(commit, "2026-10-26T04:05:06Z")

    def test_naive_build_time_is_rejected(self):
        with self.assertRaises(meta.ReleaseError):
            meta.build_identity(COMMIT, "2026-10-26T04:05:06")

    def test_default_time_is_utc_and_second_precision(self):
        identity = meta.build_identity(COMMIT)
        self.assertRegex(identity["build_time"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")


class ChecksumAndNotesTests(unittest.TestCase):
    def test_checksum_round_trips_with_sha256sum_format(self):
        self.assertEqual(
            meta.checksum_line("a" * 64, "archive.zip"),
            ("a" * 64) + "  archive.zip",
        )
        with self.assertRaises(meta.ReleaseError):
            meta.checksum_line("short", "archive.zip")

    def test_sha256_file_matches_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            sample = Path(temp) / "sample.bin"
            sample.write_bytes(b"codearts2api")
            self.assertEqual(
                meta.sha256_file(sample),
                meta.sha256_bytes(b"codearts2api"),
            )

    def test_write_checksum_uses_archive_basename(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = Path(temp) / "codearts2api-source-abc.zip"
            archive.write_bytes(b"payload")
            target = meta.write_checksum(archive)
            self.assertEqual(target.name, archive.name + ".sha256")
            self.assertEqual(
                target.read_text(encoding="utf-8"),
                meta.sha256_file(archive) + "  " + archive.name + "\n",
            )

    def test_release_notes_carry_identity_and_prerelease_warning(self):
        stable_notes = meta.release_notes(STABLE, COMMIT, "2026-10-26T04:05:06Z", "src.zip", "c" * 64)
        for token in (STABLE, COMMIT, "2026-10-26T04:05:06Z", "src.zip", "c" * 64):
            self.assertIn(token, stable_notes)
        pre_notes = meta.release_notes(PRERELEASE, COMMIT, "2026-10-26T04:05:06Z", "src.zip", "c" * 64)
        self.assertIn("never updates", pre_notes)


class ReleaseOptionsTests(unittest.TestCase):
    def test_stable_release_options_are_overlying_and_latest(self):
        options = meta.release_options(STABLE, notes_file="dist/notes.md")
        self.assertIs(options["overlying"], True)
        self.assertEqual(options["preRelease"], False)
        self.assertEqual(options["latest"], True)
        self.assertEqual(options["descriptionFromFile"], "dist/notes.md")
        self.assertEqual(options["tag"], STABLE)

    def test_prerelease_never_sets_latest(self):
        options = meta.release_options(PRERELEASE, notes_file="dist/notes.md")
        self.assertEqual(options["preRelease"], True)
        self.assertEqual(options["latest"], False)

    def test_notes_and_description_are_mutually_exclusive(self):
        with self.assertRaises(meta.ReleaseError):
            meta.release_options(STABLE, notes_file="a.md", description="b")

    def test_manifest_records_versioned_identity(self):
        payload = meta.release_manifest(
            STABLE, COMMIT, "2026-10-26T04:05:06Z", "dir/codearts2api-source-aaaaaaaaaaaa.zip", "d" * 64, "ghcr.io/team/app:latest"
        )
        self.assertEqual(payload["version"], "1.2.3")
        self.assertEqual(payload["archive"], "codearts2api-source-aaaaaaaaaaaa.zip")
        self.assertEqual(payload["sha256"], "d" * 64)
        self.assertEqual(payload["latest"], True)
        self.assertEqual(payload["commit"], COMMIT)
        prerelease = meta.release_manifest(
            PRERELEASE, COMMIT, "2026-10-26T04:05:06Z", "a.zip", "d" * 64
        )
        self.assertEqual(prerelease["latest"], False)


class ArchivePathTests(unittest.TestCase):
    def test_archive_name_is_commit_scoped_and_not_versioned(self):
        self.assertEqual(meta.source_archive_name(COMMIT), "codearts2api-source-aaaaaaaaaaaa.zip")

    def test_explicit_output_is_used(self):
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp) / "nested" / "explicit.zip"
            chosen = meta.archive_path(Path(temp), COMMIT, output=target)
            self.assertEqual(chosen, target)
            self.assertTrue(chosen.parent.is_dir())

    def test_existing_default_falls_back_to_a_temporary_path(self):
        with tempfile.TemporaryDirectory() as temp:
            repo = Path(temp)
            default = repo / meta.source_archive_name(COMMIT)
            default.write_bytes(b"old release")
            chosen = meta.archive_path(repo, COMMIT, tmp_root=repo / "tmp")
            self.assertNotEqual(chosen, default)
            self.assertEqual(chosen.name, default.name)
            self.assertEqual(chosen.parent, repo / "tmp")
            self.assertTrue(chosen.parent.is_dir())
            # The pre-existing artifact must survive untouched.
            self.assertEqual(default.read_bytes(), b"old release")

    def test_short_commit_is_rejected(self):
        with self.assertRaises(meta.ReleaseError):
            meta.archive_path(Path("."), "abc")

    def test_release_source_exporter_is_reusable(self):
        source = meta.load_release_source()
        self.assertTrue(callable(source.create_release))
        self.assertIn("scripts/release-metadata.py", source.REQUIRED_FILES)


class CliTests(unittest.TestCase):
    def test_validate_tag_prints_json_and_rejects_bad_tags(self):
        code, out, _ = run_cli(["validate-tag", "--tag", PRERELEASE])
        self.assertEqual(code, 0)
        payload = json.loads(out)
        self.assertEqual(payload["version"], "2.0.0-rc.1")
        self.assertTrue(payload["prerelease"])
        code, _, err = run_cli(["validate-tag", "--tag", "1.2.3"])
        self.assertEqual(code, 1)
        self.assertIn("release-metadata", err)

    def test_tag_outputs_emit_github_output_lines(self):
        code, out, _ = run_cli(["tag-outputs", "--tag", STABLE])
        self.assertEqual(code, 0)
        lines = dict(line.split("=", 1) for line in out.strip().splitlines())
        self.assertEqual(lines["version"], "1.2.3")
        self.assertEqual(lines["prerelease"], "false")
        self.assertEqual(lines["latest"], "true")

    def test_image_name_and_tags_are_scriptable(self):
        code, out, _ = run_cli(
            ["image-name", "--registry", "ghcr.io", "--repository", "AzureKiln2333/CodeArts2API"]
        )
        self.assertEqual(code, 0)
        self.assertEqual(out.strip(), "ghcr.io/azurekiln2333/codearts2api")
        code, out, _ = run_cli(["image-tags", "--image", "ghcr.io/team/app", "--tag", PRERELEASE])
        self.assertEqual(code, 0)
        self.assertEqual(out.strip().splitlines(), ["ghcr.io/team/app:" + PRERELEASE])

    def test_arch_tags_cli_outputs_validated_refs(self):
        code, out, _ = run_cli(["arch-tags", "--image", "ghcr.io/team/app", "--tag", STABLE])
        self.assertEqual(code, 0)
        self.assertEqual(
            out.strip().splitlines(),
            ["amd64 ghcr.io/team/app:" + STABLE + "-amd64", "arm64 ghcr.io/team/app:" + STABLE + "-arm64"],
        )

    def test_plus_build_is_rejected_by_every_cli_entrypoint(self):
        # Real CLI regression for the blocking bug: a +build tag must never emit
        # an illegal docker tag, at any entry point.
        for argv in (
            ["validate-tag", "--tag", "v1.2.3+build"],
            ["tag-outputs", "--tag", "v1.2.3+build"],
            ["image-tags", "--image", "ghcr.io/team/app", "--tag", "v1.2.3+build"],
            ["arch-tags", "--image", "ghcr.io/team/app", "--tag", "v1.2.3+build"],
        ):
            with self.subTest(argv=argv):
                code, out, err = run_cli(argv)
                self.assertEqual(code, 1)
                self.assertNotIn("+", out)
                self.assertIn("release-metadata", err)

    def test_overlong_tag_cli_fails_before_publish(self):
        code, _, err = run_cli(["validate-tag", "--tag", "v1.2.3-" + "a" * 120])
        self.assertEqual(code, 1)
        self.assertIn("container tag", err)

    def test_guard_registry_cli_blocks_custom_registry_without_credentials(self):
        code, _, _ = run_cli([
            "guard-registry", "--image", "docker.cnb.cool/ns/repo", "--platform-registry", "docker.cnb.cool",
        ])
        self.assertEqual(code, 0)
        code, _, err = run_cli([
            "guard-registry", "--image", "ghcr.io/team/app", "--platform-registry", "docker.cnb.cool",
        ])
        self.assertEqual(code, 1)
        self.assertIn("CNB_REGISTRY_PASSWORD", err)
        code, _, _ = run_cli([
            "guard-registry", "--image", "ghcr.io/team/app", "--platform-registry",
            "docker.cnb.cool", "--has-password",
        ])
        self.assertEqual(code, 0)

    def test_build_info_env_emit(self):
        code, out, _ = run_cli(
            ["build-info", "--commit", COMMIT, "--time", "2026-10-26T04:05:06Z", "--emit", "env"]
        )
        self.assertEqual(code, 0)
        lines = dict(line.split("=", 1) for line in out.strip().splitlines())
        self.assertEqual(lines["CA2A_BUILD_COMMIT"], COMMIT)
        self.assertEqual(lines["CA2A_BUILD_TIME"], "2026-10-26T04:05:06Z")

    def test_release_options_cli_never_sets_inline_latest_for_prerelease(self):
        code, out, _ = run_cli(["release-options", "--tag", PRERELEASE, "--notes-file", "n.md"])
        self.assertEqual(code, 0)
        payload = json.loads(out)
        self.assertTrue(payload["overlying"])
        self.assertTrue(payload["preRelease"])
        self.assertFalse(payload["latest"])


if __name__ == "__main__":
    unittest.main()