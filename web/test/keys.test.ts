import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import KeysPanel from '../src/views/KeysPanel.vue';
import { ApiError, api } from '../src/api/client';
import * as keysAPI from '../src/api/keys';
import type { AvailableModel } from '../src/chat/useModels';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import * as ccSwitch from '../src/lib/cc-switch';

const KEY: keysAPI.ApiKey = {
  id: 'key-1', name: 'My laptop', prefix: 'sk-test', disabled: false,
  expires_at: 0, last_used_at: 0, created_at: 1, updated_at: 1,
};
const TOKEN = 'sk-test-only-secret';
const MODEL: AvailableModel = {
  id: 'model-1', display_name: 'First model', description: '', avatar: '',
  supports_reasoning: false, supports_images: false, supports_vision: false,
  supports_streaming: true, supports_system_prompt: true, supports_tools: true,
  supports_image_gen: false, supports_chat_image_gen: false,
  context_window: 0, max_output_tokens: 0,
};
let app: App;
let host: HTMLElement;
let panelHost: HTMLElement;

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
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

async function create(): Promise<void> {
  const input = panelHost.querySelector<HTMLInputElement>('.oa-keys-create input[type="text"]')!;
  input.value = KEY.name;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  panelHost.querySelector<HTMLButtonElement>('.oa-keys-create > button.primary')!.click();
  await settle();
}

async function choose(index: number, label: string): Promise<void> {
  panelHost.querySelectorAll<HTMLButtonElement>('.oa-key-import .oa-select')[index]!.click();
  await settle();
  const option = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'))
    .find((entry) => entry.textContent?.trim() === label)!;
  expect(option).toBeDefined();
  option.click();
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
  vi.spyOn(keysAPI, 'listKeys').mockResolvedValue({ keys: [KEY], enabled: true, max: 10 });
  vi.spyOn(keysAPI, 'createKey').mockResolvedValue({ key: KEY, token: TOKEN });
  vi.spyOn(api, 'get').mockResolvedValue({ models: [MODEL, { ...MODEL, id: 'model-2', display_name: 'Second model' }] });
  vi.spyOn(ccSwitch, 'openCCSwitch').mockImplementation(() => {});
});

afterEach(async () => {
  app?.unmount();
  host.remove();
  panelHost.remove();
  vi.restoreAllMocks();
  await changeLanguage('en');
});

const EDIT_TOKEN = `sk-oa-${'A'.repeat(43)}`;
const EDIT_KEY = { ...KEY, prefix: EDIT_TOKEN.slice(0, 12) };

async function openEditor(index = 0): Promise<void> {
  panelHost.querySelectorAll<HTMLButtonElement>('.oa-key-actions button[aria-label="Edit"]')[index]!.click();
  await settle();
}

async function pasteToken(value: string): Promise<void> {
  const input = panelHost.querySelector<HTMLInputElement>('.oa-key-import-token input')!;
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await settle();
}

describe('existing key CC Switch import', () => {
  beforeEach(() => {
    vi.mocked(keysAPI.listKeys).mockResolvedValue({ keys: [EDIT_KEY], enabled: true, max: 10 });
  });

  it('requires a complete matching key and trims whitespace without submitting it', async () => {
    await mount();
    await openEditor();
    const button = panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!;
    expect(button.disabled).toBe(true);
    expect(panelHost.querySelector<HTMLInputElement>('.oa-key-import-token input')?.type).toBe('password');
    for (const invalid of [EDIT_KEY.prefix, 'wrong', `sk-oa-${'B'.repeat(43)}`]) {
      await pasteToken(invalid);
      expect(button.disabled).toBe(true);
    }
    await pasteToken(` ${EDIT_TOKEN}\n`);
    expect(button.disabled).toBe(false);
    button.click();
    expect(ccSwitch.openCCSwitch).toHaveBeenCalledWith(expect.objectContaining({ token: EDIT_TOKEN, model: MODEL.id }));
    expect(keysAPI.createKey).not.toHaveBeenCalled();
    expect(api.get).toHaveBeenCalledTimes(1);
  });

  it('blocks unsaved changes, while preserving saved model restrictions', async () => {
    vi.mocked(keysAPI.listKeys).mockResolvedValue({ keys: [{ ...EDIT_KEY, model_ids: ['model-2'] }], enabled: true, max: 10 });
    await mount();
    await openEditor();
    await pasteToken(EDIT_TOKEN);
    const button = panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!;
    button.click();
    expect(ccSwitch.openCCSwitch).toHaveBeenCalledWith(expect.objectContaining({ model: 'model-2' }));
    const name = panelHost.querySelector<HTMLInputElement>('.oa-keys-create input[type=text]')!;
    name.value = 'Unsaved name';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
    expect(button.disabled).toBe(true);
    expect(panelHost.textContent).toContain(t('keyCCSwitchSaveFirst'));
  });

  it.each([{ disabled: true }, { expires_at: 1 }])('blocks inactive keys: %j', async (state) => {
    vi.mocked(keysAPI.listKeys).mockResolvedValue({ keys: [{ ...EDIT_KEY, ...state }], enabled: true, max: 10 });
    await mount();
    await openEditor();
    await pasteToken(EDIT_TOKEN);
    expect(panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!.disabled).toBe(true);
    expect(panelHost.textContent).toContain(t('keyCCSwitchInactive'));
  });

  it('clears pasted secrets on cancel, saving, and opening another key', async () => {
    vi.mocked(keysAPI.listKeys).mockResolvedValue({ keys: [EDIT_KEY, { ...EDIT_KEY, id: 'key-2' }], enabled: true, max: 10 });
    const save = vi.spyOn(keysAPI, 'updateKey').mockResolvedValue(EDIT_KEY);
    await mount();
    await openEditor();
    await pasteToken(EDIT_TOKEN);
    panelHost.querySelector<HTMLButtonElement>('.oa-key-edit-actions button')!.click();
    await settle();
    await openEditor(1);
    expect(panelHost.querySelector<HTMLInputElement>('.oa-key-import-token input')!.value).toBe('');
    await pasteToken(EDIT_TOKEN);
    panelHost.querySelector<HTMLButtonElement>('.oa-key-edit-actions button.primary')!.click();
    await settle();
    expect(JSON.stringify(save.mock.calls)).not.toContain(EDIT_TOKEN);
    await openEditor(1);
    expect(panelHost.querySelector<HTMLInputElement>('.oa-key-import-token input')!.value).toBe('');
  });
});

describe('API key CC Switch import', () => {
  it('only imports a freshly issued key, preserving it until Done', async () => {
    const localWrite = vi.spyOn(Storage.prototype, 'setItem');
    await mount();
    expect(panelHost.querySelector('.oa-key-import')).toBeNull();
    await create();
    expect(keysAPI.createKey).toHaveBeenCalledWith(KEY.name, 0, [], '');
    expect(ccSwitch.openCCSwitch).not.toHaveBeenCalled();
    expect(panelHost.querySelector('a[href^="ccswitch:"]')).toBeNull();
    panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!.click();
    expect(ccSwitch.openCCSwitch).toHaveBeenCalledExactlyOnceWith({
      app: 'claude', origin: window.location.origin, name: 'Obsidian Arc · My laptop',
      token: TOKEN, model: MODEL.id,
    });
    expect(panelHost.querySelector<HTMLInputElement>('.oa-key-token-input')?.value).toBe(TOKEN);
    expect(localWrite.mock.calls.some((args) => JSON.stringify(args).includes(TOKEN))).toBe(false);
    panelHost.querySelector<HTMLButtonElement>('.oa-key-import + button')!.click();
    await settle();
    expect(panelHost.querySelector('.oa-key-import')).toBeNull();
    expect(panelHost.querySelector('.oa-key-token-input')).toBeNull();
  });

  it('exports selected IDs rather than display names and switches client', async () => {
    await mount();
    await create();
    await choose(0, t('keyCCSwitchCodex'));
    await choose(1, 'Second model');
    panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!.click();
    expect(ccSwitch.openCCSwitch).toHaveBeenCalledWith(expect.objectContaining({ app: 'codex', model: 'model-2' }));
  });

  it.each([['model_ids', ['model-2']], ['model_id', 'model-2']] as const)(
    'respects %s restrictions and excludes unusable models', async (field, value) => {
      vi.mocked(keysAPI.createKey).mockResolvedValue({ key: { ...KEY, [field]: value }, token: TOKEN });
      vi.mocked(api.get).mockResolvedValue({ models: [
        MODEL, { ...MODEL, id: 'blocked', usable: false },
        { ...MODEL, id: 'model-2', display_name: 'Second model' },
      ] });
      await mount();
      await create();
      panelHost.querySelectorAll<HTMLButtonElement>('.oa-key-import .oa-select')[1]!.click();
      await settle();
      expect(Array.from(document.querySelectorAll('[role="option"]')).map((entry) => entry.textContent?.trim()))
        .toEqual(['Second model']);
      panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!.click();
      expect(ccSwitch.openCCSwitch).toHaveBeenCalledWith(expect.objectContaining({ model: 'model-2' }));
    },
  );

  it('disables import when no model is allowed and keeps manual copying', async () => {
    vi.mocked(keysAPI.createKey).mockResolvedValue({ key: { ...KEY, model_ids: ['missing'] }, token: TOKEN });
    await mount();
    await create();
    const button = panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!;
    expect(button.disabled).toBe(true);
    expect(panelHost.textContent).toContain(t('keyCCSwitchNoModels'));
    expect(panelHost.querySelector<HTMLInputElement>('.oa-key-token-input')?.value).toBe(TOKEN);
    button.click();
    expect(ccSwitch.openCCSwitch).not.toHaveBeenCalled();
  });

  it('does not import on a failed creation', async () => {
    vi.mocked(keysAPI.createKey).mockRejectedValue(new ApiError(400, 'test', 'Creation refused'));
    await mount();
    await create();
    expect(panelHost.textContent).toContain('Creation refused');
    expect(panelHost.querySelector('.oa-key-import')).toBeNull();
    expect(ccSwitch.openCCSwitch).not.toHaveBeenCalled();
  });

  it('keeps launcher errors free of secrets and updates translated text', async () => {
    vi.mocked(ccSwitch.openCCSwitch).mockImplementation(() => { throw new Error(TOKEN); });
    await mount();
    await create();
    panelHost.querySelector<HTMLButtonElement>('.oa-key-import-btn')!.click();
    await settle();
    expect(panelHost.querySelector('[role="alert"]')?.textContent).toBe(t('keyCCSwitchFailed'));
    expect(panelHost.textContent).not.toContain(TOKEN);
    await changeLanguage('zh');
    await settle();
    expect(panelHost.querySelector('.oa-key-import-btn')?.textContent).toBe('一键导入 CC Switch');
    expect(panelHost.querySelector('[role="alert"]')?.textContent).toBe(t('keyCCSwitchFailed'));
  });

  it('does not retain the token after unmounting and remounting the panel', async () => {
    await mount();
    await create();
    app.unmount();
    await mount();
    expect(panelHost.querySelector('.oa-key-import')).toBeNull();
    expect(panelHost.querySelector('.oa-key-token-input')).toBeNull();
  });
});

describe('the key panel keeps to its own data', () => {
  it('reads the model catalogue once when it opens, not again after each change', async () => {
    const get = vi.spyOn(api, 'get');
    await mount();
    await create();

    expect(get.mock.calls.filter((call: unknown[]) => call[0] === '/api/models')).toHaveLength(1);
    expect(keysAPI.listKeys).toHaveBeenCalledTimes(2);
  });

  it('asks for the model catalogue again on the next refresh after it failed to load', async () => {
    const get = vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('network down'));
    await mount();
    await create();

    expect(get.mock.calls.filter((call: unknown[]) => call[0] === '/api/models')).toHaveLength(2);
  });

  it('clears a revoke that is still armed when the panel is left', async () => {
    await mount();
    const timers = vi.spyOn(window, 'setTimeout');
    panelHost.querySelector<HTMLButtonElement>('button[aria-label="Revoke"]')!.click();
    const armed = timers.mock.results.at(-1)!.value;
    const clear = vi.spyOn(window, 'clearTimeout');

    app.unmount();

    expect(clear).toHaveBeenCalledWith(armed);
  });
});
