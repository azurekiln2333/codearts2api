# CNB reference design findings (2026-10-25)

Reference root: D:/My2KProjects/cnb2api-panel. Research only. No secret files or reference product files changed.

## Exact source map

- `web/src/App.vue:293`: boot/login/application state branches.
- `web/src/App.vue:298`: complete split login page and SVG network illustration.
- `web/src/App.vue:370`: application header, grouped navigation, content/footer.
- `web/src/styles.css:17`: complete tokens. `:62`: dark tokens.
- `web/src/styles.css:355`: desktop grid shell. `:767`: metrics. `:884`: panels. `:1043`: tables.
- `web/src/styles.css:2733`: dialog. `:3118`: boot screen. `:3184`: login layout.
- `web/src/styles.css:3647`: mobile drawer (800px). Final 600px block collapses login to one column.
- `web/src/components/ModalShell.vue`: native dialog lifecycle, focus restoration, backdrop/ESC close.

## Visual contracts

Shell is 100dvh CSS grid: 180px sidebar, 52px full-width header, independent content scroll. Sidebar collapses to 64px. Outer canvas uses sidebar gray; content is a white inset bordered surface with 16px corner radius and margin 8px 8px 8px 0. Main content max-width 1600px, padding 20px 22px 18px. Topbar contains collapse, braces brand, breadcrumb, theme and user menu. Sidebar groups navigation and uses 36px rows. Selected row is pale blue with black left 3px inset marker and 1px inset border. Preserve codearts-specific six views rather than copying CNB route semantics.

Light: bg #f8f9fb, surface #fff, subtle #fafbfd, sidebar #f2f4f7, text #232b3a, secondary #596477, muted #7d8798, line #e9edf2, strong line #dce2eb, primary #3169df, hover #2459ca, primary-soft #edf3ff, selected-nav #cfddf2/border #9fb5d6. Dark: bg/sidebar #14181f, surface #1b2029, subtle #1e242e, text #e1e7f0, secondary #abb5c5, muted #8590a1, line #2a313e, strong line #3b4455, primary #78a2ff, primary-soft #263653, selected-nav #b8cceb/border #829dc6. Radii 4/8/12/16/28/999px, default panel 12px. Most controls 8px, content/dialog 16px. Base 14px weight 450; headings 650. No heavy shadows.

Login: two columns 1.08fr 1fr, min-height 100dvh. Story panel padding 42px 50px 28px, dotted radial-gradient grid at 21px spacing, right border. Brand top left. Story content centered, max-width 450px, heading 38px/1.65, slight negative tracking. Network illustration is inline SVG dashed branch path plus one 104px gateway node and three 140px account/workspace nodes with green dots; rotations -3/0/+3deg. Form column centers a max-width 320px form. Theme control top-right. Shield icon tile 48px with 16px radius, eyebrow, 25px welcome heading, muted hint, password field min-height 42px and primary full-width 42px submit. Submit says 进入控制台 / 正在验证 and disables while pending. At <=600px story art/text/footer disappear, brand remains, form max-width 330px, theme moves beside top brand.

Boot: `.boot-screen` is centered horizontal flex at 100dvh with gap 15px, muted 14px text. Blue 30px braces tile (extra right margin 10px), LoaderCircle 20px, exact text 正在连接控制台. Loader spin 1.2s linear infinite. checkingSession begins true; mount loads auth config and establishes session then finally clears checkingSession. Failed session shows login; valid session shows shell. Native implementation can reproduce this without Vue. Avoid an artificial indefinite delay.

Metrics: four equal columns, 12px gap, 18px bottom margin. Cards 130px minimum height, padding 14px 15px 12px, 1px line, radius 12px. Values 29px/1.3 weight650 with tabular numerals. <=1050px two columns. Panels use same border/radius and 58px heading toolbar. Tables scroll horizontally; account table min-width 980px, fixed layout. Header 12px muted on subtle fill, padding 11px 14px; body 19px 14px, 1px horizontal dividers; tiny blue hover and selected soft blue.

Dialog: native `<dialog>`, width min(480px,100vw - 32px), wide 650px, max-height 100dvh -60px, radius16px. Backdrop #12203560 and blur3px. Header sticky with 24px 25px 20px padding; body24px 25px; footer sticky16px 25px, subtle fill. Native showModal supplies focus containment. Save activeElement on mount and restore on close, body scroll lock, ESC and backdrop close.

Responsive shell <=800px: desktop sidebar width0, fixed drawer width208px below52px header with translated entrance, backdrop blur2px, main margin8px, breadcrumb hidden, mobile toggle shown. Theme uses root data-theme and localStorage; reduced-motion media query minimizes all transitions/animations.

## Assets and local preview

Both `web/dist` and `web/node_modules` exist. Dist assets currently: `index-BQSCVg5M.js`, `index-C6GyVZ_Q.css`, `dm-sans-latin-wght-normal-Xz1IZZA0.woff2`, `jetbrains-mono-latin-wght-normal-B9CIFXIH.woff2`. Vite preview can serve dist directly but requires synthetic `/api/*` interception or a backend for login/session/data. Vite dev uses port5173 and proxies /api to127.0.0.1:9002.

Preferred isolated real rendering: `node e2e/support/server.mjs` from reference web directory. It imports the actual panel server, uses mkdtemp OS temp storage and synthetic local worker+OIDC, never loads production credentials, listens127.0.0.1:19220 (worker19221), serves built UI. Fixture admin key is `browser-test-admin-key`; login POST `/api/session` JSON `{key: ...}`. `web/e2e/console.spec.ts:17` seeds via POST /api/accounts with name,repo,group,weight,priority,enabled,proxyKey:'test-worker-key',endpoint:'http://127.0.0.1:19221', then wake calls. This provides real overview/account data using local fixtures. Boot can be held with browser interception of session, then released. Do not run production server merely for screenshots.

Fonts: `web/node_modules/@fontsource-variable/dm-sans/files/dm-sans-latin-wght-normal.woff2` (weight100..1000); `web/node_modules/@fontsource-variable/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2` (100..800). CSS uses font-display swap. Chinese falls back Microsoft YaHei/system fonts. Copies can be embedded/data-URI or served by existing embedded asset handler, avoiding added web framework. Preserve license files `web/public/licenses/DM-Sans.txt`, `JetBrains-Mono.txt` (SIL OFL1.1), `Lucide.txt` (ISC copyright2026).

Lucide source module root: `web/node_modules/@lucide/vue/dist/esm`, actual icons use `.mjs`. Braces, LoaderCircle, ShieldCheck, Cloud, LayoutDashboard, UsersRound, ScrollText etc. are simple SVG nodes, reusable as inline SVG without Vue runtime. `web/public/favicon.svg` is self-contained 40px blue square with white braces/slash. Reference project/web package license is AGPL-3.0-only.
