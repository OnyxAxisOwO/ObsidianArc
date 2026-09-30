import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import { adminApi, type AdminPlugin, type PluginChange } from '../src/admin/api';
import * as authApi from '../src/api/auth';
import type { Account, SiteInfo } from '../src/api/auth';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { installPlugins } from '../src/plugins/registry';
import type { ArcPlugin } from '../src/plugins/types';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import AdminPlugins from '../src/views/admin/AdminPlugins.vue';
import PluginAdminPage from '../src/views/admin/PluginAdminPage.vue';
import { provideAdminView } from '../src/views/admin/adminView';

// The plugins screen and the pages plugins bring: what an administrator can
// do to a plugin from the backoffice, what each change sends, and that the
// session's plugin list follows the change.

const ADMIN: Account = {
  id: 'admin', username: 'founder', email: '', nickname: '', avatar: '', bio: '',
  role: 'super_admin', group_id: '', group_expires_at: 0, group_name: '', status: 'active',
  created_at: 1, updated_at: 1, last_login_at: 1, email_verified: true,
  allow_stats: true, allow_delete_conversations: true, api_restricted: false,
  api_restricted_until: 0, api_restriction_source: '',
};

function plugin(name: string, state: AdminPlugin['state'], purges = false): AdminPlugin {
  return {
    name, state,
    manifest: { version: '1.1.0', title: { en: name.toUpperCase(), zh: name }, description: { en: `About ${name}`, zh: name } },
    installed: state === 'available' ? {} : { version: '1.1.0', at: 1_700_000_000_000 },
    contributions: {
      settings: [], fields: [], guards: [], captcha_modes: [], admin_routes: [], public_routes: [],
      commands: [], migrations: [], purges,
    },
  };
}

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;
let panels: HTMLElement;

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(component, props);
  } });
  app.mount(host);
  await settle();
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .filter((node) => node.textContent?.trim() === label);
  if (!found.length) throw new Error(`Missing button: ${label}`);
  return found[found.length - 1]!;
}

function card(name: string): HTMLElement {
  const found = host.querySelector<HTMLElement>(`#plugin-${name}`);
  if (!found) throw new Error(`Missing card: ${name}`);
  return found;
}

function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function change(next: AdminPlugin, rest: AdminPlugin[] = []): PluginChange {
  return { plugin: next, plugins: [next, ...rest] };
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  panels = document.createElement('div');
  document.body.append(host, actions, panels);
  adopt(ADMIN);
  site.value = { ...siteInfo.value };
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  installPlugins([]);
  forget();
  site.value = null;
});

describe('the plugins screen', () => {
  it('lists every plugin with its state and offers what each state allows', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({
      plugins: [plugin('gizmo', 'available'), plugin('widget', 'enabled'), { ...plugin('gone', 'enabled'), missing: true }],
    });
    await mount(AdminPlugins);

    expect(card('gizmo').textContent).toContain(t('pluginStateAvailable'));
    expect(button(card('gizmo'), t('pluginInstall'))).toBeTruthy();
    expect(card('widget').textContent).toContain(t('pluginStateEnabled'));
    expect(button(card('widget'), t('pluginDisable'))).toBeTruthy();
    expect(button(card('widget'), t('pluginUninstall'))).toBeTruthy();
    // A row for a plugin this build lacks says so, and offers nothing.
    expect(card('gone').textContent).toContain(t('pluginMissing'));
    expect(card('gone').querySelectorAll('button')).toHaveLength(0);
  });

  it('installs with the first settings the plugin declares, and reloads the session\'s plugins', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('gizmo', 'available')] });
    const installed = vi.spyOn(adminApi, 'installPlugin').mockResolvedValue(change(plugin('gizmo', 'enabled')));
    const refreshed = vi.spyOn(authApi, 'fetchSite').mockResolvedValue({
      ...siteInfo.value, plugins: { gizmo: {} },
    } as SiteInfo);
    await mount(AdminPlugins);

    button(card('gizmo'), t('pluginInstall')).click();
    await settle();
    expect(panels.textContent).toContain(t('pluginEnableNow'));
    button(panels, t('pluginInstall')).click();
    await settle();

    // This build has no browser half for the plugin, so there are no first
    // settings to send.
    expect(installed).toHaveBeenCalledWith('gizmo', true, {});
    expect(refreshed).toHaveBeenCalled();
    expect(card('gizmo').textContent).toContain(t('pluginStateEnabled'));
  });

  it('will not switch a plugin off for an account without two-step verification', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('widget', 'enabled')] });
    const disabled = vi.spyOn(adminApi, 'disablePlugin');
    await mount(AdminPlugins);

    button(card('widget'), t('pluginDisable')).click();
    await settle();
    expect(panels.textContent).toContain(t('pluginTwoFactorMissing'));
    expect([...panels.querySelectorAll('button')].some((node) => node.textContent?.trim() === t('pluginDisable'))).toBe(false);
    expect(disabled).not.toHaveBeenCalled();
  });

  it('sends the code to switch a plugin off', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('widget', 'enabled')] });
    const disabled = vi.spyOn(adminApi, 'disablePlugin').mockResolvedValue(change(plugin('widget', 'disabled')));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);

    button(card('widget'), t('pluginDisable')).click();
    await settle();
    type(panels.querySelector<HTMLInputElement>('input')!, ' 123456 ');
    button(panels, t('pluginDisable')).click();
    await settle();
    expect(disabled).toHaveBeenCalledWith('widget', '123456');
    expect(card('widget').textContent).toContain(t('pluginStateDisabled'));
  });

  it('asks in place before uninstalling, and sends whether the data goes too', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('gizmo', 'disabled', true)] });
    const removed = vi.spyOn(adminApi, 'uninstallPlugin').mockResolvedValue(change(plugin('gizmo', 'available')));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);

    button(card('gizmo'), t('pluginUninstall')).click();
    await settle();
    expect(panels.textContent).toContain(t('pluginPurgeHint'));
    const purge = panels.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    purge.click();
    await nextTick();
    type(panels.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!, '123456');
    button(panels, t('pluginUninstall')).click();
    await nextTick();
    // Armed, not run.
    expect(removed).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('pluginPurgeConfirm', { name: 'GIZMO' }));
    button(panels, t('pluginUninstall')).click();
    await settle();
    expect(removed).toHaveBeenCalledWith('gizmo', true, '123456');
  });

  it('keeps the buttons a grant does not cover out of reach', async () => {
    adopt({ ...ADMIN, role: 'admin', admin_permissions: ['plugins'] });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('gizmo', 'available'), plugin('widget', 'enabled')] });
    await mount(AdminPlugins);
    expect(button(card('gizmo'), t('pluginInstall')).disabled).toBe(true);
    expect(button(card('widget'), t('pluginDisable')).disabled).toBe(true);
    expect(button(card('widget'), t('pluginUninstall')).disabled).toBe(true);
    expect(button(card('widget'), t('pluginDetails')).disabled).toBe(false);
  });
});

const gizmo: ArcPlugin = {
  name: 'gizmo',
  adminPages: [{ slug: 'gizmo', title: () => 'Gizmo', permission: 'plugins' }],
  settings: [{
    id: 'secGizmo',
    page: 'plugin:gizmo',
    category: 'general',
    column: 0,
    title: () => 'Gizmo',
    defaults: { 'gizmo.mode': 'fast' },
    controls: [
      { kind: 'secret', key: 'gizmo.token', label: () => 'Gizmo token' },
      {
        kind: 'select', key: 'gizmo.mode', label: () => 'Gizmo mode',
        options: [{ value: 'fast', label: () => 'Fast' }, { value: 'slow', label: () => 'Slow' }],
      },
    ],
  }],
  lists: [{
    id: 'secGizmoList',
    page: 'plugin:gizmo',
    title: () => 'Gizmo list',
    empty: () => 'No gizmos yet.',
    columns: [{ key: 'name', header: () => 'Name' }],
    async load() { return { rows: [], total: 0 }; },
    cell: () => ({ title: '' }),
  }],
};

describe('a page a plugin brings', () => {
  it('draws the plugin\'s cards and lists placed on it, and saves its settings', async () => {
    installPlugins([gizmo]);
    const page = gizmo.adminPages![0]!;
    vi.spyOn(adminApi, 'settings').mockResolvedValue({
      settings: { 'gizmo.token': '••••', 'gizmo.mode': 'slow' },
    });
    const saved = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount(PluginAdminPage, { page });

    const section = host.querySelector<HTMLElement>('#secGizmo');
    if (!section) throw new Error('the plugin card was not drawn on its page');
    expect(section.textContent).toContain('Slow');
    expect(host.textContent).toContain('No gizmos yet.');
    // Nothing edited yet, so nothing to save.
    expect(button(actions, t('save')).disabled).toBe(true);

    type(section.querySelector<HTMLInputElement>('input[type="password"]')!, 'a-new-token');
    await nextTick();
    button(actions, t('save')).click();
    await settle();
    expect(saved).toHaveBeenCalledWith({ 'gizmo.token': 'a-new-token', 'gizmo.mode': 'slow' });
  });
});
