#!/usr/bin/env python3
"""Contract checks for the shipped Compose files, GitHub workflows and .cnb.yml.

These assertions describe deployment behaviour rather than copying an
implementation: the default deployment must pull a published image, keep one
port and the original mounts, and isolate the updater; the local-build overlay
must never be replaced by a published image; version-tag CI must validate before
it publishes, must never update ``latest`` from a prerelease and must keep
untrusted pull requests read-only.

PyYAML is a test-only dependency. CI installs it explicitly
(``python -m pip install pyyaml``) before running this file; the shipped product
does not import it.
"""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import re
import unittest

try:
    import yaml
except ImportError:  # pragma: no cover - surfaced as a clear module failure
    yaml = None


ROOT = Path(__file__).resolve().parents[1]
COMPOSE = ROOT / "docker-compose.yml"
BUILD_OVERLAY = ROOT / "docker-compose.build.yml"
CNB = ROOT / ".cnb.yml"
WORKFLOW_DIR = ROOT / ".github" / "workflows"
SCRIPT_DIR = Path(__file__).resolve().parent

PUBLISHED_IMAGE = "ghcr.io/azurekiln2333/codearts2api:latest"
README_LANGUAGES = ("README.md", "README.zh-TW.md", "README.en.md")
PINNED_VERSION = re.compile(r":v?\d+(?:\.\d+){1,2}(?:[-+][0-9A-Za-z.-]+)?(?:@sha256:[0-9a-f]{64})?\Z")
CREDENTIAL_LITERAL = re.compile(r"(?:ghp_[0-9A-Za-z]{20,}|glpat-[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16})")


def setUpModule():
    if yaml is None:
        raise RuntimeError(
            "PyYAML is required for deployment contract checks: "
            "python -m pip install pyyaml"
        )


def load_yaml(path: Path):
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def env_map(value):
    """Normalise a Compose environment block (mapping or KEY=VALUE list)."""
    if isinstance(value, dict):
        return {str(key): value[key] for key in value}
    result = {}
    for entry in value or []:
        key, separator, item = str(entry).partition("=")
        result[key] = item if separator else None
    return result


def as_list(value):
    if value is None:
        return []
    return [value] if isinstance(value, str) else list(value)


def triggers(document):
    # PyYAML resolves the unquoted YAML 1.1 key `on` to True.
    value = document.get("on")
    if value is None:
        value = document.get(True)
    return value or {}


def iter_strings(node):
    if isinstance(node, str):
        yield node
    elif isinstance(node, dict):
        for key, value in node.items():
            yield str(key)
            yield from iter_strings(value)
    elif isinstance(node, (list, tuple)):
        for value in node:
            yield from iter_strings(value)


def text_of(node) -> str:
    return "\n".join(iter_strings(node))


def cnb_pipeline_entries(document):
    """Yield (build-name, pipeline-entry) for every CNB pipeline entry."""
    entries = []
    for top_key in ("main",):
        section = document.get(top_key)
        if isinstance(section, dict):
            for trigger, value in section.items():
                for entry in as_list(value):
                    if isinstance(entry, dict) and "stages" in entry:
                        entries.append((top_key + "/" + str(trigger), entry))
    for top_key, section in document.items():
        if top_key == "main" or not isinstance(section, dict):
            continue
        for trigger, value in section.items():
            for entry in as_list(value):
                if isinstance(entry, dict) and "stages" in entry:
                    entries.append((str(top_key) + "/" + str(trigger), entry))
    return entries


def cnb_entry_image(entry) -> str:
    docker = entry.get("docker")
    if isinstance(docker, dict):
        return str(docker.get("image") or "")
    if isinstance(docker, str):
        return docker
    return str(entry.get("image") or "")


def app_service(services, *, require_key=True):
    """Return (name, service) for the gateway service, never the updater."""
    candidates = []
    for name, service in services.items():
        image = str((service or {}).get("image") or "")
        if "watchtower" in image.lower() or "watchtower" in str(name).lower():
            continue
        environment = env_map((service or {}).get("environment"))
        if require_key and "CA2A_API_KEY" not in environment:
            continue
        candidates.append((name, service))
    if len(candidates) != 1:
        raise AssertionError(
            "expected exactly one gateway service, found " + repr([name for name, _ in candidates])
        )
    return candidates[0]


def updater_service(services):
    found = [
        (name, service)
        for name, service in services.items()
        if "watchtower" in str((service or {}).get("image") or "").lower()
        or "watchtower" in str(name).lower()
    ]
    if len(found) != 1:
        raise AssertionError("expected exactly one updater service, found " + repr([name for name, _ in found]))
    return found[0]


def split_top_level(text: str, separator: str = ":") -> list:
    """Split on ``separator`` outside ``${...}`` interpolations."""
    parts, current, depth = [], "", 0
    for character in text:
        if character == "{":
            depth += 1
        elif character == "}":
            depth = max(0, depth - 1)
        if character == separator and depth == 0:
            parts.append(current)
            current = ""
        else:
            current += character
    parts.append(current)
    return parts


def port_mapping(service):
    ports = as_list(service.get("ports"))
    if len(ports) != 1:
        raise AssertionError("expected one published port, found " + repr(ports))
    return split_top_level(str(ports[0]).split("/")[0])


def port_default(mapping: str) -> str:
    match = re.search(r"\$\{CA2A_PORT:-(\d+)\}", mapping)
    if not match:
        raise AssertionError("port mapping does not derive from CA2A_PORT: " + mapping)
    return match.group(1)


def volume_targets(service):
    targets = {}
    for entry in as_list(service.get("volumes")):
        parts = split_top_level(str(entry))
        if parts and parts[-1] in {"ro", "rw"}:
            parts, mode = parts[:-1], parts[-1]
        else:
            mode = ""
        if len(parts) < 2:
            raise AssertionError("unparsable volume entry: " + str(entry))
        targets[parts[-1]] = (parts[0], mode)
    return targets


class DefaultComposeTests(unittest.TestCase):
    """The standard deployment pulls an image and runs the isolated updater."""

    def setUp(self):
        self.compose = load_yaml(COMPOSE)
        self.services = self.compose["services"]
        self.name, self.app = app_service(self.services)

    def test_published_image_is_default_and_no_local_build(self):
        self.assertNotIn("build", self.app, "default deployment must not build locally")
        image = str(self.app["image"])
        self.assertIn(PUBLISHED_IMAGE, image)
        self.assertIn("CA2A_IMAGE", image, "the image must stay configurable for CNB/private registries")

    def test_one_port_drives_host_container_and_listener(self):
        parts = port_mapping(self.app)
        host, container = parts[-2], parts[-1]
        self.assertIn("CA2A_PORT", host)
        self.assertIn("CA2A_PORT", container)
        self.assertEqual(port_default(host), port_default(container))
        environment = env_map(self.app.get("environment"))
        self.assertIn("CA2A_LISTEN", environment)
        self.assertIn("CA2A_PORT", str(environment["CA2A_LISTEN"]))
        self.assertIn(":?", str(environment["CA2A_API_KEY"]), "the API key must stay a required guard")

    def test_env_file_and_original_mounts_are_preserved(self):
        env_files = as_list(self.app.get("env_file"))
        env_files = [str(entry.get("path")) if isinstance(entry, dict) else str(entry) for entry in env_files]
        self.assertIn(".env", env_files)
        targets = volume_targets(self.app)
        for target in ("/app/auths", "/app/data", "/app/config.json"):
            self.assertIn(target, targets)
        self.assertIn("CA2A_AUTHS_PATH", targets["/app/auths"][0])
        self.assertIn("CA2A_DATA_PATH", targets["/app/data"][0])
        self.assertIn("CA2A_CONFIG_PATH", targets["/app/config.json"][0])
        self.assertEqual(targets["/app/config.json"][1], "ro")

    def test_bounded_log_rotation(self):
        logging = self.app.get("logging") or {}
        self.assertEqual(logging.get("driver"), "json-file")
        options = logging.get("options") or {}
        self.assertIn("max-size", options)
        self.assertIn("max-file", options)

    def test_updater_is_opt_in_labelled_and_scope_isolated(self):
        labels = {str(key): str(value) for key, value in (self.app.get("labels") or {}).items()}
        self.assertEqual(labels.get("com.centurylinklabs.watchtower.enable"), "true")
        scope = labels.get("com.centurylinklabs.watchtower.scope", "")
        self.assertIn("COMPOSE_PROJECT_NAME", scope, "scope must be unique per Compose project")

        _, updater = updater_service(self.services)
        environment = env_map(updater.get("environment"))
        self.assertEqual(str(environment.get("WATCHTOWER_LABEL_ENABLE")).lower(), "true")
        self.assertEqual(str(environment.get("WATCHTOWER_SCOPE")), scope)
        self.assertRegex(
            str(environment.get("WATCHTOWER_POLL_INTERVAL")),
            r"\$\{[A-Za-z_][A-Za-z0-9_]*:-\d+\}",
            "update interval must be configurable with a sane default",
        )
        self.assertEqual(str(environment.get("WATCHTOWER_CLEANUP")).lower(), "true")

        updater_labels = {str(key): str(value) for key, value in (updater.get("labels") or {}).items()}
        self.assertEqual(updater_labels.get("com.centurylinklabs.watchtower.enable"), "false")
        self.assertEqual(updater_labels.get("com.centurylinklabs.watchtower.scope"), scope)

        mounts = [str(entry) for entry in as_list(updater.get("volumes"))]
        self.assertTrue(any("docker.sock" in entry for entry in mounts))
        # Standard deployment starts the updater with a plain ``up -d``; the
        # local-build overlay is the one that gates it behind the profile.
        self.assertFalse(as_list(updater.get("profiles")), "standard deployment must run the updater")

    def test_updater_enablement_matches_documentation(self):
        """Compose and every documented entrypoint must agree on updater startup."""
        _, updater = updater_service(self.services)
        documents = {name: (ROOT / name).read_text(encoding="utf-8") for name in README_LANGUAGES}
        documents[".env.example"] = (ROOT / ".env.example").read_text(encoding="utf-8")
        documents["docs/deployment.md"] = (ROOT / "docs" / "deployment.md").read_text(encoding="utf-8")
        disable_hints = ("stop watchtower", "rm -f watchtower", "--scale watchtower=0")
        if as_list(updater.get("profiles")):
            for name, body in documents.items():
                self.assertTrue(
                    "--profile auto-update" in body,
                    "the updater is profile-gated, so " + name + " must document the profile",
                )
        else:
            for name, body in documents.items():
                self.assertTrue("watchtower" in body.lower(), name + " must describe the updater")
                self.assertTrue(
                    any(hint in body for hint in disable_hints),
                    name + " must document how to disable automatic updates",
                )

    def test_updater_image_is_pinned_not_latest(self):
        _, updater = updater_service(self.services)
        image = str(updater["image"])
        self.assertNotIn(":latest", image)
        self.assertRegex(image, PINNED_VERSION)

    def test_no_credential_literals(self):
        self.assertIsNone(CREDENTIAL_LITERAL.search(COMPOSE.read_text(encoding="utf-8")))


class LocalBuildOverlayTests(unittest.TestCase):
    """The override builds locally and must never be replaced by the updater."""

    def setUp(self):
        self.overlay = load_yaml(BUILD_OVERLAY)
        self.services = self.overlay["services"]
        self.name, self.app = app_service(self.services, require_key=False)

    def test_overlay_builds_a_local_image(self):
        self.assertIn("build", self.app)
        build = self.app["build"]
        self.assertIn("context", build)
        self.assertIn("dockerfile", build)
        args = {str(key): value for key, value in (build.get("args") or {}).items()}
        self.assertIn("BUILD_COMMIT", args)
        self.assertIn("BUILD_TIME", args)
        self.assertEqual(self.app.get("pull_policy"), "build")
        image = str(self.app.get("image", ""))
        self.assertTrue(image, "the overlay must name its local image")
        self.assertNotIn(PUBLISHED_IMAGE, image, "local builds must not reuse the published image tag")

    def test_overlay_disables_the_updater_target(self):
        labels = {str(key): str(value) for key, value in (self.app.get("labels") or {}).items()}
        self.assertEqual(labels.get("com.centurylinklabs.watchtower.enable"), "false")
        found = [
            (name, service)
            for name, service in self.services.items()
            if "watchtower" in str((service or {}).get("image") or "").lower()
            or "watchtower" in str(name).lower()
        ]
        self.assertEqual(len(found), 1)
        _, updater = found[0]
        self.assertEqual(as_list(updater.get("profiles")), ["auto-update"])
        # ``up -d`` never starts a profiled service unless the profile is asked
        # for, so the overlay cannot update the local build by default.


class WorkflowContractTests(unittest.TestCase):
    """Version-tag CI validates first, publishes only on tags and never leaks PRs."""

    def setUp(self):
        self.paths = sorted(WORKFLOW_DIR.glob("*.yml")) + sorted(WORKFLOW_DIR.glob("*.yaml"))
        self.assertTrue(self.paths, "no workflow definitions found")
        self.documents = {path.name: load_yaml(path) for path in self.paths}
        self.texts = {path.name: path.read_text(encoding="utf-8") for path in self.paths}

    def release_workflow(self):
        for name, document in self.documents.items():
            tags = as_list((triggers(document).get("push") or {}).get("tags"))
            if any("v*" in str(tag) for tag in tags):
                return name, document
        raise AssertionError("no workflow triggers on v* tags")

    def test_single_release_workflow_covers_all_triggers(self):
        name, document = self.release_workflow()
        on = triggers(document)
        push = on.get("push") or {}
        self.assertTrue(as_list(push.get("tags")), name + " must trigger on v* tags")
        self.assertTrue(as_list(push.get("branches")), name + " must trigger on branch pushes")
        self.assertIn("pull_request", on, name + " must trigger on pull requests")
        self.assertIn("workflow_dispatch", on, name + " must allow manual dispatch")

    def test_pull_requests_validate_without_publish_permissions(self):
        for name, document in self.documents.items():
            self.assertIn("pull_request", triggers(document), name)
            permissions = document.get("permissions")
            if permissions is not None:
                self.assertNotEqual(str(permissions).lower(), "write-all", name)
                if isinstance(permissions, dict):
                    self.assertNotEqual(str(permissions.get("contents")), "write", name)
                    self.assertNotEqual(str(permissions.get("packages")), "write", name)
            else:
                for job_id, job in (document.get("jobs") or {}).items():
                    self.assertIsNotNone(job.get("permissions"), name + "/" + job_id + " needs explicit permissions")

    def validation_jobs(self, jobs):
        return [
            job_id
            for job_id, job in jobs.items()
            if any(word in job_id.lower() for word in ("test", "validate", "check"))
            or any(
                token in str(step.get("run", ""))
                for step in job.get("steps") or []
                for token in ("go test", "go vet", "test_release_tools.py")
            )
        ]

    def publish_jobs(self, jobs):
        found = {}
        for job_id, job in jobs.items():
            permissions = job.get("permissions") or {}
            steps = text_of(job.get("steps"))
            writes_registry_or_release = (
                str(permissions.get("contents")) == "write"
                or str(permissions.get("packages")) == "write"
                or "gh release" in steps
                or "git:release" in steps
                or "build-push-action" in steps
                or "docker push" in steps
            )
            if writes_registry_or_release:
                found[job_id] = job
        return found

    def test_release_workflow_tests_before_publishing(self):
        name, document = self.release_workflow()
        jobs = document["jobs"]
        validations = self.validation_jobs(jobs)
        self.assertTrue(validations, name + " has no validation job")
        publishes = self.publish_jobs(jobs)
        self.assertTrue(publishes, name + " has no publishing job")
        self.assertTrue(set(publishes).difference(validations), "publishing must be its own gated job")
        for job_id in publishes:
            condition = str(jobs[job_id].get("if", ""))
            self.assertTrue(
                "refs/tags/v" in condition or "ref_type == 'tag'" in condition or 'ref_type == "tag"' in condition,
                job_id + " must only publish on version tags",
            )
            self.assertTrue(
                any(self.reaches(jobs, job_id, validation) for validation in validations),
                job_id + " must depend on the validation job",
            )

    def reaches(self, jobs, job_id, target, seen=None):
        if job_id == target:
            return True
        seen = set() if seen is None else seen
        if job_id in seen or job_id not in jobs:
            return False
        seen.add(job_id)
        for need in as_list(jobs[job_id].get("needs")):
            if self.reaches(jobs, need, target, seen):
                return True
        return False

    def test_shared_release_logic_checksums_and_build_identity(self):
        name, document = self.release_workflow()
        text = self.texts[name]
        self.assertIn("scripts/release-metadata.py", text, "tag/version/latest logic must live in the shared helper")
        self.assertIn("scripts/release-source.py", text, "release must export the explicit source manifest")
        self.assertIn(".sha256", text, "release assets must ship checksums")
        self.assertTrue("sha256sum" in text or "--check" in text, "checksums must be verified before publishing")
        self.assertIn("github.sha", text, "the actual commit must be injected")
        self.assertIn("CA2A_BUILD_COMMIT", text)
        self.assertIn("CA2A_BUILD_TIME", text)
        self.assertTrue("date -u" in text or "TZ=UTC" in text, "build time must be UTC")
        self.assertIsNone(CREDENTIAL_LITERAL.search(text))

    def test_publish_permissions_and_helper_owned_latest(self):
        name, document = self.release_workflow()
        text = self.texts[name]
        self.assertIsNone(
            re.search(r"(?:docker\s+push|docker\s+tag)[^\n]*:latest", text),
            "latest must be decided by the shared helper, never inline",
        )
        jobs = document["jobs"]
        publishes = self.publish_jobs(jobs)
        self.assertTrue(any("image" in job_id.lower() for job_id in publishes), "no image publishing job")
        self.assertTrue(any("release" in job_id.lower() for job_id in publishes), "no release publishing job")
        for job_id, job in publishes.items():
            permissions = job.get("permissions") or {}
            if "release" in job_id.lower():
                self.assertEqual(str(permissions.get("contents")), "write", job_id)
            if "image" in job_id.lower():
                self.assertEqual(str(permissions.get("packages")), "write", job_id)


class CnbPipelineTests(unittest.TestCase):
    """The CNB mirror validates PRs and only publishes on version tags."""

    def setUp(self):
        self.document = load_yaml(CNB)
        self.text = CNB.read_text(encoding="utf-8")

    def test_main_and_pr_validate(self):
        main = self.document.get("main")
        self.assertIsInstance(main, dict)
        self.assertIn("push", main)
        self.assertIn("pull_request", main)
        validation = text_of(main["push"])
        self.assertIn("go test", validation)
        self.assertIn("scripts/release-metadata.py", validation)

    def test_pull_request_never_authenticates_or_publishes(self):
        pull_request = text_of(self.document["main"]["pull_request"])
        for forbidden in ("docker login", "docker push", "--password-stdin", "git:release", "gh release", "CNB_TOKEN"):
            self.assertNotIn(forbidden, pull_request)
        self.assertIn("go test", pull_request)

    def test_version_tags_publish_checksummed_source_and_image(self):
        release = text_of(self.document.get("v*"))
        self.assertIn("tag_push", self.text)
        self.assertIn("go test", release, "the tag pipeline must validate before publishing")
        self.assertIn("scripts/release-source.py", release)
        self.assertIn(".sha256", release)
        self.assertIn("docker push", release)
        self.assertIn("scripts/release-metadata.py", release)
        self.assertRegex(release, r"(?i)(pre-?release|latest)")

    def test_repository_image_path_is_dynamic(self):
        release = text_of(self.document.get("v*"))
        self.assertTrue("CNB_REPO_SLUG_LOWERCASE" in release or "CA2A_IMAGE" in release)
        self.assertIsNone(CREDENTIAL_LITERAL.search(self.text))


class CnbRuntimeTests(unittest.TestCase):
    """Every CNB pipeline entry declares the runtime it needs, and no job runs
    the race detector without a C toolchain or the docker image build without a
    docker service and the validation gate."""

    C_TOOLCHAIN = ("build-base", "gcc", "musl-dev", "libc6-dev", "apt-get install", "yum install")

    def setUp(self):
        self.entries = cnb_pipeline_entries(load_yaml(CNB))
        self.assertTrue(self.entries, "no CNB pipeline entries found")

    def test_every_pipeline_declares_a_runtime_image(self):
        for name, entry in self.entries:
            self.assertTrue(cnb_entry_image(entry), name + " must declare a runtime image")

    def test_go_jobs_use_a_go_runtime(self):
        go_entries = [
            (name, entry) for name, entry in self.entries
            if re.search(r"\bgo (?:test|vet|build)\b", text_of(entry))
        ]
        self.assertTrue(go_entries, "no Go validation entry found")
        for name, entry in go_entries:
            image = cnb_entry_image(entry).lower()
            self.assertTrue("golang" in image, name + " must use a Go runtime image, got " + repr(image))

    def test_python_checks_have_python3(self):
        python_entries = [
            (name, entry) for name, entry in self.entries
            if "scripts/release-metadata.py" in text_of(entry) or "scripts/release-source.py" in text_of(entry)
        ]
        self.assertTrue(python_entries, "no Python release entry found")
        for name, entry in python_entries:
            text = text_of(entry)
            image = cnb_entry_image(entry).lower()
            self.assertTrue(
                "python" in image or "python3" in text or "py3-yaml" in text,
                name + " runs the Python release helpers without a Python runtime",
            )

    def test_race_jobs_have_a_c_toolchain(self):
        race_entries = [(name, entry) for name, entry in self.entries if "-race" in text_of(entry)]
        self.assertTrue(race_entries, "the release gate must run the race detector")
        for name, entry in race_entries:
            text = text_of(entry)
            image = cnb_entry_image(entry).lower()
            self.assertTrue(
                any(token in text for token in self.C_TOOLCHAIN),
                name + " runs `-race` without a C toolchain (gcc/musl-dev/build-base)",
            )
            self.assertIn(
                "CGO_ENABLED=1", text,
                name + " must enable cgo for the race detector on a musl image",
            )

    def test_image_build_has_docker_service_and_validation_gate(self):
        image_entries = [
            (name, entry) for name, entry in self.entries
            if "docker push" in text_of(entry) or "docker buildx build" in text_of(entry)
        ]
        self.assertTrue(image_entries, "no CNB image publishing entry found")
        for name, entry in image_entries:
            self.assertIn("docker", as_list(entry.get("services")), name + " needs the docker service")
            text = text_of(entry)
            self.assertIn("go test", text, name + " must not bypass the test gate")
            self.assertIn("scripts/release-metadata.py", text, name + " must use the shared release helper")


class ReadmeLanguageTests(unittest.TestCase):
    """The three language READMEs ship together and stay mutually navigable."""

    def setUp(self):
        missing = [name for name in README_LANGUAGES if not (ROOT / name).is_file()]
        if missing:
            self.fail("missing language README(s): " + ", ".join(missing))
        self.texts = {name: (ROOT / name).read_text(encoding="utf-8") for name in README_LANGUAGES}

    def test_every_language_links_to_the_other_two(self):
        for name, text in self.texts.items():
            for other in README_LANGUAGES:
                self.assertIn(other, text, name + " must navigate to " + other)

    def test_key_commands_and_default_image_agree_across_languages(self):
        for name, text in self.texts.items():
            for token in ("docker compose pull", "docker compose up -d"):
                self.assertIn(token, text, name + " must document `" + token + "`")
            for token in ("CA2A_PORT", "CA2A_IMAGE"):
                self.assertIn(token, text, name + " must document " + token)
            self.assertIn(
                "ghcr.io/azurekiln2333/codearts2api",
                text,
                name + " must name the default published image",
            )
            self.assertIsNone(
                CREDENTIAL_LITERAL.search(text),
                name + " must not embed credential literals",
            )


class ManifestInputTests(unittest.TestCase):
    """The explicit source manifest must ship the deployment inputs it documents."""

    def test_manifest_requires_deployment_and_ci_inputs(self):
        spec = importlib.util.spec_from_file_location("release_source", SCRIPT_DIR / "release-source.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        required = module.REQUIRED_FILES
        for path in (
            "docker-compose.yml",
            "docker-compose.build.yml",
            ".cnb.yml",
            ".env.example",
            ".dockerignore",
            "Dockerfile",
            "README.md",
            "README.zh-TW.md",
            "README.en.md",
            "scripts/release-metadata.py",
            "scripts/release-source.py",
            "scripts/test_release_metadata.py",
            "scripts/test_deployment_contracts.py",
            "scripts/test_release_tools.py",
            "scripts/verify-deployment.py",
        ):
            self.assertIn(path, required)
        # Workflow inputs are enumerated exactly, never wildcarded by directory.
        # The retired standalone Go workflow is not product input and must not
        # be archived even while it still exists in the tree.
        self.assertNotIn(".github/workflows/go.yml", required)
        self.assertIn(".github/workflows/build-and-release.yml", required)
        self.assertFalse(hasattr(module, "WORKFLOW_ROOT"))
        # docs/deployment.md ships through the existing docs/*.md rule once created.
        self.assertTrue(module.product_path("docs/deployment.md"))
        self.assertTrue((ROOT / "docs" / "deployment.md").is_file(), "the deployment guide must ship in the release")

    def test_manifest_does_not_recursively_include_scripts_or_github(self):
        spec = importlib.util.spec_from_file_location("release_source_paths", SCRIPT_DIR / "release-source.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        for path in (
            "scripts/private-local.py",
            "scripts/private-helper.py",
            ".github/dependabot.yml",
            ".github/workflows/notes.txt",
            ".github/workflows/go.yml",
            ".github/workflows/extra-release.yml",
            ".github/workflows/anything.yaml",
            ".github/ISSUE_TEMPLATE/bug.md",
        ):
            self.assertFalse(module.product_path(path), path)
        self.assertTrue(module.product_path(".github/workflows/build-and-release.yml"))
        self.assertTrue(module.product_path(".cnb.yml"))


if __name__ == "__main__":
    unittest.main()
