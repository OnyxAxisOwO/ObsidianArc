# Obsidian Arc — Server Architecture

The plan for turning the current standalone, bring-your-own-key chat surface
into a self-hosted, multi-user AI chat server, without losing the product it
already is.

The governing constraint is stated once here and applies to every decision
below: **this is a small program**. A single Go binary with an embedded
frontend, one database, no sidecars, no Python, no broker, no cache tier. If
two designs do the same job, the one with fewer moving parts wins.

---

## 1. What is here today

| | |
| --- | --- |
| Frontend | Vanilla JavaScript, no framework, no bundler, no build step |
| Module system | 8 UMD-style scripts attached to `globalThis`, loaded by `<script>` tags |
| Styling | 2 hand-written stylesheets driven by `--ai-*` CSS custom properties |
| State | In-memory closures; persistence to `localStorage` through a `chrome.storage.local`-shaped adapter |
| Networking | The browser calls Anthropic / OpenAI-compatible endpoints directly, with the user's own key |
| Dev server | `python -m http.server` |
| Dependencies | None. Zero runtime deps, zero dev deps |
| Tests / lint / types | None |

| File | Lines | Role |
| --- | --- | --- |
| `src/chat.js` | 1013 | The chat surface: rail, transcript, composer, streaming, editing, attachments |
| `src/provider.js` | 804 | Anthropic + OpenAI-compatible clients, SSE parsing, config, key encryption |
| `src/workspace.js` | 796 | Header bar (model chip, theme, settings) + settings drawer |
| `src/markdown.js` | 435 | XSS-safe Markdown renderer that never touches `innerHTML` |
| `src/store.js` | 255 | Conversation model, normalization, caps |
| `src/color-utils.js` | 140 | Accent-color light/dark contrast math |
| `src/adapters.js` | 94 | `localStorage` storage shim |
| `src/image.js` | 76 | Client-side image downscale before attachment |
| `src/chat.css` | 1160 | Design tokens + the entire chat visual language |
| `src/workspace.css` | 534 | Header, chip menu, drawer, form controls, color picker |

### The important structural fact

`chat.js` already has exactly the seam this migration needs. It knows nothing
about providers. Its host hands it two functions:

```js
getStatus()  // -> { configured, vision, streaming }
send({ turns, signal, onReply, onThinking })  // -> { reply, thinking, stats, ... }
```

`workspace.js` is the reference host that implements those against
`provider.js`. Swapping "the browser calls Anthropic with the user's key" for
"the browser calls our server, which calls Anthropic with the admin's key" is
a **replacement of that host**, not a rewrite of the chat. The design language
survives because the file that draws it is barely touched.

---

## 2. UI architecture worth naming

These are the things that make the product feel the way it does. Every one of
them is preserved.

- **Layout** — a rounded 18px container per surface; a 260px conversation rail
  that slides out by negative margin (contents never reflow mid-animation);
  a single 680px reading column centred in the transcript.
- **Message shape** — the user's words in a violet bubble with an asymmetric
  `16px 16px 4px 16px` radius; the assistant's set as plain prose on the page,
  so a long answer reads as a document rather than a chat log.
- **Reasoning** — a quiet `<details>` with a left rule, auto-scrolled while
  live, collapsed once done.
- **Stats line** — the quietest thing in the message: duration, streamed /
  one-shot, first-token latency, token counts, tok/s.
- **Composer** — a 26px pill on `--ai-surface-container-high`, attach button
  on the left, send/stop morphing on the right, focus ring on the whole pill.
- **Model selector** — a header chip that opens a menu of `title` + `sub`
  rows, with a "manage models" footer that jumps to settings.
- **Settings** — a right-side drawer, 420px, sections separated by uppercase
  subheads, with an accent picker of 10 dots plus a custom hex row.
- **Motion** — `aiChatSwitch` on conversation change and on a just-sent
  message, a caret that blinks at the live end of a streaming answer, a
  0.34s `cubic-bezier(0.2, 0, 0, 1)` rail slide, and a full
  `prefers-reduced-motion` path that turns all of it off.
- **Theme** — light values on `:root`, dark by `prefers-color-scheme`, forced
  by `[data-theme]`; one accent hue clamped per scheme by `color-utils.js` so
  a single pick stays readable on both white and near-black, and tinted into
  `--ai-state-hover` so the whole interface reads as being *in* that color.

---

## 3. Keep / refactor / rewrite

### Keep, essentially verbatim

| What | Why |
| --- | --- |
| `chat.css`, `workspace.css` | This *is* the design language. Ported to the new build unchanged except for new selectors. |
| `markdown.js` | Genuinely good: token tree + `createElement`, no `innerHTML` path, images downgraded to links, `javascript:` links rendered as text. Retype as TS, keep the logic. |
| `color-utils.js` | Small, correct, and the reason one hue works in both schemes. |
| `image.js` | Client-side downscale is still wanted — it now saves upload bandwidth and server storage instead of `localStorage` quota. |
| `chat.js` render + interaction code | Transcript rendering, editing, regenerate, streaming fast-path, drag/drop, attachments strip, empty state, suggestion chips. |
| `workspace.js` header + drawer chrome | Chip menu, drawer open/close, field/checkbox helpers, accent grid. |

### Refactor

| What | Into what |
| --- | --- |
| `store.js` persistence | Same in-memory conversation shape, but backed by the server API instead of `localStorage`. The normalization caps move server-side, where they belong. |
| `chat.js` state | Still a closure, but with the storage/`persist()` calls replaced by an injected `ConversationStore` port. Its ~15 mutable locals stay — a full re-render of a 680px column is cheap and the `updatePending()` fast-path already handles the streaming case. |
| `provider.js` | Split. The *logic* (message building, adjacent-role merging, `<think>` splitting, SSE framing, usage extraction, error explanation) is ported to Go as the provider adapters. The *browser-side* remnant is a thin SSE client against our own API. |
| `workspace.js` settings drawer | Sections re-scoped: API key / base URL / provider move to Admin; profile, default model, default reasoning, wallpaper move in. |
| `adapters.js` | Shrinks to a preferences shim (theme, accent, wallpaper) — chat history no longer lives in `localStorage`. |
| Global `<script>` + `globalThis` modules | ES modules + TypeScript, bundled by Vite, embedded into the Go binary. |

### Delete

- The direct-to-provider network path and everything that supports it:
  `anthropic-dangerous-direct-browser-access`, the AES-GCM key-at-rest toggle,
  the "detect models" call from the browser (moves to Admin, server-side).
- `python -m http.server` as the dev server and `npm start`.
- The `localStorage` conversation store and its size caps.

---

## 4. Target architecture

```
                   ┌─────────────────────────────────────────┐
   browser ────────│ Go binary (single process)              │
                   │                                         │
   SPA  ───────────┤ embed.FS  →  static SPA + index fallback │
   fetch/SSE ──────┤ net/http  →  middleware → module handlers│
                   │                    │                    │
                   │              chat gateway                │
                   │            ┌───────┴────────┐            │
                   │        quota            adapter registry │
                   │            │             ┌────┴─────┐    │
                   │        usage ledger      openai  anthropic
                   │            │                 │      │    │
                   └────────────┼─────────────────┼──────┼────┘
                                │                 ▼      ▼
                          ┌─────┴──────┐      upstream providers
                          │ PostgreSQL │
                          │ or SQLite  │
                          └────────────┘
```

**Modular monolith.** One process, one binary, one database. Modules are Go
packages with an explicit surface, not services. A module owns its tables and
exposes a `Service` struct; other modules depend on the interface they need,
declared on the consumer side.

No Redis, no queue, no worker pool, no cron daemon. The only background
goroutines are: `http.Server`'s own, and a single janitor ticking every 10
minutes to expire sessions and drop stale quota buckets.

### Directory layout

```
cmd/server/main.go            flags/env → wire → serve → graceful shutdown
internal/
  config/      Config struct from env + flags, one source of truth
  database/    open(sqlite|postgres), dialect rebind, tx helper, migrations/*.sql (embedded)
  httpx/       ServeMux wiring, middleware, JSON + SSE helpers, typed errors
  auth/        argon2id, sessions, register/login/logout, RequireUser / RequireAdmin
  user/        user store + service, self-profile handlers
  group/       groups, model permissions, per-group quota policy
  provider/    provider CRUD, API-key encryption, upstream model listing
  model/       model CRUD, capabilities, credit weights, per-user visible list
  adapter/     unified ChatRequest/Event + openai.go + anthropic.go + registry
  chat/        the gateway: permission → quota → adapter → stream → persist → ledger
  conversation/ conversations, messages, attachments (all user-scoped)
  usage/       ledger writes + aggregation queries
  quota/       policy resolution (global→group→user), atomic counters, enforcement
  settings/    global key/value settings
  admin/       admin-only handlers over the modules above
  web/         embed.FS of the built frontend + SPA fallback
web/           Vue 3 + SCSS frontend source, built by Vite
```

Handlers live with their module. There is no `handlers/` dump and no
`repository/service/usecase/entity` ladder — a module is
`store.go` (SQL), `service.go` (rules), `http.go` (transport), `types.go`.

### Dependencies

Go, direct:

| Module | For | Why this one |
| --- | --- | --- |
| `modernc.org/sqlite` | SQLite driver | Pure Go: no cgo, static binary, trivial cross-compile. Behind a `nosqlite` build tag for Postgres-only builds. |
| `github.com/jackc/pgx/v5` | Postgres driver | The standard, and its `stdlib` shim keeps everything on `database/sql`. |
| `golang.org/x/crypto` | argon2id | Official. |

That is the whole list. Routing is `net/http`'s `ServeMux` (Go 1.22 method +
wildcard patterns). Migrations are a 60-line runner over an `embed.FS`. No
ORM, no query builder, no logging framework (`log/slog` is in the standard
library), no config library.

Frontend, runtime:

| Package | For | Why this one |
| --- | --- | --- |
| `vue` | The interface | The screens are a graph of small components with a lot of shared state — a model picker the composer reads, an allowance the composer and the backoffice both draw. The hand-written version redrew whole screens on every change and had a hand-rolled fast path in the transcript to survive it; that fast path is the part that kept going subtly wrong, and it is gone. |
| `vue-router` | Routing | The five panel routes are children of the chat, which is exactly what nested routes are. The hand-written router could not express that and each of those screens re-created the chat behind it instead. |
| `@vueuse/core` | Composables | Listeners, observers and timers that are torn down with the component that made them. Most of the leaks the old code documented in comments are this package's default behaviour. |
| `lucide-vue-next` | Icons | The icon factory and default attributes; the glyphs themselves are still this project's own paths, so the port did not silently redraw forty icons. See `web/src/icons/index.ts`. |

Build: `vite`, `@vitejs/plugin-vue`, `typescript`, `vue-tsc`, `sass`. No
component library, no CSS framework, no state-management library: the
stylesheets are the same hand-written `--ai-*` tokens, and the two stores are
a handful of `ref`s in `stores/session.ts` and `chat/useChat.ts`.

### Performance targets, and what was measured

| | Target | Measured |
| --- | --- | --- |
| Idle resident memory (SQLite, no traffic) | < 30 MB | ~16 MB |
| Cold start to serving | < 100 ms | 28 ms |
| Binary (SQLite + embedded SPA) | < 30 MB | 21.6 MB (17.9 MB `-tags nosqlite`, Linux amd64) |
| Frontend, on the wire | < 135 kB | 208.00 kB to open the chat (170.35 JS + 37.65 CSS) |
| Background goroutines at idle | 1 | 1 |
| Under load, 200 streamed turns at 20 concurrent | — | ~54 MB peak, 11 OS threads |

Remeasured on 2026-09-26 (UTC) for the leaderboard and the front page landing
together, on top of the upstream work that arrived the same day (the login
backgrounds, the site logo, the sliding tabs, the model-name marquee). That
upstream tree already measured 200.05 kB before either of these, well above
the 190.40 kB this table last recorded — the figure had drifted, which is the
failure the rule about re-measuring exists to prevent. Against it, the two
together add 7.95 kB to the first paint: the leaderboard panel is a column
over the chat and is imported statically like every other panel, so its
code, its `en` strings and its stylesheet rules are on the entry by design;
the front page's own code stays in its chunk. Every figure here was measured
from one build of the combined tree, gzip byte counts in decimal kB.

Remeasured later on 2026-09-26 (UTC) for the front page's moving background:
stronger drifting fields, a travelling grid, two layers of rising motes and
a glow that follows the pointer. The stylesheet grew by 0.55 kB, which is on
the first paint for the reason `_front.scss` is — this project ships one
stylesheet — and the `FrontPage` chunk by 0.13 kB to 3.99 kB. Measured the
same way as the entry below, before and after on one tree.

Remeasured on 2026-09-26 (UTC) for the public front page — the `site` landing
mode, which draws the product's own marketing page at the address instead of
the sign-in card. The page itself is a chunk of its own, `FrontPage`, at
3.86 kB: it is a fifth landing mode most instances will not switch on, and no
signed-in account ever sees it, so it has no business on the first paint of
somebody opening the chat. `test/bundle.test.ts` now expects seven build
artifacts rather than six, and asserts that chunk stays out of the entry.

What did land on the first paint is 2.47 kB of JS and 2.60 kB of CSS: the
fifty-six new `en` keys (the English dictionary is also the fallback, so it
cannot be split), the one-line async import in `RootView`, and
`styles/_front.scss`, which is in the single stylesheet this project ships
rather than a chunk of its own. The Chinese dictionary grew by 1.65 kB.

Measured as a delta, not as a total: the working tree carried another agent's
unfinished work at the time, so the figures above are this change's own cost —
two builds of one tree, one with the page in the graph and one with it
stripped out, nothing else differing — added to the total this table already
carried. The binary was not remeasured: Go is not installed on the machine
this was written on, and CI builds it. See "Known unverified ground" in
AGENTS.md.

Remeasured once more on 2026-09-25 (UTC), for the invite-claim flow the first
pass over invite codes below had not yet reached: the existing-account claim
box and its every-N progress line in the settings' invites section, the
admin invites table's partner and claims columns, the new i18n keys both of
those need in `en` and `zh`, and the signed-in-visitor redirect from
`/register?invite=` to the settings claim box. The first paint grew by
1.45 kB (1.43 JS, 0.02 CSS) to the figure above. The backoffice chunk grew
by 1.29 kB to 83.97 kB for the partner/claims columns, and the Chinese
dictionary to 34.80 kB. The binary grew by 61 kB with SQLite (21,430,432
bytes) and 66 kB without (17,719,456) — the larger embedded frontend, Go
1.27.1.

Remeasured for invite codes on 2026-09-25 (UTC): the first paint grew by
3.02 kB (3.01 JS, 0.01 CSS) to the figure above — the invite field on the
sign-up and complete-sign-up cards, which are the first paint for anybody
signed out, and the invites section of the settings, which are columns over
the chat rather than a chunk of their own. The backoffice grew by 3.81 kB to
82.68 kB for its new tab, and the Chinese dictionary to 34.18 kB. The binary
grew by 188 kB with SQLite (21,368,992 bytes) and 184 kB without
(17,653,920), Go 1.27.1.

Remeasured once more on 2026-09-25 (UTC), for the notification bell and its
toasts, the signed-in devices list and the new-device notice: the first
paint grew by 7.27 kB (6.92 JS, 0.35 CSS) to the figure above. All of it is
there on purpose — the toasts and the bell are drawn over the chat for
anybody signed in, and the devices list is part of the security settings,
which are columns over the same page rather than a chunk of their own. The
backoffice grew by 0.74 kB to 78.87 kB and the Chinese dictionary to 32.74
kB. The binary grew by 168 kB with SQLite (21,180,576 bytes) and 168 kB
without (17,469,600), Go 1.27.1.

Remeasured again earlier on 2026-09-25 (UTC), after the backoffice's own code
at its door, the security settings' redesign, the safe-mode fixes and the
configurable browser title and web app manifest: the first paint grew by
1.92 kB (1.37 JS, 0.55 CSS) to the figure above — the new English strings
and the redesigned settings card, mostly; the door and the manifest's form
live in the backoffice chunk, which grew by 2.67 kB to 78.13 kB. The Chinese
dictionary grew to 31.76 kB. The binary grew by 127 kB with SQLite (21,012,640
bytes) and 127 kB without (17,301,664), Go 1.27.1.

Remeasured on 2026-09-25 (UTC) for two-step verification, against the commit
before it rebuilt the same way. That commit was already 0.87 kB above the
figure recorded below — the backoffice's safe mode had not been remeasured —
at 169.79 kB (140.88 JS + 28.91 CSS). Two-step verification adds 6.95 kB to
the first paint: 6.44 kB of JS and 0.51 kB of CSS. All of it sits there on
purpose. The code step is part of the sign-in card, which is the first paint
for anybody signed out; the setup wizard is drawn by the security settings,
by the screen a policy holds an account at, and by the backoffice's gate, and
the first two are in the main graph already; and roughly a third of the JS is
English strings, which stay in `en` because that is the type the Chinese
dictionary is checked against. Splitting the wizard into a chunk of its own
would save a couple of kilobytes and add the seventh file `test/bundle.test.ts`
is there to question — a trade not taken for a screen people open once. The
QR code is drawn by the server, in `internal/qr`, so no encoder rides on the
first paint at all. The backoffice chunk grew by 1.76 kB to 75.46 kB and the
Chinese dictionary by 2.02 kB to 30.78 kB.

The binary grew by 197 kB with SQLite and 193 kB without it (20,885,664 and
17,174,688 bytes, Go 1.27.1): the TOTP and QR packages, the second sign-in
step and its policy, the terminal's `2fa` commands, and the larger embedded
frontend. No dependency was added — TOTP is `crypto/hmac` and `crypto/sha1`,
the secrets are sealed with the existing `internal/secret`, and the SSH second
step is `golang.org/x/crypto/ssh`'s keyboard-interactive, already linked.

The 2026-09-24 stream handoff fix adds 0.03 kB of gzipped chat JavaScript;
the CSS and separately loaded chunks are unchanged. The wire figures above
and below include that change.

Remeasured again later on 2026-09-24 (UTC), when the terminal moved out of
the backoffice into every account's menu. The first paint grew by 0.09 kB:
the menu entry and its icon, the route, and the flag that decides whether to
show it; the CSS shrank by 0.04 kB because the terminal's narrow-screen
gutter went with the backoffice layout it belonged to. The terminal itself —
7.31 kB — became a chunk of its own rather than landing on the first paint:
every account may open it, but few ever will, so it is fetched by the reader
who clicks it and by nobody else. That is the sixth file `test/bundle.test.ts`
now expects. The backoffice chunk lost the same code and fell by 6.63 kB to
72.51 kB. The binary grew by 37 kB with and without SQLite alike: the new
commands, the group switch and its migration. Built with Go 1.27.1
(20,676,768 bytes with SQLite; 16,969,888 without).

Earlier the same day, after the usage analytics (who uses which
model, a user × model cross-table, a weekday × hour heatmap) and a pass over
the alignment of the backoffice's and the chat home's controls. Measured
against the commit before that work, which was itself already 1.19 kB above
the figure below — three merged contributions had not been remeasured — the
first paint grew by 4.04 kB: 1.45 kB of JS, mostly the English strings the new
screens need, and 2.59 kB of CSS. The CSS is the honest cost of an unsplit
stylesheet: the charts' rules are admin-only, and they reach every reader
because there is one stylesheet. The backoffice chunk grew by 9.19 kB to
79.14 kB and the Chinese dictionary by 1.11 kB to 28.77 kB, neither of which a
non-administrator reading in English fetches. The binary grew by 86 kB, the
same 86 kB with `-tags nosqlite`: the new queries and the larger embedded
frontend, no new dependency. Built with Go 1.27.1 (20,639,904 bytes with
SQLite; 16,928,928 without).

The bundle and binaries were remeasured on 2026-09-20 (UTC), after three
contributions landed together: importing an API key into CC Switch in one
click, the Image Lab's reference images and history gallery, and the two usage
screens re-reading themselves while open. The chat payload is 28.57 kB above
the existing target; that target is unchanged. It grew by 3.22 kB over the
figure recorded earlier the same day — 2.91 kB of JS and 0.31 kB of CSS.

Most of that is the Image Lab. Its gallery, lightbox and reference-image
handling sit on the first paint because the panel already did, and this is the
screen where they belong; route-level splitting is off for everything but the
backoffice, and turning it on for one view is the careful job named below.
The other two are nearly free by comparison: the CC Switch link builder is one
37-line module with no runtime of its own, and the usage refresh reuses
`useIntervalFn`, which `@vueuse/core` was already carrying. The backoffice
chunk grew by 0.12 kB and the Chinese dictionary by 0.51 kB, which nobody
reading in English fetches.

The binary grew by 49 kB, and by the same 49 kB with `-tags nosqlite`, which
is how you can tell it is this project's own code rather than anything pulled
in underneath it: the image-generation store, its migration and the multipart
handling for reference images. No dependency was added on either side — the
same three direct Go dependencies as before, and the same four on the
frontend. The identity tokens are still signed RS256 with `crypto/rsa`,
`crypto/x509` and `encoding/pem` from the standard library, because the point
of speaking OpenID Connect is that software written by somebody else can
verify one without being configured specially.

The earlier administrative console — the terminal section and its SSH
transport — is also admin-only code, and all 8.35 kB of its frontend landed in the backoffice
chunk, which nobody who cannot open it ever fetches. What reached the first
paint is the two things this project deliberately does not split: its English
strings, because `en` in `i18n.ts` is the type that makes the Chinese
dictionary provably complete, and its stylesheet, because there is one. Those
are 0.97 kB of JS and 0.86 kB of CSS. Splitting either is the careful job named below, not
something to do while adding a screen.

The console had added 1.07 MB, all of it `golang.org/x/crypto/ssh`. That is a
package of a module `go.mod` already required for Argon2id, so it adds no
dependency — but it is a megabyte of code that only runs when
`OBSIDIAN_SSH_ADDR` is set, and it is linked in either way.
Binary sizes use Go 1.27.0,
Linux amd64, `-trimpath -ldflags "-s -w"`; transfer sizes are gzip-compressed
JS and CSS in decimal kB, with totals rounded after summing. Binary sizes
are decimal MB (20,476,064 bytes with SQLite; 16,769,184 bytes without it).

The target moved with the interface. It was < 80 kB while the frontend was
hand-written DOM calls, and 71.6 kB against it; adopting Vue put roughly 45 kB
of framework on the first paint and no amount of splitting takes that back,
because it is needed to draw anything at all. Naming the new figure is more
honest than leaving a target the build cannot meet — the number to watch now
is whether this project's own code grows, not whether the framework does. It
moved from 130 to 135 kB when the measured bundle reached 131.05 kB rather
than leaving a target the shipped usage controls no longer met.

The five pieces most people never need are split off: the administration
backoffice, the terminal, the Chinese dictionary, the LaTeX renderer, and the
public front page.
What each reader actually downloads:

| | gzipped |
| --- | --- |
| English, not an administrator | 208.00 kB |
| Chinese, not an administrator | 245.71 kB |
| …and a conversation containing a formula | 249.34 kB |
| Chinese administrator, backoffice open | 332.84 kB |
| Anybody, once they open the terminal | +7.20 kB |
| A visitor to an instance whose front door is the front page | +3.99 kB |

Route-level splitting would shave the first paint further and is deliberately
switched off for everything but the backoffice and the terminal: /settings, /keys, /usage and
/about are columns over a chat that is already on screen, so a chunk each buys
a round trip in the middle of a click to defer bytes the reader was going to
fetch anyway.
The stylesheet is not split: `admin.css` carries the shared design system —
panels, fields, tables, the About screen — and separating the part that is
genuinely admin-only is a different, more careful job.

These are transferred sizes. The server compresses its own responses, because
the deployment this document assumes has nothing in front of it to do that
instead: 136 kB of JavaScript leaves as 47, and 70 kB of CSS as 13. The
allowlist in `httpx.Compress` is what keeps the event stream out of it — a
streamed answer pushed through a compressor arrives when the buffer fills
rather than when the model produced a word, and nothing in any log would say
so.

Connection pools: SQLite 4, Postgres 10. Neither is a bottleneck at this
scale — a turn spends its time waiting on a provider, not on the database.

---

## 5. Database schema

Portable between SQLite and PostgreSQL by construction:

- **IDs are `TEXT`** — ULIDs generated in Go. Lexicographically sortable, so
  `ORDER BY id` is chronological and index-friendly; no `SERIAL` vs
  `AUTOINCREMENT` split, and no enumerable integers to walk in an IDOR probe.
- **Timestamps are `BIGINT`** — Unix epoch milliseconds. Identical semantics
  in both engines, and window queries are plain integer comparisons.
- **Booleans are `BOOLEAN`** (SQLite stores 0/1; the driver handles it).
- **Money-ish values are `DOUBLE PRECISION` / `REAL`** for credits.
- No engine-specific types, no `JSONB`, no arrays. JSON payloads are `TEXT`.

Migrations are numbered `.sql` files applied in order inside a transaction,
tracked in `schema_migrations`. Nothing mutates schema at boot beyond running
pending migrations.

### Tables

```
users                id, username, username_lower*, email, email_lower*,
                     password_hash, nickname, avatar, bio, role, group_id,
                     status, created_at, updated_at, last_login_at
sessions             id (= sha256 of the cookie token), user_id, created_at,
                     expires_at, last_seen_at, ip, user_agent
groups               id, name*, description, is_default, allow_all_models,
                     sort_order, created_at, updated_at
group_models         group_id, model_id                       (composite PK)
quota_policies       id, scope ('global'|'group'|'user'), scope_id,
                     rpm, tpm,
                     window_5h_enabled, window_5h_requests,
                       window_5h_tokens, window_5h_credits,
                     window_1w_… , window_1m_… ,
                     updated_at                                UNIQUE(scope, scope_id)
providers            id, name, kind ('openai'|'anthropic'), base_url,
                     api_key_enc (BLOB), api_key_hint, headers_json,
                     anthropic_version, reasoning_style, timeout_seconds,
                     enabled, sort_order, created_at, updated_at
models               id, provider_id→providers, model_id, display_name,
                     description, avatar, enabled, sort_order,
                     supports_reasoning, supports_images, supports_vision,
                     supports_streaming, supports_system_prompt, supports_tools,
                     context_window, max_output_tokens,
                     request_weight, input_token_weight,
                     output_token_weight, reasoning_token_weight,
                     created_at, updated_at        UNIQUE(provider_id, model_id)
conversations        id, user_id, title, model_id, pinned,
                     created_at, updated_at, deleted_at
messages             id, conversation_id, user_id, seq, role, content,
                     reasoning, error, model_id, provider_id,
                     stats_json, created_at       UNIQUE(conversation_id, seq)
attachments          id, user_id, message_id, kind, mime, width, height,
                     size, data (BLOB), created_at
usage_records        id, user_id, group_id, provider_id, model_id, model_ref,
                     conversation_id, message_id, request_id*,
                     input_tokens, output_tokens, reasoning_tokens,
                     total_tokens, credits, status, error_code,
                     started_at, finished_at, duration_ms
usage_counters       scope_key, window_kind, window_start,
                     requests, tokens, credits     PK(scope_key, window_kind, window_start)
settings             key, value, updated_at
user_preferences     user_id, data (JSON TEXT), updated_at
```

`*` = unique index.

Indexes that matter: `usage_records(user_id, started_at)`,
`usage_records(model_id, started_at)`, `messages(conversation_id, seq)`,
`conversations(user_id, updated_at DESC)`, `sessions(expires_at)`.

### Notes on specific choices

- **`username_lower` / `email_lower`** rather than `CITEXT` — Postgres-only
  types would break SQLite parity.
- **Attachments as BLOB in the database.** One database is the whole
  deployment story; an object store is a second thing to run. Images are
  already downscaled client-side to under a megabyte, and the read path is a
  single ownership-checked row fetch. The store is behind a
  `BlobStore` interface, so an S3/disk backend later is one implementation,
  not a migration.
- **`supports_images` vs `supports_vision`** are both kept as requested:
  `supports_images` = the endpoint accepts image parts in the request;
  `supports_vision` = the model actually reasons over them. The composer
  enables attachments on the first; the model list badges the second.
- **`reasoning_style`** on the provider is what keeps the reasoning
  translation out of the gateway (see §6).

---

## 6. Provider adapter

One request shape in, one event stream out. The gateway never learns a
provider's name.

```go
package adapter

type ChatRequest struct {
	Model       ModelSpec   // upstream id + capabilities + limits
	System      string
	Messages    []Message   // Role + []Part{Text|Image}
	Temperature *float64
	MaxTokens   int
	Reasoning   Reasoning   // { Enabled bool; Effort "low"|"medium"|"high" }
	Stream      bool
	Extra       map[string]any
}

type Event struct {
	Type  EventType // Delta | ReasoningDelta | Usage | Done | Error
	Text  string
	Usage *Usage    // Input/Output/ReasoningTokens
	Err   error
}

type Adapter interface {
	Kind() string
	Chat(ctx context.Context, p Provider, req ChatRequest, sink func(Event) error) (Result, error)
	ListModels(ctx context.Context, p Provider) ([]RemoteModel, error)
}

var registry = map[string]Adapter{"openai": openAI{}, "anthropic": anthropic{}}
```

The gateway does `registry[provider.Kind].Chat(...)`. There is no
`if kind == "openai"` anywhere outside `internal/adapter`.

### Reasoning translation

The frontend only ever sends `{ enabled, effort }`. Each adapter turns that
into whatever its wire format wants, selected by the provider's
`reasoning_style` so a new upstream quirk is a config value, not a code
branch:

| `reasoning_style` | Emitted |
| --- | --- |
| `anthropic` | `thinking: {type: "enabled", budget_tokens: N}`, N from effort (2048 / 8192 / 24576), clamped below `max_tokens`; temperature omitted, which the API requires |
| `openai_effort` | `reasoning_effort: "low"\|"medium"\|"high"` |
| `openrouter` | `reasoning: {effort: "..."}` |
| `qwen` | `enable_thinking: true` |
| `none` | nothing; the model reasons or does not on its own |

Reasoning **output** arrives in three shapes, all normalised to
`ReasoningDelta` events: Anthropic's `thinking_delta`, the
`reasoning` / `reasoning_content` fields OpenAI-compatible servers have
settled on, and inline `<think>…</think>` at the head of the content. The
third needs a splitter that tolerates a stream stopping mid-tag — the
existing `splitThinking()` in `provider.js` already does this correctly and
is ported to Go as-is.

Reasoning and answer are stored in separate columns (`messages.reasoning`,
`messages.content`).

### What else moves from `provider.js` into Go

- Endpoint resolution from a partial base URL (`…`, `…/v1`, `…/v1/messages`).
- Base-URL validation: https only, except loopback; no embedded credentials.
- Adjacent same-role merging and leading-assistant trimming (an edited
  transcript can produce either, and both providers reject it).
- Image de-duplication across turns, so a picture attached three turns ago is
  sent once.
- Usage extraction (`input_tokens`/`output_tokens` vs
  `prompt_tokens`/`completion_tokens`, plus `x_groq.usage`).
- Error explanation: an auth failure names the provider and URL it was sent
  to; an image rejection on 400/415/422 says the model probably cannot read
  images rather than quoting the wire error.

---

## 7. Chat, streaming, cancellation

```
POST /api/chat            (SSE response)
  { conversation_id?, model_id, content, attachment_ids[],
    reasoning: {enabled, effort}, regenerate_from_message_id? }
```

1. `RequireUser` → the session's user; `status = 'disabled'` is rejected here.
2. Resolve the model **and** check the user's group may use it. The frontend's
   model list is a convenience; this check is the authority.
3. `quota.Reserve` — atomic, see §9. A rejection is a `429` carrying which
   window tripped and when it resets.
4. Load the transcript (`WHERE conversation_id = ? AND user_id = ?`), append
   the user message, create the conversation if this is the first turn.
5. `adapter.Chat` with `ctx` derived from `r.Context()`.
6. Frames are written to the client as they arrive and flushed immediately:
   `message_start`, `delta`, `reasoning`, `usage`, `done`, `error`.
7. On completion **or** cancellation: persist the assistant message, write the
   usage ledger row, settle the quota reservation.

**Stopping** is the client closing the stream. That cancels `r.Context()`,
which cancels the outbound `http.Request`, which closes the upstream
connection — the provider stops generating and stops billing. No stop
endpoint, no request registry, no shared state. Step 7 runs on
`context.WithoutCancel(ctx)` so a cancelled turn still saves the partial
answer the user read and still records the tokens it cost.

SSE rather than WebSocket: the traffic is one-directional, it survives every
proxy unchanged, and cancellation is just closing the connection.

The client uses `fetch` + a `ReadableStream` reader rather than `EventSource`,
because the request is a POST. The SSE frame parser already written in
`provider.js` (including `\r\n\r\n` framing for proxies that rewrite line
endings) is reused verbatim.

Timeouts: `ResponseHeaderTimeout` on the upstream client, plus a per-provider
overall deadline. `WriteTimeout` on our own server is disabled for the SSE
route and enforced per-request instead, so a long generation is not cut off.

---

## 8. Usage ledger

One row per AI request, written once, never updated after settlement:

```
user_id, group_id, provider_id, model_id, model_ref, conversation_id,
message_id, request_id, input_tokens, output_tokens, reasoning_tokens,
total_tokens, credits, status, error_code, started_at, finished_at, duration_ms
```

`status` ∈ `ok | error | aborted | rejected`. An aborted turn is recorded
with the tokens it actually consumed; a quota rejection is recorded with
zeros, which is what makes "why did my request fail" answerable later.

Credits:

```
credits = request_weight
        + input_tokens     × input_token_weight     / 1000
        + output_tokens    × output_token_weight    / 1000
        + reasoning_tokens × reasoning_token_weight / 1000
```

Weights live on the model row, so a 0.2× model and a 3× model share one user
allowance. The first release can leave every weight at 1 and nothing about
the schema or the query path has to change to start using them.

Every aggregate the admin UI shows — per user, per model, per provider, per
window — is a `GROUP BY` over this table. There is no denormalised
`used_tokens` column anywhere to drift out of sync.

---

## 9. Quota

### Policy resolution

```
global default  →  user's group  →  user override      (last non-NULL wins)
```

Every field is nullable at each level; `NULL` means inherit. `*_enabled = 0`
at any level means that window is off for this user. Admins bypass
enforcement by default (a setting).

Allowance windows normally start when an account registers, so a new account
gets a complete first period. A global quota reset becomes the new anchor for
every account that already exists: immediately after one, the interface shows
five hours and seven days remaining rather than the remainder of each user's
old period. Accounts registered later start from their own registration time.

### Atomicity

The forbidden shape is `read → check → increment`. Instead, a **reservation**
is a single atomic upsert per window:

```sql
INSERT INTO usage_counters (scope_key, window_kind, window_start, requests, tokens, credits)
VALUES (?, ?, ?, 1, 0, 0)
ON CONFLICT (scope_key, window_kind, window_start)
DO UPDATE SET requests = usage_counters.requests + 1
RETURNING requests, tokens, credits;
```

The returned value is post-increment, so the check happens *after* the write:
if it is over the limit, the request is rejected and the increment is undone
in the same transaction. Two concurrent requests cannot both see room that
only one of them has. RPM/TPM use the same table with a 60-second bucket.

Settlement after the response adds the real token and credit totals to the
same rows.

This works identically on SQLite (`ON CONFLICT … DO UPDATE … RETURNING`, 3.35+)
and Postgres. `usage_counters` is a cache, not a source of truth: it can be
rebuilt from `usage_records` at any time, and stale buckets are dropped by the
janitor.

Redis is not in this design. It would be introduced only if measurement shows
the counter table is a bottleneck, which for a self-hosted server on a VPS it
will not be.

---

## 10. Security

| Concern | Handling |
| --- | --- |
| Passwords | Argon2id, `m=19MiB, t=2, p=1` (OWASP floor), with a concurrency semaphore so a login burst cannot spike memory. Tunable in config. |
| Sessions | 32 random bytes in an `HttpOnly; Secure; SameSite=Lax; Path=/` cookie. The database stores only its SHA-256, so a database leak is not a session leak. Rotated on login, revoked on logout and on disable. |
| CSRF | `SameSite=Lax` plus an `Origin` / `Sec-Fetch-Site` check on every unsafe method. No token table. |
| IDOR | Ownership is a `WHERE user_id = ?` clause on the query, never a check on the result. Conversations, messages, attachments and usage rows all follow this. |
| Authorization | Every admin route sits behind `RequireAdmin`. Model permission is checked in the gateway, not inferred from what the client asked for. |
| XSS | The markdown renderer builds elements and never produces an HTML string. Plus a strict CSP (`default-src 'self'`, no inline script). |
| SQL injection | `database/sql` placeholders only; a `rebind` helper converts `?` to `$N` for Postgres. No string-concatenated SQL anywhere. |
| Provider keys | AES-256-GCM at rest, key derived by HKDF from `OBSIDIAN_SECRET_KEY`. Never serialised into any response — the admin UI sees `••••1234`. Never logged. |
| Log redaction | A `redact()` helper for anything key-shaped; request bodies are never logged. |
| Input validation | Every handler decodes into a typed struct with explicit bounds; `MaxBytesReader` on all bodies. |
| Rate limiting | Per-IP on `/api/auth/*` (in-memory token bucket), per-user via quota everywhere else. |
| Abuse review | Sign-up review produces `allow`, timed `restrict`, or `refuse`; restrictions gate all programmatic API keys at the account level. Browser chat bursts use a database-backed per-account counter and an on-demand Turnstile challenge. |
| Security audit | Access decisions are persisted in `security_events`, separately from the ordinary HTTP request log. |

Explicitly out of scope for the browser: provider API keys never reach the
HTML, the JS bundle, any network response, or `localStorage`.

---

## 11. Frontend

Vue 3 with `<script setup>`, SCSS, built by Vite, output embedded into the Go
binary via `embed.FS` and served with an SPA fallback.

```
web/src/
  main.ts          boot: dictionary + session, then mount
  router/          the table, and the guards that decide what is drawn
  api/             typed fetch client, SSE reader, error normalisation
  stores/          session.ts — who is signed in, as refs
  composables/     i18n, theme, panel host, turnstile, confetti, stored widths
  components/      Oa* — panel, select, menu, table, fields, chart, scroll area
  layouts/         AppShell, ChatLayout, AccountMenu
  icons/           the glyph set, on lucide's factory
  lib/             pure helpers: formats, chart geometry, list placement, sanitiser
  theme/           color-utils.ts, theme.ts (light/dark/auto), accent, wallpaper
  chat/            the surface, its store, markdown.ts, math.ts, image.ts
  announce/        the bell, the sheet, the two banners
  views/           one screen each, admin/ under it
  styles/          _chat, _workspace, _app, _surfaces, _base — carried over
```

The `components/` primitives are the interface's whole vocabulary: the
backoffice is built from the same panel, table, fields and select the chat and
the settings screen use, so it reads as one product rather than a dashboard
bolted to the side of one.

The stylesheets arrived as the previous build's, byte for byte, renamed to
`.scss` partials. That was deliberate — the port was meant to be
pixel-for-pixel identical to what it replaced, and re-indenting five thousand
declarations into nested Sass is a change with thousands of chances to move
something by a pixel and no way to see that it did. What has changed since is
a short list of deliberate rules, each with its reason beside it.

### Route map

```
/                 chat (redirects to /login when unauthenticated)
/login /register  auth
/settings         user settings (drawer on desktop, page on mobile)
/admin            dashboard
/admin/users      /admin/groups   /admin/providers
/admin/models     /admin/usage    /admin/settings
```

### API surface

```
POST   /api/auth/register           POST /api/auth/login
POST   /api/auth/logout             GET  /api/auth/me
GET    /api/models                  → only what this user may actually use
GET    /api/conversations           DELETE /api/conversations
GET    /api/conversations/{id}      PATCH /api/conversations/{id}   (title, pin, archive)
DELETE /api/conversations/{id}
GET    /api/projects                POST /api/projects
GET    /api/projects/{id}           PATCH /api/projects/{id}
DELETE /api/projects/{id}
POST   /api/chat                    → SSE
POST   /api/attachments             GET  /api/attachments/{id}
POST   /api/images/generate
GET    /api/usage/me                → current windows, limits, resets_at
GET    /api/uptime                  → system & model availability
GET    /api/announcements           POST /api/announcements/read
GET    /api/preferences             PATCH /api/preferences
PATCH  /api/profile
GET/POST/PATCH/DELETE /api/admin/{users,groups,providers,models}/…
POST   /api/admin/providers/{id}/detect
GET    /api/admin/usage             GET/PUT /api/admin/settings
```

---

## 12. Deployment

Three supported shapes, in order of how the project expects to be run:

**1. A binary.**

```
./obsidian-arc
```

Defaults to SQLite at `./data/obsidian.db` on `:8080`, runs migrations,
creates the first admin from `OBSIDIAN_ADMIN_USER` / `OBSIDIAN_ADMIN_PASSWORD`
on an empty database, and serves the embedded SPA. Nothing else to install,
and nothing required in the environment: a missing `OBSIDIAN_SECRET_KEY` is
generated once into `./data/secret.key` and reused, with a warning saying to
set it explicitly before running a second instance against the same
database.

**2. Docker.** Multi-stage: `node` builds the SPA, `golang` builds the binary
with the SPA embedded, `scratch`/`distroless` runs it. Target image well
under 40 MB.

**3. Docker Compose.** The binary plus a Postgres service, for anyone who
wants it.

Configuration is environment variables with flag overrides, all in
`internal/config`:

```
OBSIDIAN_ADDR              :8080
OBSIDIAN_DB_DRIVER         sqlite | postgres
OBSIDIAN_DB_DSN            ./data/obsidian.db  |  postgres://…
OBSIDIAN_SECRET_KEY        encrypts provider keys; generated into the data
                           directory when unset
OBSIDIAN_SESSION_TTL       720h
OBSIDIAN_TRUST_PROXY       false
OBSIDIAN_LOG_LEVEL         info
```

No Kubernetes assumptions. No init containers, no sidecars, no service mesh.

---

## 13. Implementation phases

Each phase ends with `go build`, `go vet`, `go test ./...`, `tsc --noEmit`,
a frontend build, and a boot of the server before the next one starts.

| Phase | Contents |
| --- | --- |
| 1 | Repo layout, `config`, `database` (both drivers), migration runner, `httpx` middleware, embedded SPA serving, Vite/TS scaffold with the existing CSS ported, `main.go` that boots and serves |
| 2 | `auth` (argon2id, sessions, CSRF), `user`, `group`, RBAC middleware, login/register pages, first-admin bootstrap |
| 3 | `provider` (CRUD + key encryption), `model` (CRUD + capabilities + weights), `adapter` package with both adapters and their tests, upstream model detection |
| 4 | `conversation`, `attachments`, `chat` gateway, SSE streaming, cancellation, the chat UI wired to the server — the product works end to end here |
| 5 | `usage` ledger, `quota` policy + atomic counters + enforcement, usage bar in the UI |
| 6 | `admin` module and all seven admin pages |
| 7 | User settings: profile, default model, default reasoning, theme, accent, wallpaper, preference sync |
| 8 | Security pass (CSP, rate limits, redaction, an IDOR sweep), performance pass (indexes, allocation profile, bundle size), Dockerfile, Compose, README, release build |
```
