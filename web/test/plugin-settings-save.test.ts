// A plugin page's Save sends only the keys that changed, and the reply settles
// only those keys. An edit typed into another field while the request was in
// flight is not in the request, so reloading the whole page from the reply
// would drop it and make the page read as saved.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { installPlugins } from '../src/plugins/registry';
import type { AdminPluginPage as PluginPageSpec, ArcPlugin } from '../src/plugins/types';
import PluginAdminPage from '../src/views/admin/PluginAdminPage.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};

const page: PluginPageSpec = { slug: 'tips', title: () => 'Tips', permission: 'users' };

let app: App | undefined;
let host: HTMLElement;
let panelHost: HTMLElement;
let actions: HTMLElement;

function field(label: string): HTMLInputElement {
  const node = [...host.querySelectorAll<HTMLElement>('.oa-field')]
    .find((candidate) => candidate.querySelector('.oa-field-label')?.textContent?.trim() === label);
  const found = node?.querySelector<HTMLInputElement>('input');
  if (!found) throw new Error(`Missing field: ${label}`);
  return found;
}

function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

async function mountPage(): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(ref(panelHost));
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(PluginAdminPage, { page });
  } });
  app.mount(host);
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panelHost, actions);
  installPlugins([{ name: 'tipper', settings: [{
    id: 'tips.main',
    page: 'plugin:tips',
    category: 'general',
    column: 0,
    title: () => 'Tip rules',
    controls: [
      { kind: 'text', key: 'tips.one', label: () => 'First' },
      { kind: 'text', key: 'tips.two', label: () => 'Second' },
    ],
    defaults: {},
  }] } satisfies ArcPlugin]);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  panelHost.remove();
  actions.remove();
  installPlugins([]);
  vi.restoreAllMocks();
});

describe('a plugin page\'s Save', () => {
  it('keeps an edit typed while Save was in flight, and settles only the keys it sent', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { 'tips.one': 'a', 'tips.two': 'b' } });
    const reply = deferred<{ settings: Record<string, string> }>();
    const saveSpy = vi.spyOn(adminApi, 'saveSettings').mockImplementation(() => reply.promise);
    await mountPage();

    type(field('First'), 'x');
    await nextTick();
    [...actions.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('save'))!
      .click();
    await nextTick();
    type(field('Second'), 'typed in flight');
    await nextTick();

    reply.resolve({ settings: { 'tips.one': 'x', 'tips.two': 'b' } });
    await settle();

    expect(saveSpy).toHaveBeenCalledWith({ 'tips.one': 'x' });
    expect(field('Second').value).toBe('typed in flight');
    // What was typed after the request is not saved yet, so the page reads as unsaved.
    expect(actions.querySelector('.oa-control-save-state')!.classList.contains('dirty')).toBe(true);
  });
});
