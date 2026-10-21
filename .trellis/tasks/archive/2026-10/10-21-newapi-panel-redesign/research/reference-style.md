# Research: Local New API visual style and workspace architecture

- Query: Rebuild the CodeArts2API panel using the user's local New API style and layout architecture.
- Scope: internal; reference project is read-only at `D:/My2KProjects/new-api`.
- Date: 2026-10-21

## Findings

### Reference identity and instructions

Read `new-api/AGENTS.md`, `new-api/web/AGENTS.md`, and its project `shadcn-ui/SKILL.md`. The reference is the current React/TypeScript frontend, not an older Semi UI dashboard. `web/components.json:3` selects `base-nova`, neutral colors, CSS variables, Hugeicons and subtle menu accents. `web/package.json` currently declares React 19, Tailwind 4, Base UI and TanStack Router/Table. These are references for visual and interaction conventions; CodeArts2API should retain its self-contained HTML and existing Go embedding.

### Workspace shell: reproduce the hierarchy

| Reference file / anchor | Actual pattern | Adaptation target |
|---|---|---|
| `web/src/components/layout/components/authenticated-layout.tsx:41` | Header spans full width; below it is a flex row of sidebar and inset main content. Main area is viewport height minus header and has bounded internal scrolling. | Full-width top bar, left navigation, independent active page; replace the current long stack of unrelated cards. |
| `web/src/styles/theme.css:99`; `components/layout/components/header.tsx:28` | Header is **3rem / 48px**, sticky at top, transparent against shell, 8px mobile / 12px desktop horizontal inset, 32px sidebar trigger. | Brand and control-panel identity on the left; refresh, theme and logout on the right. Move account operations to their page. |
| `web/src/components/ui/sidebar.tsx:50` | Expanded width **13rem / 208px**, mobile drawer **17rem / 272px**, collapsed content width **2.75rem / 44px** (inset padding adds to outer occupied width). | 208px desktop sidebar; at narrow widths use a real dismissible drawer, not an overflowing fixed sidebar. Collapse is optional if it compromises accessibility. |
| `web/src/context/layout-provider.tsx:32`; `components/ui/sidebar.tsx:325` | Default layout is `inset`, collapse style `icon`; inset main has 8px top/right/bottom margin, rounded corner and subtle shadow. | Very light neutral outer canvas, white/charcoal inset main; avoid glass, glow and large floating page margins. |
| `web/src/components/layout/components/app-sidebar.tsx:29` | Root sidebar and contextual drill-in sidebar views are URL driven; deep settings replace the tree and expose a back affordance. | Five views are enough here: overview, accounts, request audit, models, runtime/configuration. Each has a stable hash route; keep larger contextual navigation for future real subpages. |
| `web/src/components/layout/components/nav-group.tsx:68`; `components/ui/sidebar.tsx:498` | 11px muted group captions, 32px menu rows, 16px icons, 8px icon/text gap; selected item uses soft primary tint, blue text and medium weight. | Group items into workspace / management. Use restrained active row highlighting, not a large gradient block. Include text labels and `aria-current`. |

Sizes above assume the reference's default root size and theme density; user theme preferences can change them. The source supports both light and dark themes, with system mode the default (`web/src/context/theme-provider.tsx:37`). No running user's saved preferences were read.

### Color and typography tokens

Concrete source values from `web/src/styles/theme.css:97` and `:181`:

| Role | Light | Dark |
|---|---|---|
| Canvas / foreground | `oklch(1 0 0)` / `oklch(.145 0 0)` | `oklch(.235 0 0)` / `oklch(.965 0 0)` |
| Card | `oklch(1 0 0)` | `oklch(.285 0 0)` |
| Muted surface / text | `oklch(.97 0 0)` / `oklch(.49 0 0)` | `oklch(.305 0 0)` / `oklch(.78 0 0)` |
| Border | `oklch(.93 0 0)` | white at 10% |
| Primary | `oklch(.692 .141 243.716)` | `oklch(.54 .142 248.516)` |
| Sidebar | foreground mixed 0.5% into background | `oklch(.225 0 0)` |
| Active navigation tint | primary mixed 12% into background | primary mixed 20% into background |
| Active navigation text | `oklch(.579 .196 255.909)` | `oklch(.9 .05 250)` |
| Table header | foreground mixed 1.5% into background | foreground mixed 6% into background |
| Success / warning / error | `oklch(.596 .145 163.225)` / `oklch(.681 .162 75.834)` / `oklch(.577 .245 27.325)` | `oklch(.696 .17 162.48)` / `oklch(.769 .188 70.08)` / `oklch(.704 .191 22.216)` |

Implement equivalent original CSS tokens, with broadly supported sRGB fallbacks if needed. Blue is an accent for active navigation/actions, not a colored page backdrop. Verify small text contrast against the actual chosen surfaces; the reference primary is not automatically a suitable text color for every role.

Typography: `theme.css:104` defaults to browser sans; explicit Sans uses bundled Public Sans, and presets can select serif (`theme-presets.css:623`). CodeArts2API can use `system-ui`, `Segoe UI`, `PingFang SC`, `Microsoft YaHei`, sans-serif without downloading fonts. Page titles are **16px mobile / 18px desktop, bold**, not oversized hero headings (`section-page-layout.tsx:96`). Body/table text is 14px; secondary text 12px. Dashboard numbers use 24px semibold with tabular figures (`features/dashboard/components/ui/stat-card.tsx:276`); no fabricated sparklines if the API has no series.

Source base radius is 16px; its mapped radii are 9.6 / 12.8 / 16 / 22.4px (`theme.css:88`). Thus rounded cards/inset main are about 22px, controls about 16px, nav rows about 13px under default settings. A compact adaptation may consistently use 16px cards, 8px controls/nav, but should record this as an intentional density adjustment rather than claiming those are exact source values.

### Page and component treatment

- **Page shell:** `web/src/components/layout/components/section-page-layout.tsx:84` separates title/actions from scrollable content and an optional footer. Page horizontal padding is 12px mobile / 16px desktop, title top padding 12px / 20px; actions wrap, content has `min-height:0`, and pagination belongs below the table. Keep one title, short purpose text and contextual primary action per view.
- **Cards:** `web/src/components/ui/card.tsx:33` uses solid surface, thin foreground/10 ring, 16px internal padding/gap and restrained rounding. Description text is muted; footer gets a subtle muted fill plus top rule (`:105`). No translucent glass surface or colorful stripe on every metric.
- **Buttons and inputs:** `web/src/components/ui/button.tsx:25` / `:43` uses 32px default button height, 28px small, 24px extra small; primary solid accent, secondary outlined/ghost, destructive lightly tinted. `web/src/components/ui/input.tsx:30` uses 32px input height, 1px border, focus ring, clear disabled state. Mobile form inputs use 16px font to avoid browser zoom (`styles/index.css:131`). Give touch controls enough hit area without making desktop tables tall.
- **Tables:** `web/src/components/ui/table.tsx:34` uses 14px medium body text and tabular numbers; secondary cell text is 12px normal. Table headers are 40px, body rows default 60px (`:68`, `:105`), cells 8px padding. Row separators, subtle hover and neutral disabled background convey structure. The table container owns horizontal scrolling (`:45`), not the entire page. Long model/account/error fields should be bounded, truncated or wrapped intentionally.
- **Toolbar/mobile/paging:** `web/src/components/data-table/README.md` and `layout/data-table-page.tsx:368` separate filters, table/mobile list, bulk actions and footer pagination. A background refresh dims but preserves existing rows (`:422`); do not blank all data. Mobile uses a readable card list, or keeps a locally scrollable table when columns matter (`:412`). For our audit view, local table scroll is acceptable; account rows can adapt into stacked cards without duplicating active actions.
- **Badges:** `web/src/components/ui/badge.tsx:26` uses 20px compact pills, 12px medium text, restrained status tint. Success, disabled, cooling and history are distinct semantics; a historical consecutive error count must not masquerade as current availability.
- **Motion/accessibility:** restrained 120–150ms hover feedback and reduced-motion support (`styles/index.css:560`). Semantic header/nav/main, visible focus, keyboard-operable drawer/dialog and focus return are more important than copying animations. The reference includes skip-to-main (`authenticated-layout.tsx:42`).

### Proposed view allocation for the embedded panel

1. **Overview:** current account/availability/concurrency summaries, honest recently loaded request counters if clearly scoped, compact service/base-URL card and links into accounts/audit. Avoid fake charts, uptime or billing totals.
2. **Accounts:** page title + add authorization primary action; compact operation toolbar for validation/check-in/keepalive/reload; existing account table with capacity, current state, separate failure history and reachable per-account actions.
3. **Request audit:** filters immediately above the table, count/loss/storage notes nearby, existing exclusive-cursor pagination below. Preserve unknown HTTP status and committed 200 + failed execution as separate fields.
4. **Models:** count and searchable existing catalog as readable cards/list; show real IDs. Do not infer entitlement or success from catalog presence.
5. **Runtime/configuration:** effective timeout/admission/audit limits, build known/unknown, scheduler details and operational explanation in grouped label/value cards.

Implement shell/navigation/theme helpers around the existing renderer and action functions, retaining existing IDs or updating all listeners together. `internal/server/panel.go:9` embeds one HTML file; `panel.html:277` onward already owns base-path discovery, key storage, API requests, escaping, renderers, request cursor/filter state and OAuth state. Retain their data contracts. Hash routes work under reverse-proxy prefixes without a new Go route or frontend toolchain. Hidden views must not leave focusable controls in the tab order; unknown hashes should resolve to overview.

### Asset and source considerations

New API's root `LICENSE` is AGPLv3, and inspected source headers name QuantumNous, while CodeArts2API's root `LICENSE` is MIT. Do not import New API source files, logo, font binaries or icon packages as a shortcut. Recreate the general layout and visual conventions using original embedded HTML/CSS/JS, retain CodeArts2API branding and use simple original inline SVG icons. If exact source/assets are later requested, preserve their notices and review their own license terms first; no such copied asset is necessary for this task.

### Related specifications and external references

- `.trellis/spec/backend/request-audit.md`: bounded safe metadata, escaping, unknown/committed-status semantics, account history versus schedulability, honest build/runtime details.
- `.trellis/spec/backend/request-lifecycle.md`: configuration meanings and existing scheduler behavior; this UI task must not alter them.
- `.trellis/tasks/10-21-newapi-panel-redesign/prd.md`: presentation, navigation, compatibility and isolated verification requirements.
- No external web research or network request was needed/performed; local package declarations, source files and license notices are authoritative for this reference.

## Caveats / Not Found

- This is source inspection, not a claim that the local New API app was run or visually verified in a browser. Current user theme preferences are unknown.
- React components cannot be directly reused inside the Go-embedded page without a new toolchain; reproduce their visible structure while retaining the existing app logic.
- No credential/config/state files were inspected, no production request was made, and no file in the reference project was edited.
