# R17 Compose direct .env delivery verification

Date: 2026-10-24. Implementation is frozen for parent integration/review.

## Changes

- `docker-compose.yml` reads `.env` through service `env_file`; removed the
  optional application-variable enumeration. Three explicit environment entries
  remain: required API key validation, timezone fallback and the listener derived
  from `CA2A_PORT`. Build/name/restart/ports/mount interpolation is preserved.
- `.env.example` explains that one port value controls publishing and listening;
  removed the redundant commented LISTEN assignment. Optional application values
  are read directly from this file. The legacy OAuth callback-host variable is
  accurately described as an explicit port override, not a public callback host.
- Deployment README explains required `.env`, service env-file versus Compose
  interpolation and shell-only optional variables. Native LISTEN remains
  supported, without adding dotenv loading or a second native port setting.

## Validation

The machine has no installed `docker` CLI/engine. Downloaded the pinned official
Docker Compose standalone Windows x86-64 release `v2.29.7` into Git-excluded
`.local-test/tools/` and verified its SHA256 against the official release checksum.
It performs config parsing without a Docker engine.

Private harness `.local-test/compose_env_verify.py` copies only the reviewed
Compose YAML into a new isolated temporary project. Every dotenv/key/config value
is synthetic; inherited CA2A/COMPOSE/TZ settings are cleared. No actual project
`.env`, config or credential file is read or modified. Captured config JSON is
asserted in memory and is not printed.

Final standalone `config --quiet` / `config --format json` checks PASS:

- Bulk forwarding includes an optional field absent from YAML and preserves an
  empty application override.
- One port setting 21508 produces published/container/listener21508. A redundant
  LISTEN9999 value cannot desynchronize it. Default port remains7866.
- Required API key absent or empty rejects config; a missing service `.env` also
  rejects config even when the API key is supplied by shell.
- Explicit API-key/TZ/port shell overrides work. Optional application shell-only
  values are not forwarded and do not override the service `.env` values.
- CLI `--env-file alternate.env` affects interpolated key/port while service
  optional fields still come from the YAML-referenced `.env`.
- Default and custom build args/context, container name, restart policy and three
  credential/data/config mounts retain their expected values/read-only mode.

`python -B scripts/test_release_tools.py`: PASS, 15 tests in 6.597s.
`git diff --check -- docker-compose.yml .env.example README.md`: PASS.

The first private harness run exposed Windows default GBK decoding of a Chinese
Compose error; the harness now decodes UTF-8 explicitly and the final run passed
without reader-thread exceptions. This required no product change.

No Docker image runtime, production deployment, container recreation or credential
operation was performed. Parent owns independent review, source commit/export and
actual packaged-server verification. Historical R13 closure remains separate.
