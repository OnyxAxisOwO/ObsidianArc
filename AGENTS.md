# Working on Obsidian Arc

## Deployment identity

**Chat and Arc are separate systems.** Chat is the independent chat software;
Arc is the AI chat application. Never treat their services, containers,
ports, data, or deployment directories as interchangeable.

When the user asks to deploy **Arc**, deploy to the Arc Docker Compose project
(`/data/obsidian-arc` on the production host) and operate only the
`obsidian-arc-server` container and its PostgreSQL companion. Build it on the
local machine with `make deploy DEPLOY_HOST=…`, not on the server: the
server's own `docker compose up --build` compiles the frontend and the binary
there, which is minutes on that host and seconds here. `make deploy` ships a
cross-compiled binary and replaces the server container alone.

**Axis AI's production instance runs plugins that are not in this
repository.** They live in `../AxisAI` (`/Users/onyxaxis/Documents/GitHub/AxisAI`),
which lays them over this tree and calls this Makefile, so production is
deployed with `make deploy DEPLOY_HOST=…` *there*. Deploying from here ships
the core alone, which switches those features off under the people using
them; `scripts/deploy.sh` refuses when the running server carries a plugin
the build does not, and `DROP_PLUGINS=1` is for the day one is meant to go.

Do not touch the Chat service (`obsidianchat.service`, its `/opt/obsidianchat`
releases, or its port 8090). When the user asks to repair or deploy **Chat**, operate that
systemd service only; never substitute the Arc binary or Arc container.

Read this before changing anything. It is the shared context — several agents
work on this repository at once, and the defects that reach it are almost never
inside one agent's work. They are between them: two correct halves that
disagree about who owns something. Everything below exists to make those
agreements explicit instead of guessed.

## The governing constraint

**This is a small program.** One Go binary with the frontend embedded, one
database, no sidecars, no cache tier, no broker. When two designs do the same
job, the one with fewer moving parts wins.

Four direct Go dependencies (`modernc.org/sqlite`, `pgx`, `x/crypto` and
`github.com/tetratelabs/wazero`) and four on the frontend: `vue`,
`vue-router`, `@vueuse/core` and `lucide-vue-next`. Routing on the server is `net/http`. Migrations are
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

The same goes for wazero, the Go side's fourth: it is what lets a plugin be
installed from the backoffice without a rebuild, running as WebAssembly in a
sandbox instead of as code with the server's own hands. It is pure Go, so the
binary stays static and cross-compiles, and it costs 2.8 MB of that binary,
also written down in `docs/ARCHITECTURE.md`. A fifth is that conversation
again.

## Plugins: what only some instances want

The core is a general AI chat site. A feature that only one kind of instance
needs — an account field for one community, one operator's self-hosted
risk-control service — is a **plugin** under `plugins/<name>/`, compiled in
by the `plugin_<name>` build tag (`cmd/server/plugin_<name>.go`) and chosen
by `PLUGINS` in the Makefile and the Dockerfile. **This repository ships
none.** An instance's plugins live in its own repository and are laid over
this tree when it is built (Go only lets code inside the tree import
`internal/`, which is why they are laid over rather than imported), so the
seams below are a contract with code that is not here: change one and the
instances that build on it break where this repository's tests cannot see.
`docs/architecture/plugins.md` is the operator's view; this is the rule set.

There is a second kind, installed at run time and needing no build: a
**plugin package** (`.arcx`, see below). It uses the same seams, and the rules
here hold for it unchanged; what differs is where the code comes from.

- **The arrow points one way.** Plugins import the core; nothing under
  `internal/` or in the core frontend names a plugin, its settings, its
  column, its routes or its strings. `internal/server`'s tests build servers
  with no plugin at all, and `core_only_test.go` holds that line. If a grep
  of `internal/` for a plugin's name finds something, that is a bug.
- **A plugin only uses seams a module exports**: `database.Migrate`'s plugin
  directories, `settings.Define`/`AddCaptchaMode`, `user.DefineField`,
  `auth.Service.AddGuard`/`SetFieldRule`, `auth.Handlers.Extend`,
  `oauth.Service.BindSubject`, `admin.Handlers.Mount`, `console.Register`,
  `invite.Handlers.DecorateInvitees`, `plugin.Host.Handle`/`AllowOrigin`. A
  plugin that needs a seam nobody offers adds it to the module — generically,
  named for what it does rather than for the plugin — never a reach into
  internals.
- **Every seam is gated.** A compiled-in plugin is installed, switched and
  removed at runtime (`plugin.Manager`, the `plugin_installs` table), and it
  is set up at boot whatever its state — so everything it attaches is inert
  until the gate says otherwise. Each registration carries its owner
  (`Plugin: Name` on a `settings.Definition`, `user.Field`, `auth.Guard`,
  `admin.Route`, `console.Command`; the name argument of `AddCaptchaMode`,
  `Extend`, `DecorateInvitees`), and the module asks its `plugingate.Gate`
  before running it. A registration that forgets its owner is core and
  always on, which is the bug to look for. A new seam takes the owner and
  asks the gate the same way; the gate is per server, never a package
  global, because the tests build many servers in one process.
- **A plugin with tables ships its undo.** `Purge()` returns the SQL an
  uninstall-with-data runs (indexes before columns, because SQLite refuses to
  drop an indexed column), and the manager forgets the migrations afterwards
  so a reinstall runs them again.
- **Migrations keep their versions when they move.** A migration that leaves
  the core for a plugin keeps its file name, so a database that ran it as
  core does not run it twice; a plugin's new migrations are
  `<name>_NNNN_*.sql`, and a test in the plugin holds the moved ones to their
  old names.
- **Settings keys, columns and tables keep their names** for the same reason.
- **The browser half declares; the core draws.** `web/src/plugins/<name>/<name>.plugin.ts`
  describes fields, guards, settings cards, lists, account actions and its
  own backoffice pages (see `plugins/types.ts`; a card or list placed on
  `plugin:<slug>` lands on that page, drawn by `PluginAdminPage.vue`), and
  the core renders them with its own components. A
  plugin module imports only what the entry already has — `@/api/client`,
  `@/icons`, `@/lib/*`, and `pluginStrings` from `plugins/registry.ts` — never
  `views/admin/`, or the bundler splits a shared chunk out and the file count
  in `test/bundle.test.ts` catches it.
- **A plugin's strings are its own**, in `pluginStrings(en, zh)`, typed the way
  `i18n.ts` is. They never go into the core dictionaries.
- **Tests come with it**, in the plugin's own package: `servertest` builds a
  whole server with the plugin compiled in — `New` with it installed and
  enabled, `NewFresh` with nothing installed, `NewLegacy` as an upgrade from
  before plugins could be switched — and `parity_test.go` holds the browser
  half's setting keys to the ones the plugin defines.

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
web/src/composables/  web/src/stores/     web/src/plugins/registry.ts
internal/httpx/       internal/database/  internal/server/server.go
internal/plugin/
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

### Plugin packages: code that arrives as data

A package is a zip in the `plugin_packages` table: a manifest, a backend
compiled to WebAssembly against `sdk/`, a browser module, migrations and an
undo. `docs/architecture/plugin-packages.md` is the operator's view. The code
is `internal/plugin/arcx` (the format and every check on it),
`internal/plugin/wasm` (the sandbox), and `internal/plugin` (`package.go`
bridges a package to the seams, `hostops.go` is everything a backend can ask
of the server, `attach.go`/`routes.go` put it on and take it off, `packages.go`
installs, updates and removes).

- **A package gets what it declared and nothing else.** Every host operation
  checks the permission it needs, in `hostops.go`, at the call — not at
  install. A new operation names its permission first, is added to the table
  in the operator's document (each permission is a risk the install dialog
  shows), and comes with a test that it is refused without it. `db` is the
  broad one: the schema is not a stable interface, and the dialog says so.
- **No call outlives itself.** A backend call is a fresh module instance with
  a deadline, a memory ceiling and no state that survives; whatever it left
  open — a transaction — is rolled back when it returns. A backend that
  crashes or times out fails that call, and a guard that cannot answer
  refuses. Do not add a way for one call to leave something for the next.
- **Compiled code is a cache, not a resource the server holds.** A backend
  compiles when a call needs it and gives the code back after
  `wasm.Limits.IdleEvict` idle (three minutes): warm, a package is ~60 MB
  resident, and what they serve is used now and then. So nothing may keep a
  compiled module alive beside the backend — no cache that cannot drop an
  entry (`wasm.ShareCompiledCode` is for tests, and only `servertest` calls
  it), no call at boot that compiles what will not be used. An update or a
  removal must give its code back, and a test holds that eviction and calls
  race safely.
- **What a backend chose to say and what it failed to say are kept apart.**
  An `*arc.Error` reaches the client as written, whatever its status — a 503
  because the service it stands in front of is down is an answer. What the SDK
  worded itself (a panic, an error that was not an `*arc.Error`) is marked
  `Internal` and answered with the server's own 500, its text going to the log:
  it may be a database password. Do not widen what is shown by looking at the
  message, and do not narrow it by clamping statuses.
- **The package is never a file.** It is bytes in the database, so a backup
  carries it and a second instance finds it; nothing is unpacked, and a
  removed package leaves nothing behind but the data the operator chose to
  keep. `systembackup.PrepareRestore` is the other half of that: it builds
  the destination's schema from the archive's own `plugin_packages`.
- **The SDK and the host are one contract.** `sdk/` is a separate Go module
  with no dependencies, importable by anyone writing a plugin; changing a
  host operation or an envelope changes what every installed package does, so
  it moves `arcx.APILevel` (manifests say what they `requires`).
- **The browser half runs with the page's authority.** `web/ui.js` is
  imported only when the plugin is enabled, and it can call whatever the
  signed-in user can; the install dialog says so. `PluginHost` therefore
  hands it nothing the page does not already have, and adds nothing to it
  lightly.
- **Tests build a real package.** `pkgtest.Build` compiles one from source
  the way an author would; `internal/plugin/packages_test.go` installs it into
  whole servers and drives it over HTTP. A test that never reaches the
  WebAssembly proves nothing about a package.

## Measurements are claims

`README.md` and `docs/ARCHITECTURE.md` carry a table of measured costs. If a
change moves one of those numbers, re-measure and update it in the same change.
They drifted to nearly double once because nobody re-ran the build.

Current: 25.8 MB binary (this repository ships no plugin; 2.8 MB of it is
the plugin runtime); 229.20 kB on the wire to open the chat, against a target
of 135. The target used to be 80 and the figure used to be 59.5;
adopting Vue moved both, and `docs/ARCHITECTURE.md` says so rather than
quietly restating a target the build cannot meet.

The backoffice, the terminal, the Chinese dictionary, the LaTeX renderer and
the public front page are separate chunks, fetched only by the readers who
need them — so a static import reaching into `views/admin/`,
`views/TerminalPanel.vue`, `terminal/`, `views/FrontPage.vue`, `i18n.zh` or
`chat/math` from the main graph silently undoes one of those
splits. `web/src/lib/format.ts` holds
`formatUptime` for exactly that reason: one import of one four-line helper
used to pull the whole backoffice back into the main bundle.

`test/bundle.test.ts` asserts the build produces exactly seven files, plus one
per directory under `web/src/plugins/` — each plugin's own chunk. Route-level
lazy loading produces a dozen and is switched off for everything but the
backoffice, the terminal and the front page; if that count changes, it should be because
somebody decided it should.

Responses are compressed by `httpx.Compress`, on an allowlist of content
types. Adding `text/event-stream` to it would buffer streamed answers into
silence, so the list is the one place to be careful.

## Versions

`vyyyy.MM.dd.HH.mm.ss`, UTC, zero-padded, stamped by the Makefile. They sort
chronologically as plain text and need no tag or counter. The `v` is there so
a version reads as one anywhere it appears on its own.
