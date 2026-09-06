# Obsidian Arc

A self-hosted AI chat server: multi-user, multi-provider, with an admin
backoffice, saved conversations and usage accounting. One Go binary with the
frontend inside it, one database, nothing else to run.

It started as the AI chat component inside [PageDye](https://github.com/OnyxAxisOwO/PageDye)
(a browser extension for theming websites), was pulled out into a standalone
bring-your-own-key page, and is now the server that page always wanted to be.
The interface is the same one: same layout, same message shapes, same accent
system, same motion. What changed is everything behind it.

## What it does

- **Accounts and groups.** Register, sign in, sign out. Groups decide which
  models a set of people may use and what allowance they share; nothing is
  hard-coded, so an instance defines whatever groups it wants.
- **Any provider.** Anthropic's Messages API and any OpenAI-compatible
  endpoint — OpenAI, DeepSeek, xAI, OpenRouter, Groq, a local Ollama or vLLM.
  Added from the admin backoffice at runtime, not from a config file.
- **Providers and models are separate.** One provider serves many models, and
  the id the provider knows a model by is not the name a user reads.
- **Streamed answers** over Server-Sent Events, with live reasoning where the
  model produces it. Pressing Stop cancels the upstream request, so it stops
  generating and stops billing.
- **Unified reasoning.** The interface offers one toggle and three levels; the
  adapter turns that into thinking budgets, `reasoning_effort`, or whatever
  else the endpoint wants.
- **Saved conversations.** Switch, rename, delete. Edit an earlier message and
  resend — everything after it is regenerated. Regenerate any answer, retry a
  failed turn.
- **Images and text files.** Pictures are downscaled in the browser before
  they are uploaded; text and code files are folded into the message.
- **Usage and quota.** Every request writes a ledger row. Limits apply per 5
  hours, per week and per month, over requests, tokens or credits, resolved
  global → group → user.
- **Light / dark / system theme** with an accent picker: pick one hue and it
  is adjusted to stay readable in both schemes.
- **Announcements** with a read state per person: a popup on every visit,
  once until it is read, or nothing but a dot on the bell.
- **A configurable front door.** Visitors with no account get the sign-in
  card, a page you write, or the chat itself — optionally live, so they can
  ask a couple of questions before signing up.
- **Model routing.** Offer one model and serve it with another; users see the
  model they picked, and only an administrator sees that a route exists.
- **Registration controls**: required and verified email addresses, a domain
  allowlist, and a ceiling on how fast accounts may appear.
- **Safe Markdown rendering** with no `innerHTML` in any path that renders
  someone else's content. The one assignment in the project is the
  administrator's own landing page, and the content policy — `script-src`
  is `'self'` plus one hash — is what contains it rather than trust.

## Running it

```bash
./obsidian-arc
```

That is the whole quick start. It creates `./data`, generates a secret key,
runs its migrations, opens SQLite, and serves on `:8080`. The first account to
register becomes the administrator.

For an unattended first boot, name the administrator instead:

```bash
OBSIDIAN_ADMIN_USER=admin OBSIDIAN_ADMIN_PASSWORD='a good password' ./obsidian-arc
```

### Configuration

Every setting has a working default. These are the ones a real deployment
tends to set:

| Variable | Default | |
| --- | --- | --- |
| `OBSIDIAN_ADDR` | `:8080` | Listen address |
| `OBSIDIAN_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `OBSIDIAN_DB_DSN` | `./data/obsidian.db` | Required for Postgres |
| `OBSIDIAN_SECRET_KEY` | generated into `./data` | Encrypts provider API keys. Set it explicitly before running more than one instance against one database |
| `OBSIDIAN_DATA_DIR` | `./data` | Database, secret key |
| `OBSIDIAN_TRUST_PROXY` | `false` | Honour forwarded addresses. Only from the peers below |
| `OBSIDIAN_TRUSTED_PROXIES` | private ranges | Exact proxy addresses or CIDRs. Everything else has its forwarded headers ignored |
| `OBSIDIAN_COOKIE_SECURE` | `true` | Turn off only for plain-http local use |
| `OBSIDIAN_SESSION_TTL` | `720h` | |
| `OBSIDIAN_ADMIN_USER` / `_PASSWORD` | — | First administrator, on an empty database |
| `OBSIDIAN_PUBLIC_URL` | — | Where links in outgoing mail point. Required for email verification |
| `OBSIDIAN_SMTP_HOST` | — | Enables mail. Without it, email verification stays inert whatever the setting says |
| `OBSIDIAN_SMTP_PORT` | `587` | 465 turns on implicit TLS; 587 uses STARTTLS |
| `OBSIDIAN_SMTP_FROM` | the username | Envelope and header sender |
| `OBSIDIAN_SMTP_USERNAME` / `_PASSWORD` | — | Omit both for an unauthenticated relay |

### Docker

```bash
docker build -t obsidian-arc .
docker run -d -p 8080:8080 -v arc-data:/data \
  -e OBSIDIAN_SECRET_KEY=$(openssl rand -hex 32) \
  obsidian-arc
```

Three stages: the frontend is compiled, embedded into the Go binary, and the
binary is copied into a distroless image with no shell, no package manager
and no interpreter in it. It runs as a non-root user.

For PostgreSQL, `docker-compose.yml` adds one service and nothing else — no
reverse proxy, no cache, no queue:

```bash
OBSIDIAN_SECRET_KEY=$(openssl rand -hex 32) docker compose up -d
```

Kubernetes is not assumed anywhere.

> **Not yet run anywhere.** The Docker and PostgreSQL paths were written and
> reviewed but never executed — neither Docker nor a Postgres server was
> available on the machine this was built on. Everything else in this README
> was verified end to end against SQLite. The Postgres schema is covered by a
> lint that rejects engine-specific syntax in the migrations, and by an
> integration test that runs the real migrations plus the atomic quota upsert
> when you point it at a database:
>
> ```bash
> OBSIDIAN_TEST_POSTGRES_DSN=postgres://user:pass@localhost:5432/arc_test go test ./internal/database/
> ```

## Building

```bash
make build     # frontend, then a binary with it embedded
make test      # go vet, gofmt, go test, tsc
make dev       # server on :8080 proxying to Vite on :5173
make version   # the version this build would carry
```

`make dev` expects `npm --prefix web run dev` alongside it and reverse
proxies to it, so the frontend hot-reloads while the API stays on one origin.

Requires Go 1.22+ and Node 20+.

Versions are the UTC build moment — `yyyy.MM.dd.HH.mm.ss` — stamped in by the
Makefile. Zero-padded, so they sort chronologically as plain text; unique per
build; and needing no tag or counter to keep up to date. `/api/health`
returns the running one, and the admin rail shows it.

## What it costs to run

Measured on the build in this repository, SQLite, one process:

| | |
| --- | --- |
| Binary | 16.9 MB — 13.3 MB built `-tags nosqlite` for a Postgres-only deployment |
| Cold start to serving | 28 ms |
| Idle resident memory | ~16 MB |
| After 200 streamed turns, 20 concurrent | ~54 MB peak, 11 OS threads |
| Frontend | 59 kB on the wire to open the chat — 47 kB of JavaScript and 13 kB of CSS. The server compresses, so that is what is actually transferred, not what a proxy might have managed. The Chinese dictionary (12 kB), the backoffice (18 kB) and the formula renderer (4 kB) are separate, and are fetched only by the readers who need them |
| Background goroutines at idle | 1 — a janitor on a ten-minute tick |
| Direct Go dependencies | 3 |
| Runtime frontend dependencies | 0 |

## Architecture

One process. A modular monolith, not services.

```
cmd/server            wire, serve, shut down
internal/
  config              one Config from the environment
  database            one pool, `?` rebound per dialect, embedded migrations
  httpx               errors, JSON, SSE, middleware
  auth                argon2id, sessions, RBAC
  user  group         accounts, groups, permissions
  provider  model     upstream endpoints and the catalogue
  adapter             OpenAI + Anthropic, behind one request shape
  conversation        transcripts, messages, attachments
  chat                the gateway: permission → transcript → provider → ledger
  usage  quota        accounting and enforcement
  admin               the administrative surface
  settings            instance settings
  web                 the embedded frontend
web/                  TypeScript + Vite, zero runtime dependencies
```

Three direct Go dependencies: a pure-Go SQLite driver, pgx, and `x/crypto`
for Argon2id. Routing is `net/http`. Migrations are numbered `.sql` files.
There is no ORM, no router, no logging framework, and no config library.

The design, the schema and the reasoning behind both are in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

### Notes on a few decisions

**SQLite or PostgreSQL.** Both, from one set of queries. Identifiers are
ULIDs and timestamps are epoch milliseconds, so nothing depends on a
sequence or a timestamp type; `?` placeholders are rebound per dialect in one
place. SQLite is the default because a single-instance deployment does not
need a second process to run.

**Cancellation is the request context.** Stop closes the browser's
connection, which cancels the server's request context, which cancels the
outbound call to the provider. There is no stop endpoint and no registry of
in-flight requests. The save afterwards runs on a detached context, so a
stopped answer is still kept — it is what the user read, and it was paid for.

**Provider keys never leave the server.** They are encrypted with a key
derived from the instance secret, and the struct the admin API serialises has
no field they could travel in. The administrator sees `••••1234`.

**No Redis.** Quota is enforced with an atomic upsert that returns its own
post-increment value, so the check happens after the write and two concurrent
requests cannot both see room that only one of them has. If the counter table
ever becomes the bottleneck, that is the moment to add a cache — not before.

## License

[MIT](LICENSE)
