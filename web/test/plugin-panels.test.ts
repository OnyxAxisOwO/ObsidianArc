// What a plugin can open: a panel of its own for everybody signed in, and a
// record of one of its backoffice lists. Held here: the core draws the
// controls the plugin declared and hands back exactly what was entered; a
// required field stops the send; the reader's own records are listed; a row
// opens only when the plugin says rows open, shows its fields — a link only
// when it is http(s) — and an action reads the table again.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { installPlugins, userPanels } from '../src/plugins/registry';
import type { AdminListSpec, ArcPlugin, RecordDetail, UserFormValues, UserPanelSpec } from '../src/plugins/types';
import PluginUserPanel from '../src/views/PluginUserPanel.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import PluginList from '../src/views/admin/PluginList.vue';

let app: App | undefined;
let host: HTMLElement;
let panelHost: HTMLElement;

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/');
  await router.isReady();
  app = createApp({ setup() {
    providePanelHost(ref(panelHost));
    provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
    return () => h(component, props);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

function input(root: ParentNode, label: string): HTMLInputElement | HTMLTextAreaElement {
  const node = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((candidate) => candidate.querySelector('.oa-field-label')?.textContent?.trim() === label);
  const found = node?.querySelector<HTMLInputElement | HTMLTextAreaElement>('input, textarea');
  if (!found) throw new Error(`Missing field: ${label}`);
  return found;
}

function type(field: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  panelHost.remove();
  installPlugins([]);
  vi.restoreAllMocks();
});

function panel(run: (values: UserFormValues) => Promise<string>): UserPanelSpec {
  return {
    slug: 'tips',
    title: () => 'Tips',
    intro: () => 'Tell us something.',
    controls: [
      { kind: 'choice', key: 'kind', label: () => 'Kind', options: [
        { value: 'a', label: () => 'Alpha' }, { value: 'b', label: () => 'Beta' },
      ] },
      { kind: 'text', key: 'link', label: () => 'Link', required: true },
      { kind: 'textarea', key: 'body', label: () => 'Details' },
      { kind: 'switch', key: 'quiet', label: () => 'Quietly' },
      { kind: 'images', key: 'shots', label: () => 'Pictures', max: 2 },
    ],
    submit: { label: () => 'Send tip', run },
    mine: {
      title: () => 'Yours',
      empty: () => 'Nothing yet',
      load: async () => [{ id: '1', title: 'Earlier tip', badge: { label: 'Accepted', tone: 'default' }, note: 'Thanks!' }],
    },
  };
}

describe('a plugin\'s panel for everybody signed in', () => {
  it('is offered by the registry only while its plugin is loaded', () => {
    expect(userPanels()).toEqual([]);
    const spec = panel(async () => '');
    installPlugins([{ name: 'tipper', userPanels: [spec] } satisfies ArcPlugin]);
    expect(userPanels().map((entry) => entry.slug)).toEqual(['tips']);
  });

  it('draws the declared controls and sends what was entered', async () => {
    const run = vi.fn(async () => 'Got it');
    installPlugins([{ name: 'tipper', userPanels: [panel(run)] }]);
    await mount(PluginUserPanel, { slug: 'tips' });

    expect(panelHost.textContent).toContain('Tell us something.');
    expect(panelHost.textContent).toContain('Earlier tip');
    expect(panelHost.textContent).toContain('Thanks!');

    button(panelHost, 'Beta').click();
    type(input(panelHost, 'Link'), 'https://example.com/repo');
    type(input(panelHost, 'Details'), 'It calls the API.');
    await nextTick();
    button(panelHost, 'Send tip').click();
    await settle();

    expect(run).toHaveBeenCalledWith({
      kind: 'b', link: 'https://example.com/repo', body: 'It calls the API.', quiet: false, shots: [],
    });
    expect(panelHost.textContent).toContain('Got it');
    // Cleared for the next one.
    expect((input(panelHost, 'Link') as HTMLInputElement).value).toBe('');
  });

  it('does not send without a required field', async () => {
    const run = vi.fn(async () => '');
    installPlugins([{ name: 'tipper', userPanels: [panel(run)] }]);
    await mount(PluginUserPanel, { slug: 'tips' });
    button(panelHost, 'Send tip').click();
    await settle();
    expect(run).not.toHaveBeenCalled();
    expect(panelHost.textContent).toContain(t('pluginFieldRequired', { field: 'Link' }));
  });

  it('says so when no loaded plugin has the panel', async () => {
    await mount(PluginUserPanel, { slug: 'nowhere' });
    expect(panelHost.textContent).toContain(t('pluginPanelMissing'));
  });
});

describe('a record of a plugin\'s list, opened', () => {
  function list(detail?: AdminListSpec['detail']): { spec: AdminListSpec; load: ReturnType<typeof vi.fn> } {
    const load = vi.fn(async () => ({ rows: [{ id: 'r1', title: 'Leak' }], total: 1 }));
    return {
      load,
      spec: {
        id: 'reports', page: 'plugin:tips', title: () => 'Reports', empty: () => 'None',
        columns: [{ key: 'title', header: () => 'Title' }],
        load, cell: (key, row) => ({ title: String(row[key]) }),
        ...(detail ? { detail } : {}),
      },
    };
  }

  it('opens nothing when the plugin did not make rows open', async () => {
    const { spec } = list();
    await mount(PluginList, { spec });
    host.querySelector<HTMLElement>('tbody tr')!.click();
    await settle();
    expect(panelHost.querySelector('.oa-plugin-record')).toBeNull();
  });

  it('shows the fields, links only http(s), and reads the table again after an action', async () => {
    const run = vi.fn(async (draft: Record<string, string>) => `Rewarded ${draft.cards}`);
    const detail: RecordDetail = {
      title: 'Leak report',
      fields: [
        { label: 'Where', value: 'https://forum.example/thread', link: true },
        { label: 'Other', value: 'javascript:alert(1)', link: true },
      ],
      actions: [{
        id: 'accept', label: 'Accept',
        controls: [{ kind: 'text', key: 'cards', label: () => 'Cards' }],
        defaults: { cards: '1' },
        run,
      }],
    };
    const { spec, load } = list(async () => detail);
    await mount(PluginList, { spec });
    host.querySelector<HTMLElement>('tbody tr')!.click();
    await settle();

    const links = [...panelHost.querySelectorAll<HTMLAnchorElement>('.oa-plugin-record a')];
    expect(links.map((link) => link.getAttribute('href'))).toEqual(['https://forum.example/thread']);
    expect(panelHost.textContent).toContain('javascript:alert(1)');

    type(input(panelHost, 'Cards'), '3');
    await nextTick();
    button(panelHost, 'Accept').click();
    await settle();
    expect(run).toHaveBeenCalledWith({ cards: '3' });
    expect(panelHost.textContent).toContain('Rewarded 3');
    expect(load).toHaveBeenCalledTimes(2);
  });
});
