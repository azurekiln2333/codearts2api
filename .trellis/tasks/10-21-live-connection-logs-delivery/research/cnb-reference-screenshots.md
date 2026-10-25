# Rendered CNB reference screenshots (2026-10-25)

Output directory: `D:/My2KProjects/codearts2api/.local-test/cnb-reference-20261025/`

Captured from existing built reference UI with isolated `web/e2e/support/server.mjs`. All accounts are local synthetic fixtures. No production credentials, real OAuth, or external upstream used. Synthetic service cleanup verified: exec session24357 no longer exists. Get-NetTCPConnection found no LISTEN sockets on19220 or19221. No e2e/support/server.mjs node process remained. No process kill was necessary. Synthetic session key: browser-test-admin-key.

Screenshots:
- 01-login-desktop-light.png (1440x1000)
- 02-boot-desktop-light.png (1440x1000)
- 03-login-desktop-dark.png (1440x1000)
- 04-accounts-empty-desktop-light.png (1440x1000)
- 05-accounts-desktop-light.png (1440x1000)
- 06-overview-desktop-light.png (1440x1000)
- 07-overview-desktop-dark.png (1440x1000)
- 08-accounts-desktop-dark.png (1440x1000)
- 09-accounts-mobile-light.png (390x844)
- 10-navigation-mobile-light.png (390x844)
- 11-overview-mobile-light.png (390x844)
- 12-login-mobile-light.png (390x844)
- 13-login-mobile-dark.png (390x844)

Inspected screenshots 01,05,07,12,13 directly. Layout matches source findings. Desktop content is an independently scrolling viewport, so fullPage screenshots intentionally show the current viewport of inner content rather than expanding the whole dashboard vertically. Use `.main-shell` scrollTop or locator screenshots for lower dashboard sections.

Visual findings:
- Login desktop left panel occupies about52% width. Its heading and network are vertically centered, branding stays top-left. Large surrounding whitespace is deliberate. Right form is320px wide, vertically centered. Do not add cards around form.
- Mobile login removes entire illustration/story and keeps brand at top, shield/form around middle. At390px it has30px form-side margins,330px form width, proper full-width42px action. Dark mode applies to full canvas and input.
- Header is visually integrated into gray outer canvas, not a standalone white bar. Main panel starts at y60 with8px inset from header and right edge.
- Desktop accounts show four shallow summary cards, a single bordered table panel with tabs/search/filters, then two secondary panels. 180px sidebar is intentionally compact.
- Dark mode preserves pale blue active-nav row with black text, rather than merely tinting nav text. This is a distinctive reference detail.
- Desktop overview uses two larger top summary cards (tokens and credits), four smaller stats, then detailed token and credit sections. Existing target metrics can map to this structure without inventing unrelated reference features.
- Sidebar mobile drawer has208px width and starts below52px header, darkened blurred content backdrop. Mobile page body scrollWidth matched viewport390px in checked overview.

Additional modal/collapsed screenshots were optional and not captured. Native dialog styling and lifecycle are documented in cnb-reference-findings.md. Playwright helper heading exact match must account for inline count text: account heading is `账号池3`, overview is `运行概览实时同步`; use `.main-content h1` or scoped class rather than exact literal text.

