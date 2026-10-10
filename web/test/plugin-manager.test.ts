import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import { adminApi, type AdminPlugin, type PluginChange, type PluginPreview } from '../src/admin/api';
import * as authApi from '../src/api/auth';
import type { Account, SiteInfo } from '../src/api/auth';
import { api, ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { installPlugins } from '../src/plugins/registry';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import AdminPlugins from '../src/views/admin/AdminPlugins.vue';
import PluginAdminPage from '../src/views/admin/PluginAdminPage.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import example from './fixtures/example.plugin';

// The core ships no plugin, so there is no module for the install panel to
// find; hand it the made-up one under its own name, and leave everything else
// in the registry as it is.
vi.mock('../src/plugins/registry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../src/plugins/registry')>();
  const { default: fixture } = await import('./fixtures/example.plugin');
  return { ...actual, loadPluginModule: async (name: string) => (name === 'example' ? fixture : null) };
});

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
    name, state, kind: 'builtin', permissions: [],
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
      plugins: [plugin('alpha', 'available'), plugin('beta', 'enabled'), { ...plugin('gone', 'enabled'), missing: true }],
    });
    await mount(AdminPlugins);

    expect(card('alpha').textContent).toContain(t('pluginStateAvailable'));
    expect(button(card('alpha'), t('pluginInstall'))).toBeTruthy();
    expect(card('beta').textContent).toContain(t('pluginStateEnabled'));
    expect(button(card('beta'), t('pluginDisable'))).toBeTruthy();
    expect(button(card('beta'), t('pluginUninstall'))).toBeTruthy();
    // A row for a plugin this build lacks says so, and offers nothing.
    expect(card('gone').textContent).toContain(t('pluginMissing'));
    expect(card('gone').querySelectorAll('button')).toHaveLength(0);
  });

  it('installs with the first settings the plugin declares, and reloads the session\'s plugins', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('example', 'available')], two_factor_required: false });
    const installed = vi.spyOn(adminApi, 'installPlugin').mockResolvedValue(change(plugin('example', 'enabled')));
    const refreshed = vi.spyOn(authApi, 'fetchSite').mockResolvedValue({
      ...siteInfo.value, plugins: { example: {} },
    } as SiteInfo);
    await mount(AdminPlugins);

    button(card('example'), t('pluginInstall')).click();
    await settle();
    // The plugin's own settings section, drawn in the panel from its module.
    expect(panels.textContent).toContain(t('pluginEnableNow'));
    expect(panels.textContent).toContain('Example webhook token');
    // The policy is off, so no code is asked for and none is sent.
    expect(panels.textContent).not.toContain(t('pluginTwoFactorCode'));
    button(panels, t('pluginInstall')).click();
    await settle();

    // Defaults for the selects and switches, and no untouched secret.
    expect(installed).toHaveBeenCalledWith('example', true, {
      'example.base_url': '', 'example.site': '', 'example.on_login': 'false',
      'example.requirement': 'off', 'example.mode': 'disable',
    }, '');
    expect(refreshed).toHaveBeenCalled();
    expect(card('example').textContent).toContain(t('pluginStateEnabled'));
  });

  it('asks for the two-step code to install a plugin when the instance requires it, and sends it', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('example', 'available')], two_factor_required: true });
    const installed = vi.spyOn(adminApi, 'installPlugin').mockResolvedValue(change(plugin('example', 'enabled')));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: { example: {} } } as SiteInfo);
    await mount(AdminPlugins);

    button(card('example'), t('pluginInstall')).click();
    await settle();
    // The plugin's own settings are inputs too, so the code is found by its autocomplete hint.
    type(panels.querySelector<HTMLInputElement>('input[autocomplete="one-time-code"]')!, ' 123456 ');
    button(panels, t('pluginInstall')).click();
    await settle();
    expect(installed).toHaveBeenCalledWith('example', true, expect.any(Object), '123456');
    expect(card('example').textContent).toContain(t('pluginStateEnabled'));
  });

  it('will not install a plugin for an account without two-step verification when the instance requires it', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('example', 'available')], two_factor_required: true });
    const installed = vi.spyOn(adminApi, 'installPlugin');
    await mount(AdminPlugins);

    button(card('example'), t('pluginInstall')).click();
    await settle();
    expect(panels.textContent).toContain(t('pluginInstallTwoFactorMissing'));
    expect(panels.querySelector('input[autocomplete="one-time-code"]')).toBeNull();
    expect(() => button(panels, t('pluginInstall'))).toThrow();
    expect(installed).not.toHaveBeenCalled();
  });

  it('will not switch a plugin off for an account without two-step verification', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('beta', 'enabled')] });
    const disabled = vi.spyOn(adminApi, 'disablePlugin');
    await mount(AdminPlugins);

    button(card('beta'), t('pluginDisable')).click();
    await settle();
    expect(panels.textContent).toContain(t('pluginTwoFactorMissing'));
    expect([...panels.querySelectorAll('button')].some((node) => node.textContent?.trim() === t('pluginDisable'))).toBe(false);
    expect(disabled).not.toHaveBeenCalled();
  });

  it('sends the code to switch a plugin off', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('beta', 'enabled')] });
    const disabled = vi.spyOn(adminApi, 'disablePlugin').mockResolvedValue(change(plugin('beta', 'disabled')));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);

    button(card('beta'), t('pluginDisable')).click();
    await settle();
    type(panels.querySelector<HTMLInputElement>('input')!, ' 123456 ');
    button(panels, t('pluginDisable')).click();
    await settle();
    expect(disabled).toHaveBeenCalledWith('beta', '123456');
    expect(card('beta').textContent).toContain(t('pluginStateDisabled'));
  });

  it('asks in place before uninstalling, and sends whether the data goes too', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('alpha', 'disabled', true)] });
    const removed = vi.spyOn(adminApi, 'uninstallPlugin').mockResolvedValue(change(plugin('alpha', 'available')));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);

    button(card('alpha'), t('pluginUninstall')).click();
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
    expect(panels.textContent).toContain(t('pluginPurgeConfirm', { name: 'ALPHA' }));
    button(panels, t('pluginUninstall')).click();
    await settle();
    expect(removed).toHaveBeenCalledWith('alpha', true, '123456');
  });

  it('keeps the buttons a grant does not cover out of reach', async () => {
    adopt({ ...ADMIN, role: 'admin', admin_permissions: ['plugins'] });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [plugin('alpha', 'available'), plugin('beta', 'enabled')] });
    await mount(AdminPlugins);
    expect(button(card('alpha'), t('pluginInstall')).disabled).toBe(true);
    expect(button(card('beta'), t('pluginDisable')).disabled).toBe(true);
    expect(button(card('beta'), t('pluginUninstall')).disabled).toBe(true);
    expect(button(card('beta'), t('pluginDetails')).disabled).toBe(false);
  });
});

describe('a page a plugin brings', () => {
  it('draws the plugin\'s cards and lists placed on it, and saves its settings', async () => {
    installPlugins([example]);
    const page = example.adminPages![0]!;
    vi.spyOn(adminApi, 'settings').mockResolvedValue({
      settings: { 'example.requirement': 'optional', 'example.webhook_token': '••••', 'example.mode': 'disable' },
    });
    vi.spyOn(api, 'get').mockResolvedValue({ records: [], total: 0 });
    const saved = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    await mount(PluginAdminPage, { page });

    const section = host.querySelector<HTMLElement>('#secExampleGroup');
    if (!section) throw new Error('the plugin card was not drawn on its page');
    expect(section.textContent).toContain('Optional');
    expect(host.textContent).toContain('No records yet.');
    // Nothing edited yet, so nothing to save.
    expect(button(actions, t('save')).disabled).toBe(true);

    type(section.querySelector<HTMLInputElement>('input[type="password"]')!, 'a-new-token');
    await nextTick();
    button(actions, t('save')).click();
    await settle();
    expect(saved).toHaveBeenCalledWith({
      'example.requirement': 'optional', 'example.webhook_token': 'a-new-token', 'example.mode': 'disable',
    });
  });
});


// --- packages ---------------------------------------------------------------

function pack(name: string, state: AdminPlugin['state'], extra: Partial<AdminPlugin> = {}): AdminPlugin {
  return {
    ...plugin(name, state, true),
    kind: 'package', source: 'upload', permissions: ['db', 'sessions'], sha256: 'ab'.repeat(32), size: 5000, has_ui: true,
    ...extra,
  };
}

function preview(over: Partial<PluginPreview> = {}): PluginPreview {
  return {
    token: 'tok-1',
    manifest: {
      name: 'demo', version: '1.2.0', title: { en: 'Demo', zh: '演示' }, description: { en: 'Does demo things.', zh: '做演示的事。' },
      author: 'Ada', permissions: ['db', 'network', 'notify'], ui: { module: 'web/ui.js' },
      settings: [
        { key: 'demo.mode', default: 'open', enum: ['open', 'closed'], initial: true, label: { en: 'Mode', zh: '模式' } },
        { key: 'demo.token', secret: true, initial: true, label: { en: 'Token' } },
        { key: 'demo.hidden', default: 'x' },
      ],
    },
    sha256: 'cd'.repeat(32), size: 2_500_000, action: 'install', warnings: ['ui'], ...over,
  };
}

/** A file dragged over the page, as the browser reports it. */
function dragOver(type: string, file?: File): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'dataTransfer', { value: { types: ['Files'], files: file ? [file] : [] } });
  window.dispatchEvent(event);
  return event;
}

function dropFile(file: File): void {
  dragOver('dragenter');
  dragOver('drop', file);
}

describe('a plugin package', () => {
  it('is listed with a switch, and the switch enables and disables it', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [pack('demo', 'disabled')] });
    const enabled = vi.spyOn(adminApi, 'enablePlugin').mockResolvedValue(change(pack('demo', 'enabled')));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);

    const toggle = card('demo').querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(toggle.checked).toBe(false);
    toggle.click();
    await settle();
    expect(enabled).toHaveBeenCalledWith('demo');
    expect(card('demo').querySelector<HTMLInputElement>('input[type="checkbox"]')!.checked).toBe(true);
    // A package offers Remove where a built-in plugin offers Uninstall, and no Install.
    expect(button(card('demo'), t('pluginRemove'))).toBeTruthy();
    expect(() => button(card('demo'), t('pluginUninstall'))).toThrow();

    // Switching off asks first: the click alone changes nothing.
    const disabled = vi.spyOn(adminApi, 'disablePlugin').mockResolvedValue(change(pack('demo', 'disabled')));
    adopt({ ...ADMIN, two_factor_at: 1 });
    card('demo').querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    await settle();
    expect(disabled).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('pluginDisableTitle', { name: 'DEMO' }));
  });

  it('links its homepage only when that is a web address', async () => {
    const withHomepage = (name: string, homepage: string): AdminPlugin => {
      const base = pack(name, 'enabled');
      return { ...base, manifest: { ...base.manifest, homepage } };
    };
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({
      plugins: [
        withHomepage('safe', 'https://example.org/safe'),
        withHomepage('script', 'javascript:alert(document.domain)'),
      ],
    });
    await mount(AdminPlugins);

    button(card('safe'), t('pluginDetails')).click();
    await settle();
    const link = document.body.querySelector<HTMLAnchorElement>('a.oa-plugin-meta-link');
    expect(link?.getAttribute('href')).toBe('https://example.org/safe');

    button(card('script'), t('pluginDetails')).click();
    await settle();
    // An address that does something when clicked is shown as text, not made a link.
    expect(document.body.querySelector('a[href^="javascript:"]')).toBeNull();
    expect(document.body.textContent).toContain('javascript:alert(document.domain)');
  });

  it('can be removed, and disappears from the list with or without its data', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [pack('demo', 'enabled')] });
    const removed = vi.spyOn(adminApi, 'uninstallPlugin').mockResolvedValue({ plugin: null, plugins: [] });
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);

    button(card('demo'), t('pluginRemove')).click();
    await settle();
    // The package's own wording, not the built-in plugin's.
    expect(panels.textContent).toContain(t('pluginRemoveTitle', { name: 'DEMO' }));
    expect(panels.textContent).toContain(t('pluginRemoveHint'));
    panels.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    await nextTick();
    type(panels.querySelector<HTMLInputElement>('input:not([type="checkbox"])')!, '123456');
    button(panels, t('pluginRemove')).click();
    await nextTick();
    expect(removed).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('pluginRemovePurgeConfirm', { name: 'DEMO' }));
    button(panels, t('pluginRemove')).click();
    await settle();
    expect(removed).toHaveBeenCalledWith('demo', true, '123456');
    expect(host.querySelector('#plugin-demo')).toBeNull();
    expect(host.textContent).toContain(t('pluginPackagesEmpty'));
  });

  it('is read, not installed, when a file is dropped on the page', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [] });
    const previewed = vi.spyOn(adminApi, 'previewPlugin').mockResolvedValue({ preview: preview() });
    const installed = vi.spyOn(adminApi, 'installPluginPackage');
    await mount(AdminPlugins);

    const file = new File(['zip'], 'demo.arcx');
    const enter = dragOver('dragenter');
    expect(enter.defaultPrevented).toBe(true);
    await nextTick();
    expect(document.body.textContent).toContain(t('pluginDropRelease'));
    dragOver('drop', file);
    await settle();

    expect(previewed).toHaveBeenCalledWith(file);
    expect(installed).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain(t('pluginDropRelease'));
    // What it is, and above all what it asks for.
    const panel = panels.textContent!;
    expect(panel).toContain(t('pluginPackageInstallTitle', { name: 'Demo' }));
    expect(panel).toContain('v1.2.0');
    expect(panel).toContain(t('pluginPermDb'));
    expect(panel).toContain(t('pluginPermNetwork'));
    expect(panel).toContain(t('pluginPermNotify'));
    expect(panels.querySelectorAll('.oa-plugin-permissions li.risky')).toHaveLength(2);
    expect(panel).toContain(t('pluginRunsInPage'));
    expect(panel).toContain('cdcdcdcdcdcdcdcd');
    // Only the settings it asks to be asked about.
    expect(panel).toContain('Mode');
    expect(panel).toContain('Token');
    expect(panel).not.toContain('demo.hidden');
  });

  it('installs what was shown, with the settings chosen and the switch as set', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [], two_factor_required: false });
    vi.spyOn(adminApi, 'previewPlugin').mockResolvedValue({ preview: preview() });
    const installed = vi.spyOn(adminApi, 'installPluginPackage')
      .mockResolvedValue({ ...change(pack('demo', 'enabled')), did: 'install' });
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);
    dropFile(new File(['zip'], 'demo.arcx'));
    await settle();

    type(panels.querySelector<HTMLInputElement>('input[type="password"]')!, 's3cret');
    button(panels, t('pluginInstall')).click();
    await settle();

    // Defaults for what was not touched; the secret because it was typed.
    expect(installed).toHaveBeenCalledWith('tok-1', true, { 'demo.mode': 'open', 'demo.token': 's3cret' }, '');
    expect(panels.textContent).not.toContain(t('pluginPackageInstallTitle', { name: 'Demo' }));
    expect(host.textContent).toContain(t('pluginDone', { name: 'Demo', version: '1.2.0' }));
    expect(card('demo').textContent).toContain(t('pluginStateEnabled'));
  });

  it('says it is an update when one of that name is installed, and asks for no settings again', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [pack('demo', 'enabled')], two_factor_required: false });
    vi.spyOn(adminApi, 'previewPlugin').mockResolvedValue({
      preview: preview({
        action: 'update', warnings: ['ui', 'downgrade'],
        existing: { version: '1.0.0', state: 'enabled', source: 'upload', sha256: 'ee'.repeat(32) },
      }),
    });
    const installed = vi.spyOn(adminApi, 'installPluginPackage')
      .mockResolvedValue({ ...change(pack('demo', 'enabled')), did: 'update' });
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);
    dropFile(new File(['zip'], 'demo.arcx'));
    await settle();

    const panel = panels.textContent!;
    expect(panel).toContain(t('pluginPackageUpdateTitle', { name: 'Demo' }));
    expect(panel).toContain(t('pluginPackageUpdateHint', { from: '1.0.0', to: '1.2.0' }));
    expect(panel).toContain(t('pluginPackageDowngrade'));
    button(panels, t('pluginUpdate')).click();
    await settle();
    expect(installed).toHaveBeenCalled();
    expect(host.textContent).toContain(t('pluginDoneUpdate', { name: 'Demo', version: '1.2.0' }));
  });

  it('refuses to install a file that is already installed, byte for byte', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [pack('demo', 'enabled')] });
    vi.spyOn(adminApi, 'previewPlugin').mockResolvedValue({ preview: preview({ action: 'unchanged' }) });
    await mount(AdminPlugins);
    dropFile(new File(['zip'], 'demo.arcx'));
    await settle();
    expect(panels.textContent).toContain(t('pluginPackageUnchanged'));
    expect(() => button(panels, t('pluginUpdate'))).toThrow();
  });

  it('turns away a file that is not a package, and shows the server\'s reason when it refuses one', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [] });
    const previewed = vi.spyOn(adminApi, 'previewPlugin');
    await mount(AdminPlugins);

    dropFile(new File(['x'], 'notes.txt'));
    await settle();
    expect(previewed).not.toHaveBeenCalled();
    expect(host.textContent).toContain(t('pluginUploadWrongType'));

    previewed.mockRejectedValue(new ApiError(400, 'invalid_package', 'plugin.wasm is larger than 32768 KiB'));
    dropFile(new File(['x'], 'big.arcx'));
    await settle();
    expect(host.textContent).toContain('plugin.wasm is larger than 32768 KiB');
    expect(panels.textContent).not.toContain(t('pluginPackageInstallTitle', { name: 'Demo' }));
  });

  it('is not accepted from somebody who may not manage plugins', async () => {
    adopt({ ...ADMIN, role: 'admin', admin_permissions: ['plugins'] });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [] });
    const previewed = vi.spyOn(adminApi, 'previewPlugin');
    await mount(AdminPlugins);
    expect(host.textContent).not.toContain(t('pluginDropTitle'));
    dropFile(new File(['zip'], 'demo.arcx'));
    await settle();
    expect(previewed).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain(t('pluginDropRelease'));
  });

  it('asks for the two-step code when the instance wants one, and sends it', async () => {
    adopt({ ...ADMIN, two_factor_at: 1 });
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [], two_factor_required: true });
    vi.spyOn(adminApi, 'previewPlugin').mockResolvedValue({ preview: preview() });
    const installed = vi.spyOn(adminApi, 'installPluginPackage')
      .mockResolvedValue({ ...change(pack('demo', 'enabled')), did: 'install' });
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue({ ...siteInfo.value, plugins: {} } as SiteInfo);
    await mount(AdminPlugins);
    dropFile(new File(['zip'], 'demo.arcx'));
    await settle();

    expect(panels.textContent).toContain(t('pluginTwoFactorCode'));
    const inputs = panels.querySelectorAll<HTMLInputElement>('input:not([type="checkbox"]):not([type="password"])');
    type(inputs[inputs.length - 1]!, ' 123456 ');
    button(panels, t('pluginInstall')).click();
    await settle();
    expect(installed.mock.calls[0]![3]).toBe('123456');
  });

  it('cannot be installed by an account with no two-step verification when one is required', async () => {
    vi.spyOn(adminApi, 'plugins').mockResolvedValue({ plugins: [], two_factor_required: true });
    vi.spyOn(adminApi, 'previewPlugin').mockResolvedValue({ preview: preview() });
    await mount(AdminPlugins);
    dropFile(new File(['zip'], 'demo.arcx'));
    await settle();
    expect(panels.textContent).toContain(t('pluginTwoFactorMissing'));
    expect(() => button(panels, t('pluginInstall'))).toThrow();
  });
});
