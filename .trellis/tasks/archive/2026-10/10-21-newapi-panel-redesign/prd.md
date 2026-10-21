# Admin panel redesign using local New API style

## Goal

Replace the crowded single-page admin panel with a clear workspace inspired by the user's local New API project, making account operations and request investigation easier to find and read.

## Background and authorization

The user explicitly requests the visual style and layout architecture of the New API folder on drive D. The resolved reference is D:/My2KProjects/new-api, whose current frontend uses React/TypeScript with a sidebar workbench and shared UI primitives. This project serves one embedded HTML file through internal/server/panel.go. On 2026-10-21 the user answered “创建任务并继续实现（推荐）”, explicitly authorizing task creation and continued implementation. This is a presentation/navigation redesign.

## Requirements

- UI-R1: Follow the local reference's sidebar, compact header, neutral surfaces, restrained borders, typography, spacing, table density and active navigation treatment. Preserve CodeArts2API branding.
- UI-R2: Give overview, accounts, request audit, models and runtime/configuration distinct navigable views. Keep page title, description and contextual actions clear. Support desktop and narrow mobile layouts, keyboard navigation and visible focus.
- UI-R3: Retain API-key login/logout, OAuth account addition/import/polling, refresh, account validation/sign-in/keepalive/reload, enable/disable/clear-cooldown, all build/runtime details, model display and audit filtering/pagination/loss counters.
- UI-R4: Preserve actual data semantics: current schedulability differs from failure history; unknown build/time/status stays unknown; committed HTTP 200 may still accompany a failed stream. Any aggregate displayed must be computed from existing data with its scope labeled honestly.
- UI-R5: Keep existing admin APIs, service key storage, credential/state formats, static embedding and Docker deployment compatible. Assets and styles must work without a frontend build tool or external CDN.
- UI-R6: Reuse existing API calls, escaping, account/action and audit logic. New navigation and display helpers must preserve base-path support, default display, loading/empty/error states and sanitized metadata.

## Acceptance criteria

- [x] The panel visibly follows the reference's workspace shell, restrained theme and table/card treatment, replacing the current floating gradient/glass layout.
- [x] Every existing function remains reachable in the appropriate view, including OAuth and request-audit diagnostics.
- [x] Navigation changes views reliably; reload/deep links remain valid, mobile navigation is operable and long account/model/error fields do not cause whole-page horizontal overflow.
- [x] Login, all views, filters, pagination, account controls, modal dismissal and keyboard use are checked against isolated fixtures. Screenshots are inspected at desktop and mobile sizes if a runnable browser is available; any unavailable visual check is reported honestly.
- [x] Embedded script checks, relevant Go handler tests/build and diff checks pass. No production traffic, credential files or administrator mutation is needed for verification.

## Out of scope

Changes to scheduler/upstream transport, provider entitlements, auth protocol/key persistence, audit schema/storage, production deployment, new backend features, or editing the reference New API project. Copying its React application or introducing its dependency stack is unnecessary for the existing embedded panel.
