# Obsidian Arc

A standalone, bring-your-own-key AI chat workspace. No account, no server, no
build step — open `index.html` and talk to Anthropic or any OpenAI-compatible
endpoint (DeepSeek, OpenRouter, Groq, a local Ollama/vLLM...) straight from
the browser.

It started as the AI chat component inside [PageDye](https://github.com/OnyxAxisOwO/PageDye)
(a browser extension for theming websites), pulled out and generalized into
its own project: the chat surface, history, streaming, editing and model
management, without anything specific to what PageDye used it for.

## What it does

- Bring your own API key — Anthropic or any OpenAI-compatible base URL —
  stored only in your browser, optionally encrypted at rest
- Streamed responses with live "thinking" / reasoning display where the
  model provides one
- Full conversation history: switch, rename by first message, delete, clear
- Edit an earlier message and resend — everything after it is regenerated
- Regenerate any answer, copy any message, retry a failed turn
- Image attachments (drag, drop, paste, or pick a file) for vision models,
  downscaled client-side before they're ever sent or stored
- A model shortlist per provider, with a "detect" button that asks the
  endpoint what it actually serves
- Light / dark / system theme
- Safe Markdown rendering with no `innerHTML` anywhere in the render path

Nothing is sent anywhere until you add an API key. There is no backend: your
key and your conversations live in `localStorage`, in your browser, on your
device.

## Quick start

No build step. Any static file server works — or just open the file:

```bash
python3 -m http.server 8080   # macOS/Linux
python -m http.server 8080    # Windows
```

Then visit `http://localhost:8080`, or open `index.html` directly.

## How calling the API from a browser tab works

Anthropic's Messages API supports being called directly from a page origin
via the `anthropic-dangerous-direct-browser-access` header — that's what
makes a serverless bring-your-own-key tool like this possible for that
provider. It is exactly as dangerous as it sounds: your API key is visible to
anything else running on the page, which is the inherent trade-off of a
client-only tool with no backend of its own. Don't paste a key you don't
trust this browser profile with, and don't open this page somewhere a
malicious script could run alongside it.

Most OpenAI-compatible endpoints already allow browser-origin requests with
just a bearer token; a few (notably OpenAI's own API) do not enable CORS for
arbitrary origins and will need a proxy of your own in front of them.

## Architecture

Plain scripts, no bundler, no framework — the same convention PageDye itself
uses. Each file is a small UMD-style module attached to `globalThis`:

| File | Purpose |
| --- | --- |
| `src/store.js` | Conversation persistence and normalization (`ObsidianStore`) |
| `src/markdown.js` | A small, XSS-safe Markdown renderer (`ObsidianMarkdown`) |
| `src/image.js` | Downscales a picked/dropped/pasted image for attachment (`ObsidianImage`) |
| `src/provider.js` | Talks to Anthropic / OpenAI-compatible APIs, streaming included (`ObsidianProvider`) |
| `src/chat.js` | The chat UI itself — history, composer, transcript (`ObsidianChat`) |
| `src/workspace.js` | The header bar + settings drawer wrapped around `chat.js` (`ObsidianWorkspace`) |
| `src/adapters.js` | The `localStorage`-backed storage adapter used standalone (`ObsidianAdapters`) |

`src/chat.js` has no idea providers exist. It asks its host two things:

```js
ObsidianChat.mount({
  root,               // an element to render into
  storage,            // async {get(key), set(obj), onChanged?(cb)} — the exact
                       // shape chrome.storage.local already has
  getStatus,          // async () => ({ configured, vision, streaming })
  send,               // async ({turns, signal, onReply, onThinking}) => ({reply, thinking, stats, streamed, streamFallback})
  openSettings,       // () => void, called from the chat's own setup card
  lang,               // 'en' | 'zh'
  variant,            // 'wide' | 'narrow'
  mainHeader          // optional element rendered above the transcript
});
```

That's a small enough surface that embedding it somewhere else — a different
backend, a browser extension's own message-passing, a different set of
providers — means writing `getStatus`/`send` against whatever that host
already has, not forking this file. `src/workspace.js` is what a host looks
like when it *is* `src/provider.js`: it is the reference implementation of
that adapter contract, plus the settings UI to go with it.

An embedding browser extension can pass `chrome.storage.local` straight
through as `storage` with no adapter at all — that shape was chosen on
purpose.

## What this project deliberately does not do

This is the chat surface, not a specific application built on top of it. It
doesn't know how to turn a conversation into anything other than more
conversation — no structured output, no tool use, no domain-specific "cards"
in the transcript. A host that wants that (PageDye's original theme
generator being one example) builds it on top of `send()`'s response, the
same way `workspace.js` builds a settings panel on top of `provider.js`.

## License

[MIT](LICENSE)
