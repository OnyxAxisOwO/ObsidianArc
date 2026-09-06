# Working on Obsidian Arc

Read this before changing anything. It is the shared context — several agents
work on this repository at once, and the defects that reach it are almost never
inside one agent's work. They are between them: two correct halves that
disagree about who owns something. Everything below exists to make those
agreements explicit instead of guessed.

## The governing constraint

**This is a small program.** One Go binary with the frontend embedded, one
database, no sidecars, no cache tier, no broker. When two designs do the same
job, the one with fewer moving parts wins.

Three direct Go dependencies (`modernc.org/sqlite`, `pgx`, `x/crypto`) and
**zero** runtime frontend dependencies. Routing is `net/http`. Migrations are
numbered `.sql` files. There is no ORM, no router, no logging framework and no
config library, and none of those is an oversight.

**Do not add a dependency.** If a task seems to need one, that is the moment to
stop and say so, not the moment to run `go get` or `npm install`.

## Before you say you are done

```bash
make test     # go vet, gofmt, go test ./..., tsc --noEmit
```

All four must pass. `make test` is the only reviewer that reads every agent's
output, so it is not optional and it is not something to work around:

- `gofmt` **fails the build** now. Run `make fmt`, do not hand-edit alignment.
- Source is **LF**, enforced by `.gitattributes`. `core.autocrlf` on Windows
  used to rewrite the tree to CRLF, gofmt read that as unformatted, and the
  gate lit up on sixty files at once — which is how a genuinely misformatted
  file went unnoticed inside the noise. Do not reintroduce CRLF.
- A new feature ships with tests. This repository has ~260 of them and every
  concurrency fix carries a test that actually reproduces the race with real
  goroutines. Match that bar.

## Conventions that are already true

### Comments explain *why*, never *what*

This is the single most valuable convention here, and the easiest to erode.

```go
// No client-level Timeout: it would cut a long streamed answer off
// mid-sentence. The request context is the deadline, and it is cancelled
// when the browser goes away.
```

Not `// set up the http client`. A comment that restates the code costs a line
and teaches nothing. A comment that records the reasoning is why someone three
months from now does not undo the decision.

Signs you have drifted: `// Title and subtitle`, `// Main action`,
`// Close button in upper right corner`. Delete those and write the reason, or
write nothing.

Comments are English even though the interface ships in Chinese.

### Check-then-write is a database lock, never a mutex

Any invariant that is read and then written — a per-account cap, a quota, "at
least one administrator" — must hold a **database row lock** across both. A
`sync.Mutex` only covers one process, and the deployment notes allow a second
instance against one database.

Two spellings, both portable across SQLite and Postgres:

```go
// Per-account invariants: lock the owner's row.
tx.Exec(ctx, `UPDATE users SET updated_at = updated_at WHERE id = ?`, userID)

// Instance-wide invariants: upsert a known settings row without changing it.
tx.Exec(ctx,
    `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
     ON CONFLICT (key) DO UPDATE SET updated_at = settings.updated_at`, ...)
```

Live examples: `apikey.Store.Issue`, `conversation.Store.Upload`,
`auth.Service.Register`, `admin.lockAdminPopulation`. Use one of those two
spellings rather than inventing a third.

### A transaction never spans a provider call

Short transactions only — read rows, write rows, commit. The chat gateway does
one transaction before the provider call and one after, never across it. A
transaction held open for the length of a generation holds a connection for
minutes.

### Cancellation is the request context; persistence outlives it

Stop closes the browser's connection → cancels `r.Context()` → cancels the
outbound provider call, so it stops generating and stops billing. There is no
stop endpoint and no registry of in-flight requests.

The save afterwards runs on `context.WithoutCancel` — the partial answer is
what the user read and the tokens were spent either way. Same for quota
refunds and attachment cleanup.

### Database queries are dialect-free

Every query is written with `?` placeholders and goes through
`database.Queryer`, which rebinds per dialect. Identifiers are ULIDs and
timestamps are epoch milliseconds, so nothing depends on a sequence or a
timestamp type. `%BLOB%` is the one type token.

Store methods take a `database.Queryer` as their second argument so the same
method works standalone (`nil`) or inside a transaction. Do not write a second
`…Tx` copy of a query.

`internal/database/portability_test.go` rejects engine-specific SQL in
migrations. If it fails, the migration is wrong, not the lint.

### No `innerHTML`

There is exactly **one** assignment in the project — `landing/landing-page.ts`,
where operator markup is parsed inside a detached `<template>` and rebuilt from
a strict allowlist before anything is attached. Everything else builds DOM
through the helpers in `ui/dom.ts`.

If `grep -rn innerHTML web/src/` ever returns a second assignment, that is a
bug, not a shortcut.

### Every user-facing string goes through `t()`

`web/src/i18n.ts`: `const en = {...} as const` is the source of truth,
`type StringKey = keyof typeof en`, and `const zh: Record<StringKey, string>`.
A key added to `en` and forgotten in `zh` is a **`tsc` error**, not a silent
English fallback. Never loosen that type to make a build pass.

Deliberately English: API values and enums (`'openai'`, `'5h'`), protocol names
shown on purpose (`reasoning_effort`, `Base URL`), format placeholders
(`'sk-…'`), and the language picker's own `English` / `中文`.

Module-level label tables evaluate at import time, before the language is
known. Store `StringKey`s and resolve at render, as `PAGES` in
`admin/admin-page.ts` does.

### Interface language

Every surface comes from the `--ai-*` tokens in `web/src/styles/chat.css`. No
new colour, radius or easing, and **no token hardcodes a hue** — the accent
colours the whole interface, not just the controls on it.

- Side panels are **columns, not overlays**: another rounded 18px card in the
  same flex row, sliding in by margin so the list beside it narrows.
- **No outlines** on inputs, selects or buttons. A fill marks a control.
  Buttons are pills (`999px`), fields are `11px` on `--ai-field-bg`, focus is a
  ring only.
- Scrollbars are thin, inside the container, no track, no arrows.
- Nothing destructive may depend on `window.confirm()` — it returns `false`
  immediately in some browsers and in the preview pane. Use `confirmable()` in
  `ui/dom.ts` or `PanelOptions.destructive`.

Before adding a surface, check whether the chat already solves the same problem
and reuse that shape.

## Shared ground — change with care

These files are used by everything, and they are where cross-agent bugs land.
Read the whole file before editing, and do not reach into another module's DOM
or state from them:

```
web/src/ui/panel.ts   web/src/ui/dom.ts   web/src/router.ts
internal/httpx/       internal/database/  internal/server/server.go
```

A worked example of the failure they invite: the router used to remove a
modal's overlay node directly. The node was only half of that modal — the rest
was a `document` key handler and a module-level reference — so Escape kept
firing from unrelated screens. The fix was `onBeforeRender`, a registry the
modal subscribes to, so each thing dismisses *itself*. Prefer that shape.

## Known unverified ground

**Docker and PostgreSQL have never been run.** Neither is installed on the
build machine. Both paths were written and reviewed, and the Postgres schema is
covered by the portability lint plus an integration test that runs when
`OBSIDIAN_TEST_POSTGRES_DSN` is set — but nothing has executed them.

Do not quietly claim either works. The README says so explicitly; keep it that
way.

## Measurements are claims

`README.md` and `docs/ARCHITECTURE.md` carry a table of measured costs. If a
change moves one of those numbers, re-measure and update it in the same change.
They drifted to nearly double once because nobody re-ran the build.

Current: 16.9 MB binary, 89.0 kB gzipped frontend — the bundle is **over** its
stated < 80 kB target and still growing, because there is no code splitting and
the front door ships the whole admin backoffice, both language tables, and now
a LaTeX renderer.

## Versions

`yyyy.MM.dd.HH.mm.ss`, UTC, zero-padded, stamped by the Makefile. They sort
chronologically as plain text and need no tag or counter.
