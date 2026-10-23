# Catalog credential freshness and negative projection

Date: 2026-10-23, Asia/Shanghai. This implementation used synthetic localhost
HTTP fixtures only; no supplied credentials or external traffic were accessed.

The parent observed a new real gateway use expired STS for all metadata sources,
then refresh the same account successfully when chat validated it. The catalog
worker previously called `DiscoverModels` directly; its account eligibility guard
only checked account/auth membership, disable and cooldown. Consequently failed
old-credential requests could leave source backoff even after a later refresh.

Bounded background/startup and explicit POST refresh workers now call existing
`Pool.ValidateContext`, sharing its bounded refresh with chat and other callers.
Service/job cancellation releases the worker's wait while the one bounded shared
refresh can preserve a provider-rotated token. A second exact account/auth
eligibility check runs before discovery, and `WithCurrentAuth` still guards
publication. No cached admin GET initiates validation or metadata traffic.

A credential validation failure finishes that account's attempted job work as
failed, without publishing a fabricated HTTP failure for unattempted model
sources. Initially disabled accounts stay skipped. A follow-up regression proved
that detecting rotation only before/after worker validation was insufficient:
chat/watch can complete refresh before that worker begins. The source-backoff
owner now scopes one three-source retry state per account to captured auth owner
and a private SHA-256 digest of the signing tuple. A new generation replaces the
old state, and a late old result can neither set nor clear current retry times.
Removed/relogged accounts and expired retries are pruned. Same-generation genuine
failures retain five-minute backoff, explicit force remains effective, and a
source success clears its own retry. No API schema, claim policy, source parser
or account refresh algorithm was changed.

The cached admin negative projection also copies eligible negative entries under
the availability lock, releases that lock, and resolves the current account's
wire identity before applying `availabilityEntryMatches`. Old-login or wrong-wire
denials therefore cannot inflate blocked-account counts or appear as current
exclusions. Unknown mixed-case literal IDs retain authoritative current denials.
Resolution never runs while holding availability, preserving publisher order
pool -> catalog -> availability.

`internal/server/model_catalog_auth_test.go` covers actual expired-STS synthetic
HTTP refusal followed by one shared STS refresh and successful authenticated
sources; old-source-backoff recovery; cached GET no-I/O; terminal refresh failure;
initial/manual disable; service cancellation and job timeout; same-UID relogin and
remove/re-add; and exact-wire/login negative projection. The independent
`catalog_rotation_boundary_test.go` specifically refreshes credentials before
the catalog job begins. `model_source_backoff_test.go` uses real localhost HTTP
to assert token/AK/SK isolation, same-generation 5xx and force/recovery, late
old-generation success/failure, replacement auth and bounded generation pruning.
Full integration and
real-account verification remain the parent session's responsibility.
