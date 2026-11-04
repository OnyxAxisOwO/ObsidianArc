import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { adminApi, type Provider } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { provideAdminView } from '../src/views/admin/adminView';
import AdminProviders from '../src/views/admin/AdminProviders.vue';

const provider: Provider = {
  id: 'prov-1', name: 'OpenRouter', kind: 'openai', base_url: 'https://openrouter.ai/api/v1',
  allow_insecure: false, api_key_hint: '****abcd', api_key_hints: ['****abcd'], key_rotation: 'sequential', headers: {}, anthropic_version: '',
  reasoning_style: 'auto', timeout_seconds: 120, enabled: true, sort_order: 0, model_count: 0,
  created_at: Date.now(), updated_at: Date.now(),
};

// The server's own sentence for this refusal. It is English on every instance,
// so the form must not show it to someone who chose another language.
const SERVER_MESSAGE = "Only a super administrator can set or change a provider's base URL.";

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  document.body.append(host, panels);
  vi.spyOn(adminApi, 'providers').mockResolvedValue({ providers: [provider] });
  vi.spyOn(adminApi, 'meta').mockResolvedValue({
    provider_kinds: ['openai', 'anthropic'], reasoning_styles: ['auto'],
  });
});

afterEach(async () => {
  app?.unmount();
  app = undefined;
  host.remove();
  panels.remove();
  await changeLanguage('en');
  vi.restoreAllMocks();
});

async function openProvider(): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
    return () => h(AdminProviders);
  } });
  app.mount(host);
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
  await nextTick();
}

function baseURLInput(): HTMLInputElement {
  const field = [...panels.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === t('baseURL'));
  const input = field?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error('Missing the base URL field');
  return input;
}

async function saveMovedAddress(): Promise<string> {
  vi.spyOn(adminApi, 'updateProvider').mockRejectedValue(
    new ApiError(403, 'super_admin_required', SERVER_MESSAGE),
  );
  await openProvider();
  const input = baseURLInput();
  input.value = 'https://collector.example.net/v1';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  const save = [...panels.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === t('save'));
  if (!save) throw new Error('Missing the save button');
  save.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
  return panels.querySelector('.oa-drawer-flash')?.textContent ?? '';
}

describe('providers page base URL refusal', () => {
  it('shows a delegate the refusal in the language they chose, not the server English', async () => {
    await changeLanguage('zh');
    const shown = await saveMovedAddress();
    expect(shown).toBe(t('providerBaseURLSuperAdmin'));
    expect(shown).not.toBe(SERVER_MESSAGE);
  });

  it('shows the refusal in English when English is chosen', async () => {
    const shown = await saveMovedAddress();
    expect(shown).toBe(t('providerBaseURLSuperAdmin'));
    expect(shown).not.toBe(SERVER_MESSAGE);
  });
});
