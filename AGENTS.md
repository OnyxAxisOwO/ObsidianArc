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
four on the frontend: `vue`, `vue-router`, `@vueuse/core` and
`lucide-vue-next`. Routing on the server is `net/http`. Migrations are
numbered `.sql` files. There is no ORM, no logging framework and no config
library on one side, and no component library, no CSS framework and no
state-management library on the other. None of those absences is an oversight.

**Do not add a dependency.** If a task seems to need one, that is the moment to
stop and say so, not the moment to run `go get` or `npm install`.

The frontend's four arrived at once, deliberately, when the interface moved
from hand-written DOM calls to Vue — and they cost about 45 kB on every first
paint, which is written down in `docs/ARCHITECTURE.md` rather than absorbed
quietly. That was a decision, not a precedent: a fifth is the same
conversation the first four were.

## Before you say you are done

```bash
make test     # go vet, gofmt, go test ./..., vue-tsc --noEmit, vitest
```

All of it must pass, and `.github/workflows/ci.yml` runs it again on every
push — on Linux, against a real PostgreSQL, and building the Docker image. It
is the only reviewer that reads every agent's output, so it is not optional and
it is not something to work around:

- `gofmt` **fails the build** now. Run `make fmt`, do not hand-edit alignment.
- The frontend typecheck is `vue-tsc`, not `tsc`: templates are checked too,
  so a prop that does not exist is a build failure rather than an `undefined`
  discovered at runtime.
- Source is **LF**, enforced by `.gitattributes`. `core.autocrlf` on Windows
  used to rewrite the tree to CRLF, gofmt read that as unformatted, and the
  gate lit up on sixty files at once — which is how a genuinely misformatted
  file went unnoticed inside the noise. Do not reintroduce CRLF.
- A new feature ships with tests. This repository has ~280 of them and every
  concurrency fix carries a test that actually reproduces the race with real
  goroutines. Match that bar.
- A new `/api/admin` endpoint must be added to the route list in
  `TestAdminRoutesRequireAnAdministrator`. That test counts the table in
  `admin.Routes` and fails when the two disagree, so it will tell you.

The stylesheet for everything that is not the chat is
`web/src/styles/_surfaces.scss`. It was called admin.css; it holds
`.oa-panel`, `.oa-field`, `.oa-table` and `.oa-icon-btn`, which the settings,
keys and About screens all use, so do not treat it as the backoffice's private
file.

All five partials arrived as the standalone build's stylesheets, renamed and
not reformatted: the port was meant to be pixel-for-pixel identical, and
re-indenting five thousand declarations into nested Sass is a change with
thousands of chances to move something and no way to see that it did. Rules
have been changed since, deliberately and one at a time — keep it that way,
and keep the reason in a comment beside the rule. Components carry no `scoped`
styles either: every class here is global by design, and scoping one would
quietly stop the rules in these files from reaching it.

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

There is exactly **one** assignment in the project — `lib/safe-intro.ts`,
where operator markup is parsed inside a detached `<template>` and rebuilt
from a strict allowlist before anything is attached. Everything else is a Vue
template, which escapes what it interpolates.

`v-html` is the same hole wearing a Vue costume, and there is none in the
project. The transcript in particular does not use it: `chat/markdown.ts`
builds nodes, and `OaMarkdown.vue` appends what it built.

If a grep for `innerHTML` or `v-html` under `web/src/` ever returns a second
hit, that is a bug, not a shortcut.

### Every user-facing string goes through `t()`

`web/src/i18n.ts`: `const en = {...} as const` is the source of truth,
`type StringKey = keyof typeof en`, and `const zh: Record<StringKey, string>`.
A key added to `en` and forgotten in `zh` is a **typecheck error**, not a silent
English fallback. Never loosen that type to make a build pass.

Deliberately English: API values and enums (`'openai'`, `'5h'`), protocol names
shown on purpose (`reasoning_effort`, `Base URL`), format placeholders
(`'sk-…'`), and the language picker's own `English` / `中文`.

Module-level label tables evaluate at import time, before the language is
known. Store `StringKey`s and resolve at render, as `PAGES` in
`views/admin/AdminPage.vue` does.

`t()` is imported from `composables/useI18n`, not from `i18n.ts` directly.
That wrapper reads a version counter, which is what makes every translated
string on screen redraw when the language changes; the raw `t()` renders once
in the old language and stays there.

### Interface language

Every surface comes from the `--ai-*` tokens in `web/src/styles/_chat.scss`.
No new colour, radius or easing, and **no token hardcodes a hue** — the accent
colours the whole interface, not just the controls on it.

- Side panels are **columns, not overlays**: another rounded 18px card in the
  same flex row, sliding in by margin so the list beside it narrows.
- **No outlines** on inputs, selects or buttons. A fill marks a control.
  Buttons are pills (`999px`), fields are `11px` on `--ai-field-bg`, focus is a
  ring only.
- Scrollbars are thin, inside the container, no track, no arrows.
- Nothing destructive may depend on `window.confirm()` — it returns `false`
  immediately in some browsers and in the preview pane. Use `OaConfirmButton`,
  or `OaPanel`'s `destructive-label` and `destructive-confirm`.

Before adding a surface, check whether the chat already solves the same problem
and reuse that shape.

## Shared ground — change with care

These files are used by everything, and they are where cross-agent bugs land.
Read the whole file before editing, and do not reach into another module's DOM
or state from them:

```
web/src/components/   web/src/layouts/    web/src/router/
web/src/composables/  web/src/stores/
internal/httpx/       internal/database/  internal/server/server.go
```

A worked example of the failure they invite: the hand-written router used to
remove a modal's overlay node directly. The node was only half of that modal —
the rest was a `document` key handler and a module-level reference — so Escape
kept firing from unrelated screens. Component teardown answers that whole
class of bug now, and it is most of why the framework is here.

What survives is teardown *ordering*. A template ref is set to null while the
tree is being unmounted, so anything reading one — a `<Teleport>` target, a
`styleTarget` prop — schedules a render on a component that is already going,
where its own setup state has gone and every expression reads `undefined`.
`AppShell.keepBody` and `AdminPage.attachActions` are the shape to copy: set
once, never cleared. `test/boot.test.ts` mounts and unmounts the whole
application precisely to catch this.

## Known unverified ground

**Docker and PostgreSQL now run on every push**, in CI, on Linux — the image is
built and started and asked for its health, and the suite runs against a real
PostgreSQL 16. Neither is installed on the machine most of this was written on,
so locally they are still unexercised; trust the CI run, not your laptop.

What remains genuinely unproven is time. Nothing here has carried real traffic
for a week. Do not write anything into the README that claims otherwise.

## Measurements are claims

`README.md` and `docs/ARCHITECTURE.md` carry a table of measured costs. If a
change moves one of those numbers, re-measure and update it in the same change.
They drifted to nearly double once because nobody re-ran the build.

Current: 17.4 MB binary; 116.8 kB on the wire to open the chat, against a
target of 130. The target used to be 80 and the figure used to be 59.5;
adopting Vue moved both, and `docs/ARCHITECTURE.md` says so rather than
quietly restating a target the build cannot meet.

The backoffice, the Chinese dictionary and the LaTeX renderer are separate
chunks, fetched only by the readers who need them — so a static import
reaching into `views/admin/`, `i18n.zh` or `chat/math` from the main graph
silently undoes one of those splits. `web/src/lib/format.ts` holds
`formatUptime` for exactly that reason: one import of one four-line helper
used to pull the whole backoffice back into the main bundle.

`test/bundle.test.ts` asserts the build produces exactly five files. Route-level
lazy loading produces a dozen and is switched off for everything but the
backoffice; if that count changes, it should be because somebody decided it
should.

Responses are compressed by `httpx.Compress`, on an allowlist of content
types. Adding `text/event-stream` to it would buffer streamed answers into
silence, so the list is the one place to be careful.

## Versions

`vyyyy.MM.dd.HH.mm.ss`, UTC, zero-padded, stamped by the Makefile. They sort
chronologically as plain text and need no tag or counter. The `v` is there so
a version reads as one anywhere it appears on its own.
