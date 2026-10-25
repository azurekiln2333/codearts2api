# R18 independent console review (2026-10-25)

## Scope and result

Reviewed the actual final `internal/server/panel.html` diff, R18 PRD/design/plan,
embedded-panel/OAuth/token specs and reference/migration research. Source
ownership was transferred after implementation handoff. Only the panel and this
report were edited by this reviewer. No credentials, production requests or
reference-project changes.

No remaining in-scope defect was established after the local fix and checks
below. Parent still owns final integration, four-suite source-hash evidence,
Go/embed rebuild, immutable export and actual extracted-release checks. This
review does not close historical R13 or establish production deployment.

## Findings (fixed)

- File: `internal/server/panel.html`
- Issue: Login retained an implementation-detail paragraph describing
  localStorage/Bearer and added a decorative OPENAI COMPATIBLE API footer.
  Neither helps select the next login action; the reference has no storage
  explanation and user requested removal of redundant UI chrome.
- Fix: Removed both markup fragments and unused CSS selectors. Stored-key and
  Bearer authentication behavior are unchanged. No new slogans were added.

## Behavioral/source review

- Initial no-key overview attempt remains available for proxy-injected auth.
  Only that initial missing-key401 is silent; submitted bad keys show feedback.
- Initial/submitted auth uses one real boot surface and15s AbortController
  deadline. Finally clears the timer; failures return to an actionable form.
  Login epochs and overview versions reject obsolete response publication.
  showLogin aborts the active auth request, cancels view/OAuth polling, clears
  sensitive callback and prior account/audit/model UI state.
- Hidden surfaces use display:none; hidden page controls cannot enter visible
  focus order. Existing mobile drawer inert/Escape/focus trap and OAuth modal
  lifecycle remain intact. Collapsed navigation retains title-based names.
- API path-prefix handling, saved-key/theme storage keys, account actions,
  model refresh, connection diagnostics and token source/window/query semantics
  remain on their existing code paths. No new framework/runtime assets, made-up
  data, backend API or credential persistence changes.
- Layout uses reference52px header/180px sidebar/64px collapse, restrained
  blue/neutral light and dark tokens,16/12/8px shell/card/control radii and
  independent workspace/table scroll. Removed sidebar branding/build/shortcut
  controls remain absent.

## Actual visual inspection

Opened new screenshot files after the cleanup:
`.local-test/cnb-ui-1792926358158/{login-desktop,overview-desktop,login-mobile,shell-dark}.png`.
Compared the actual inspected reference screenshots and source findings. Split
login form/point texture/gateway illustration, centered compact form, inset
workspace/header/sidebar and mobile/dark rendering are coherent with reference.
Login implementation explanation/footer are absent. Existing overview data and
six navigation destinations are retained rather than fabricating CNB metrics.
Mobile login screenshot includes the transient logout toast from its fixture;
this is existing action feedback, not persistent login decoration.

## Verification

- Embedded inline JavaScript: `vm.Script` syntax PASS (one script).
- Browser fixture syntax: `node --check` PASS for cnb/browser/oauth/token suites.
- Whitespace: `git diff --check` PASS.
- Independent focused Chrome run: PASS, synthetic localhost only, screenshot
  directory `.local-test/cnb-ui-1792926358158/`.
  Covers real held initial/submitted boot, no pre-auth content, blank401 versus
  submitted bad-key feedback, reduced motion/hidden-control focus, transport
  failure,15s timeout/late response, saved-key reload, proxy prefixes, six pages
  desktop/mobile,64px collapse, theme persistence, drawer Escape and short phone.
- No TypeScript source or dedicated HTML lint command applies. Source JS syntax
  and browser runtime checks provide the applicable static/runtime validation.
- Four-suite full-source rerun and extracted final-release checks are parent-owned
  and must be completed after this source handoff. The independent focused run
  loaded its fixture before concurrent hash-reporting addition; final integration
  must record the current source hash with refreshed fixture output.

## Findings (not fixed)

None within the reviewed scope. Historical R13 and production rollout remain
outside this visual migration and are not claimed resolved.

## Final source identity / ownership handoff

Panel SHA256: `d3f9ac3a35460962ac92199a35c0f1cb4cea7c9f209e580f1b360440fa139f47`

Source edits are complete and released to parent for final integration. Reviewer
will not mutate panel or fixtures after this handoff.
