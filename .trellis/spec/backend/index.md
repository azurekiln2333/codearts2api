# Backend Development Guidelines

> Best practices for backend development in this project.

---

## Overview

This directory contains guidelines for backend development. Fill in each file with your project's specific conventions.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Module organization and file layout | To fill |
| [Database Guidelines](./database-guidelines.md) | ORM patterns, queries, migrations | To fill |
| [Error Handling](./error-handling.md) | Error types, handling strategies | To fill |
| [Quality Guidelines](./quality-guidelines.md) | Code standards, forbidden patterns | To fill |
| [Logging Guidelines](./logging-guidelines.md) | Structured logging, log levels | To fill |
| [Chat Request Lifecycle](./request-lifecycle.md) | Admission, fairness, ownership, streaming deadlines, SSE completion, and refresh/reload contracts | Implemented |
| [Request Audit](./request-audit.md) | Bounded metadata, audit API/UI, storage limits, recovery state and build diagnostics | Implemented |
| [Embedded Admin Panel](./admin-panel.md) | Workspace layout, navigation/base paths, UI data contracts and real-browser verification | Implemented |

## Pre-Development Checklist

For chat, pool, streaming, or related config changes, read
[Chat Request Lifecycle](./request-lifecycle.md) and the active task's context before
editing. For audit, account diagnostics or build identity, also read
[Request Audit](./request-audit.md). Other guideline pages remain bootstrap placeholders.
For embedded panel layout, navigation or controls, also read
[Embedded Admin Panel](./admin-panel.md).

## Quality Check

For lifecycle changes, verify the actual HTTP route and cross-layer configuration,
not only selection helpers. Run the full Go test suite, race detector, vet, build,
and diff check; include new untracked source/tests in review. Follow the concrete
boundary assertions listed in the lifecycle spec.

---

## How to Fill These Guidelines

For each guideline file:

1. Document your project's **actual conventions** (not ideals)
2. Include **code examples** from your codebase
3. List **forbidden patterns** and why
4. Add **common mistakes** your team has made

The goal is to help AI assistants and new team members understand how YOUR project works.

---

**Language**: All documentation should be written in **English**.
