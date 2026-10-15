// The human check on making an API key.
//
// A token is good for one submission, and a refusal is no exception: the widget
// shows solved until it is reset, so a retry that is not reset sends the spent
// token and is refused again for a reason the reader cannot see. The widget
// itself is stubbed here, since it loads a script the test environment does not
// have.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import * as keysAPI from '../src/api/keys';
import { ApiError, api } from '../src/api/client';
import { changeLanguage } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { site, siteInfo } from '../src/stores/session';
import KeysPanel from '../src/views/KeysPanel.vue';

// What the stubbed widget currently holds: a solve sets it, a reset clears it.
const widget = vi.hoisted(() => ({ token: '' }));

vi.mock('../src/components/OaTurnstile.vue', () => ({
  default: {
    setup(_props: unknown, { expose }: { expose: (exposed: Record<string, unknown>) => void }) {
      expose({
        token: () => widget.token,
        reset: () => { widget.token = ''; },
      });
      return () => null;
    },
  },
}));

const KEY = { id: 'key-1', name: 'laptop', prefix: 'sk-test', disabled: false } as keysAPI.ApiKey;

let app: App | null = null;
let host: HTMLElement;
let panelHost: HTMLElement;

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

async function mount(): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: KeysPanel }] });
  await router.push('/');
  await router.isReady();
  app = createApp({
    setup() {
      providePanelHost(ref(panelHost));
      return () => h(KeysPanel);
    },
  });
  app.use(router).mount(host);
  await settle();
}

async function press(name: string): Promise<void> {
  const input = panelHost.querySelector<HTMLInputElement>('.oa-keys-create input[type="text"]')!;
  input.value = name;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  panelHost.querySelector<HTMLButtonElement>('.oa-keys-create > button.primary')!.click();
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
  widget.token = '';
  site.value = { ...siteInfo.value, turnstile_on_api_key: true, turnstile_site_key: 'site' };
  vi.spyOn(keysAPI, 'listKeys').mockResolvedValue({ keys: [], enabled: true, max: 10 });
  vi.spyOn(api, 'get').mockResolvedValue({ models: [] });
});

afterEach(() => {
  app?.unmount();
  app = null;
  site.value = null;
  host.remove();
  panelHost.remove();
  vi.restoreAllMocks();
});

describe('the human check on a new key', () => {
  it('is spent by a refused attempt, so the retry sends a fresh check and not the old token', async () => {
    widget.token = 'solved-once';
    const create = vi.spyOn(keysAPI, 'createKey')
      .mockRejectedValueOnce(new ApiError(400, 'challenge_failed', 'The check did not pass.', {}))
      .mockResolvedValueOnce({ key: KEY, token: 'sk-test-only-secret' });
    await mount();

    await press('laptop');
    expect(create.mock.calls[0]![3]).toBe('solved-once');
    expect(widget.token).toBe('');

    await press('laptop');
    expect(create.mock.calls[1]![3]).toBe('');
  });

  // The server refuses a full account before it checks the human token, so the
  // token is still good: resetting it here would make the reader solve again
  // for nothing.
  it('is not spent by a refusal for a full account, which comes before the check', async () => {
    widget.token = 'solved-once';
    const create = vi.spyOn(keysAPI, 'createKey')
      .mockRejectedValueOnce(new ApiError(409, 'too_many_keys', 'You already have the maximum of 10 keys.', {}))
      .mockResolvedValueOnce({ key: KEY, token: 'sk-test-only-secret' });
    await mount();

    await press('laptop');
    expect(widget.token).toBe('solved-once');

    await press('laptop');
    expect(create.mock.calls[1]![3]).toBe('solved-once');
  });

  it('is spent by a key that is made, too', async () => {
    widget.token = 'solved-once';
    vi.spyOn(keysAPI, 'createKey').mockResolvedValue({ key: KEY, token: 'sk-test-only-secret' });
    await mount();

    await press('laptop');
    expect(widget.token).toBe('');
  });
});
