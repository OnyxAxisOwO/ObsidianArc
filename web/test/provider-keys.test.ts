import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { adminApi, type Provider } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { provideAdminView } from '../src/views/admin/adminView';
import AdminProviders from '../src/views/admin/AdminProviders.vue';

const provider: Provider = {
  id: 'prov-1', name: 'Pool', kind: 'openai', base_url: 'https://api.example.com/v1',
  allow_insecure: false, api_key_hint: '••••1111', api_key_hints: ['••••1111', '••••2222'],
  key_rotation: 'sequential', headers: {}, anthropic_version: '',
  reasoning_style: 'auto', timeout_seconds: 120, enabled: true, sort_order: 0, model_count: 0,
  created_at: Date.now(), updated_at: Date.now(),
};

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  document.body.append(host, panels);
  vi.spyOn(adminApi, 'providers').mockResolvedValue({ providers: [provider] });
  vi.spyOn(adminApi, 'meta').mockResolvedValue({ provider_kinds: ['openai', 'anthropic'], reasoning_styles: ['auto'] });
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  panels.remove();
  vi.restoreAllMocks();
});

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

async function openProvider(): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
    return () => h(AdminProviders);
  } });
  app.mount(host);
  await settle();
  host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
  await nextTick();
}

function keyInput(): HTMLInputElement {
  const input = [...panels.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === t('addAPIKey'))
    ?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error('Missing the key field');
  return input;
}

async function save(): Promise<Record<string, unknown>> {
  const update = vi.spyOn(adminApi, 'updateProvider').mockResolvedValue({ provider } as never);
  [...panels.querySelectorAll<HTMLButtonElement>('button')].find((node) => node.textContent?.trim() === t('save'))!.click();
  await settle();
  return update.mock.calls[0]![1] as Record<string, unknown>;
}

describe('provider API keys', () => {
  it('sends the kept positions with the hints they were read from, and the keys typed beside them', async () => {
    await openProvider();
    panels.querySelector<HTMLButtonElement>(`.oa-provider-key button[aria-label="${t('remove')}"]`)!.click();

    const input = keyInput();
    const pasted = new Event('paste', { bubbles: true, cancelable: true }) as ClipboardEvent;
    Object.defineProperty(pasted, 'clipboardData', { value: { getData: () => 'sk-new-3333\nsk-new-4444\n' } });
    input.dispatchEvent(pasted);
    await nextTick();
    expect(pasted.defaultPrevented).toBe(true);
    expect(panels.querySelectorAll('.oa-provider-key')).toHaveLength(4);

    // Left in the field when Save is pressed: meant to be added too.
    input.value = 'sk-new-5555';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    const payload = await save();
    expect(payload['keep_keys']).toEqual([1]);
    expect(payload['key_hints']).toEqual(['••••1111', '••••2222']);
    expect(payload['api_keys']).toEqual(['sk-new-3333', 'sk-new-4444', 'sk-new-5555']);
    expect(payload['key_rotation']).toBe('sequential');
  });

  it('leaves the keys out of a save that does not change them', async () => {
    await openProvider();
    const payload = await save();
    expect(payload).not.toHaveProperty('keep_keys');
    expect(payload).not.toHaveProperty('api_keys');
  });
});
