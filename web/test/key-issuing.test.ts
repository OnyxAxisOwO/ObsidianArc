// What a plugin can put around the moment an API key is made: a line at the top
// of the keys screen, and a question that has to be answered by typing before
// the key exists. Held here: the notice is there and only there; the question
// is asked for every key, not once; the word it asks for is drawn, never text,
// so it cannot be selected and copied, and cannot be pasted back in; and saying
// no, or saying it wrong, creates nothing.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import * as keysAPI from '../src/api/keys';
import { api } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { installPlugins } from '../src/plugins/registry';
import type { ArcPlugin, KeyConfirmation } from '../src/plugins/types';
import KeysPanel from '../src/views/KeysPanel.vue';

const KEY: keysAPI.ApiKey = {
  id: 'key-1', name: 'My laptop', prefix: 'sk-test', disabled: false,
  expires_at: 0, last_used_at: 0, created_at: 1, updated_at: 1,
};
const WORD = 'Proceed';

const question = (over: Partial<Record<keyof KeyConfirmation, string>> = {}): KeyConfirmation => ({
  title: () => over.title ?? 'Are you sure',
  body: () => over.body ?? 'Read this before you go on.',
  prompt: () => over.prompt ?? 'Type the word below to go on',
  word: () => over.word ?? WORD,
  proceed: () => over.proceed ?? 'Make the key',
});

const plugin = (keyIssuing: ArcPlugin['keyIssuing']): ArcPlugin => ({ name: 'demo', ...(keyIssuing ? { keyIssuing } : {}) });

let app: App;
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

async function press(name = KEY.name): Promise<void> {
  const input = panelHost.querySelector<HTMLInputElement>('.oa-keys-create input[type="text"]')!;
  input.value = name;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  panelHost.querySelector<HTMLButtonElement>('.oa-keys-create > button.primary')!.click();
  await settle();
}

const dialog = (): HTMLElement | null => document.querySelector<HTMLElement>('.oa-typed-confirm');
const field = (): HTMLInputElement => dialog()!.querySelector<HTMLInputElement>('input')!;
const proceed = (): HTMLButtonElement => dialog()!.querySelector<HTMLButtonElement>('button[type="submit"]')!;

async function type(value: string): Promise<void> {
  field().value = value;
  field().dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
  vi.spyOn(keysAPI, 'listKeys').mockResolvedValue({ keys: [], enabled: true, max: 10 });
  vi.spyOn(keysAPI, 'createKey').mockResolvedValue({ key: KEY, token: 'sk-test-only-secret' });
  vi.spyOn(api, 'get').mockResolvedValue({ models: [] });
});

afterEach(() => {
  app?.unmount();
  host.remove();
  panelHost.remove();
  document.querySelectorAll('.oa-modal-overlay').forEach((node) => node.remove());
  installPlugins([]);
  vi.restoreAllMocks();
});

describe('the notice', () => {
  it('is the first thing on the screen when a plugin has one', async () => {
    installPlugins([plugin({ notice: () => 'Keys are not for that.' })]);
    await mount();
    const notice = panelHost.querySelector('.oa-key-warning[role="note"]');
    expect(notice?.textContent).toBe('Keys are not for that.');
    expect(panelHost.querySelector('.oa-panel-body .oa-key-warning')?.textContent).toBe('Keys are not for that.');
  });

  it('is not there without one', async () => {
    installPlugins([plugin(undefined)]);
    await mount();
    expect(panelHost.querySelector('.oa-key-warning[role="note"]')).toBeNull();
  });

  it('is not shown where no key can be made', async () => {
    vi.mocked(keysAPI.listKeys).mockResolvedValue({ keys: [], enabled: false, max: 0 });
    installPlugins([plugin({ notice: () => 'Keys are not for that.' })]);
    await mount();
    expect(panelHost.querySelector('.oa-key-warning[role="note"]')).toBeNull();
  });
});

describe('the confirmation', () => {
  beforeEach(() => installPlugins([plugin({ confirmation: question() })]));

  it('asks before the key is made, and makes it only after the word is typed', async () => {
    await mount();
    await press();
    expect(dialog()).not.toBeNull();
    expect(dialog()!.querySelector('.oa-auth-title')?.textContent).toBe('Are you sure');
    expect(keysAPI.createKey).not.toHaveBeenCalled();
    expect(proceed().disabled).toBe(true);

    await type('Proc');
    expect(proceed().disabled).toBe(true);
    await type('  proceed ');
    expect(proceed().disabled).toBe(false);

    proceed().click();
    await settle();
    expect(keysAPI.createKey).toHaveBeenCalledTimes(1);
    expect(dialog()).toBeNull();
    expect(panelHost.querySelector('.oa-key-token-input')).not.toBeNull();
  });

  it('does not put the word in the page, where it could be selected and copied', async () => {
    await mount();
    await press();
    const word = dialog()!.querySelector<HTMLElement>('.oa-typed-word')!;
    // It is drawn from an attribute by the stylesheet: nothing in the tree says it.
    expect(word.dataset['word']).toBe(WORD);
    expect(word.textContent).toBe('');
    expect(word.getAttribute('aria-hidden')).toBe('true');
    expect(dialog()!.textContent).not.toContain(WORD);
    // A reader that cannot see it is still told, by a name nothing can select.
    expect(field().getAttribute('aria-label')).toContain(WORD);
  });

  it('refuses a word carried in by paste or by drop', async () => {
    await mount();
    await press();
    for (const kind of ['paste', 'drop', 'dragover']) {
      const event = new Event(kind, { bubbles: true, cancelable: true });
      field().dispatchEvent(event);
      expect(event.defaultPrevented, kind).toBe(true);
    }
  });

  it('makes nothing when it is turned down, and leaves the form as it was', async () => {
    await mount();
    await press();
    const cancel = dialog()!.querySelector<HTMLButtonElement>('button[type="button"]:not(.oa-modal-close)')!;
    expect(cancel.textContent).toBe(t('cancel'));
    cancel.click();
    await settle();
    expect(dialog()).toBeNull();
    expect(keysAPI.createKey).not.toHaveBeenCalled();
    expect(panelHost.querySelector<HTMLInputElement>('.oa-keys-create input[type="text"]')?.value).toBe(KEY.name);
  });

  it('is not asked when the name is missing: the refusal for that comes first', async () => {
    await mount();
    await press('  ');
    expect(dialog()).toBeNull();
    expect(panelHost.textContent).toContain(t('keyNameRequired'));
  });

  it('is asked for every key, from an empty field, and not once', async () => {
    await mount();
    for (let round = 0; round < 3; round += 1) {
      await press(`Key ${round}`);
      expect(dialog(), `round ${round}`).not.toBeNull();
      expect(field().value).toBe('');
      await type(WORD);
      proceed().click();
      await settle();
      const done = Array.from(panelHost.querySelectorAll<HTMLButtonElement>('button'))
        .find((button) => button.textContent?.trim() === t('keyCopied'))!;
      done.click();
      await settle();
    }
    expect(keysAPI.createKey).toHaveBeenCalledTimes(3);
  });

  it('cannot be answered twice by a second click on the button', async () => {
    await mount();
    await press();
    await type(WORD);
    const button = proceed();
    button.click();
    button.click();
    await settle();
    expect(keysAPI.createKey).toHaveBeenCalledTimes(1);
  });
});

describe('more than one plugin', () => {
  it('asks each question in turn, and stops at the first no', async () => {
    installPlugins([
      { name: 'one', keyIssuing: { confirmation: question({ title: 'First', word: 'one' }) } },
      { name: 'two', keyIssuing: { confirmation: question({ title: 'Second', word: 'two' }) } },
    ]);
    await mount();
    await press();
    expect(dialog()!.querySelector('.oa-auth-title')?.textContent).toBe('First');
    await type('one');
    proceed().click();
    await settle();
    expect(dialog()!.querySelector('.oa-auth-title')?.textContent).toBe('Second');
    expect(keysAPI.createKey).not.toHaveBeenCalled();
    dialog()!.querySelector<HTMLButtonElement>('button[type="button"]:not(.oa-modal-close)')!.click();
    await settle();
    expect(keysAPI.createKey).not.toHaveBeenCalled();
  });
});
