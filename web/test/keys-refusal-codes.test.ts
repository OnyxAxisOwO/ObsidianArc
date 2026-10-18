// A refusal the server gives a code is worded by the interface in the reader's
// language. Only a refusal without a code keeps the server's own sentence.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import KeysPanel from '../src/views/KeysPanel.vue';
import { ApiError, api } from '../src/api/client';
import * as keysAPI from '../src/api/keys';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { settle } from './helpers';

const KEY: keysAPI.ApiKey = {
  id: 'key-1', name: 'My laptop', prefix: 'sk-test', disabled: false,
  expires_at: 0, last_used_at: 0, created_at: 1, updated_at: 1,
};

let app: App | null = null;
let panelHost: HTMLElement;


async function mount(): Promise<void> {
  const host = document.createElement('div');
  panelHost.append(host);
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

async function submitName(name: string): Promise<void> {
  const input = panelHost.querySelector<HTMLInputElement>('.oa-keys-create input[type="text"]')!;
  input.value = name;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  panelHost.querySelector<HTMLButtonElement>('.oa-keys-create > button.primary')!.click();
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  panelHost = document.createElement('div');
  document.body.append(panelHost);
  vi.spyOn(keysAPI, 'listKeys').mockResolvedValue({ keys: [KEY], enabled: true, max: 10 });
  vi.spyOn(api, 'get').mockResolvedValue({ models: [] });
});

afterEach(async () => {
  app?.unmount();
  app = null;
  panelHost.remove();
  vi.restoreAllMocks();
  await changeLanguage('en');
});

describe('key refusals', () => {
  it('words a coded refusal in the interface language, with the cap in the message', async () => {
    vi.spyOn(keysAPI, 'createKey').mockRejectedValue(
      new ApiError(409, 'too_many_keys', 'You already have the maximum of 10 keys. Delete one first.'),
    );
    await mount();
    await changeLanguage('zh');
    await submitName('one more');

    expect(panelHost.textContent).toContain(t('keyErrorTooMany', { max: 10 }));
    expect(panelHost.textContent).not.toContain('You already have the maximum');
  });

  it('shows a refusal without a translation as the server wrote it', async () => {
    vi.spyOn(keysAPI, 'createKey').mockRejectedValue(
      new ApiError(400, 'some_new_refusal', 'That name is reserved.'),
    );
    await mount();
    await submitName('plain');

    expect(panelHost.textContent).toContain('That name is reserved.');
  });

  // A server failure's sentence was written for logs and API clients, so the
  // reader gets the screen's generic line rather than the server's English.
  it('shows a server failure as the generic line', async () => {
    vi.spyOn(keysAPI, 'createKey').mockRejectedValue(
      new ApiError(500, 'internal', 'Something went wrong on our side.'),
    );
    await mount();
    await submitName('plain');

    expect(panelHost.textContent).not.toContain('Something went wrong on our side.');
    expect(panelHost.textContent).toContain(t('failed'));
  });
});
