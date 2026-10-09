import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type AdminMailSettings, type AdminUserCheckSettings } from '../src/admin/api';
import * as authApi from '../src/api/auth';
import type { Account } from '../src/api/auth';
import { api, ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { describeNotification } from '../src/lib/notification-text';
import { refusalText } from '../src/lib/refusal';
import {
  installPlugins, loadPlugins, pinnedProvider, pluginHost, pluginInviteeNote, pluginOAuthError, pluginRefusal,
  pluginStrings, plugins, setModuleImporter,
} from '../src/plugins/registry';
import type { ArcPlugin } from '../src/plugins/types';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import AuthView from '../src/views/AuthView.vue';
import AdminSecurity from '../src/views/admin/AdminSecurity.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import PluginList from '../src/views/admin/PluginList.vue';
import PluginUserActions from '../src/views/admin/PluginUserActions.vue';
import example from './fixtures/example.plugin';

// The plugin framework's browser half: which plugins load, how the core
// forms draw what a plugin declares, and that a plugin's settings travel
// with the page's own save. The plugin is a made-up one (fixtures/), because
// the core ships none; each real plugin's tests live with it.

const ADMIN: Account = {
  id: 'admin', username: 'founder', email: '', nickname: '', avatar: '', bio: '',
  role: 'super_admin', group_id: '', group_expires_at: 0, group_name: '', status: 'active',
  created_at: 1, updated_at: 1, last_login_at: 1, email_verified: true,
  allow_stats: true, allow_delete_conversations: true, api_restricted: false,
  api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/');
  await router.isReady();
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(component, props);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

function field(root: ParentNode, label: string): HTMLInputElement {
  const node = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((candidate) => candidate.querySelector('.oa-field-label')?.textContent === label);
  const input = node?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error(`Missing field: ${label}`);
  return input;
}

function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  installPlugins([]);
  setModuleImporter(null);
  forget();
  site.value = null;
});

describe('loading plugins', () => {
  it('skips a name this build has no code for', async () => {
    await loadPlugins({ nosuchplugin: {} });
    expect(plugins()).toEqual([]);
  });

  it('loads nothing for a server that names nothing', async () => {
    await loadPlugins(undefined);
    expect(plugins()).toEqual([]);
  });
});

describe('a package\'s browser half', () => {
  it('is fetched from the address the server gave and called with what the page lends it', async () => {
    const seen: string[] = [];
    let lent: ReturnType<typeof pluginHost> | undefined;
    setModuleImporter(async (url) => {
      seen.push(url);
      return { default: (host: ReturnType<typeof pluginHost>) => { lent = host; return example; } };
    });
    await loadPlugins({ example: { _ui: '/api/x/example/web/ui.js?v=abc', mode: 'open' } });

    expect(seen).toEqual(['/api/x/example/web/ui.js?v=abc']);
    expect(plugins().map((plugin) => plugin.name)).toEqual(['example']);
    // The block's other keys stay what they were: the page's own config for the plugin.
    expect(lent!.api.get).toBe(api.get);
    expect(typeof lent!.icons.IconUsers).toBe('function');
    expect(lent!.format.absoluteTime(0)).toEqual(expect.any(String));
    expect(lent!.language()).toBe('en');
    const s = lent!.strings({ hello: 'Hello {name}' }, { hello: '你好 {name}' });
    expect(s('hello', { name: 'Ada' })).toBe('Hello Ada');
    await changeLanguage('zh');
    expect(lent!.language()).toBe('zh');
    expect(s('hello', { name: 'Ada' })).toBe('你好 Ada');
  });

  it('may be built asynchronously', async () => {
    setModuleImporter(async () => ({ default: async () => example }));
    await loadPlugins({ example: { _ui: '/x.js' } });
    expect(plugins()).toHaveLength(1);
  });

  it('is left out, with the rest still loading, when it cannot be used', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    setModuleImporter(async (url) => {
      if (url.includes('broken')) throw new Error('404');
      if (url.includes('notafunction')) return { default: { name: 'notafunction' } };
      if (url.includes('impostor')) return { default: () => ({ name: 'somebodyelse' }) };
      return { default: () => example };
    });
    await loadPlugins({
      broken: { _ui: '/broken.js' }, notafunction: { _ui: '/notafunction.js' },
      impostor: { _ui: '/impostor.js' }, example: { _ui: '/example.js' },
    });
    expect(plugins().map((plugin) => plugin.name)).toEqual(['example']);
    expect(warn).toHaveBeenCalledTimes(3);
  });
});

describe('what the core asks a plugin', () => {
  it('words a field\'s refusals from its spec, and a plugin\'s own codes from its table', () => {
    installPlugins([example]);
    const ref = example.fields!['ref']!;
    expect(pluginRefusal('ref_taken')).toBe(ref.taken());
    expect(pluginRefusal('invalid_ref')).toBe(ref.invalid());
    expect(pluginRefusal('ref_required')).toBe(ref.required());
    expect(pluginRefusal('example_blocked')).toBe(example.refusals!['example_blocked']!());
    expect(pluginRefusal('badge_taken')).toBeNull();
    expect(refusalText(new ApiError(409, 'ref_taken', 'That reference is already registered.', {}))).toBe(ref.taken());
    expect(pluginOAuthError('ref_required')).toBe(example.oauthErrors!['ref_required']!());
    expect(pinnedProvider('oidc')?.hint()).toBe(example.pinnedProviders!['oidc']!.hint());
    expect(pinnedProvider('github')).toBeNull();
  });

  it('says nothing of a plugin that is not loaded', () => {
    expect(pluginRefusal('ref_taken')).toBeNull();
    expect(pinnedProvider('oidc')).toBeNull();
    // The code a server without the plugin would never send reads as the
    // generic refusal rather than a key.
    expect(refusalText(new ApiError(409, 'ref_taken', 'x', {}))).toBe(t('authRequestFailed'));
  });

  it('lets a plugin word its own notifications and invitee rows', () => {
    installPlugins([example]);
    const text = describeNotification({
      id: 'n1', kind: 'example_departed', params: { username: 'ada', count: 2 },
      link: '', created_at: 1, read: false,
    } as unknown as Parameters<typeof describeNotification>[0]);
    expect(text.body).toContain('ada');
    expect(text.body).toContain('2');
    expect(pluginInviteeNote({ flagged: true })).toContain('account removed');
    expect(pluginInviteeNote({ counted: true })).toBeNull();
  });

  it('keeps a plugin\'s dictionary complete and switches it with the language', async () => {
    const s = pluginStrings({ hello: 'Hello {name}' }, { hello: '你好 {name}' });
    expect(s('hello', { name: 'Ada' })).toBe('Hello Ada');
    await changeLanguage('zh');
    expect(s('hello', { name: 'Ada' })).toBe('你好 Ada');
  });
});

describe('a plugin account field on the sign-up form', () => {
  beforeEach(() => {
    installPlugins([example]);
  });

  it('is drawn only where the server asks for it, and marked required when it must be', async () => {
    site.value = { ...siteInfo.value, fields: { ref: 'off' } };
    await mount(AuthView, { mode: 'register' });
    expect(() => field(host, example.fields!['ref']!.label())).toThrow();
    app!.unmount();
    app = undefined;

    site.value = { ...siteInfo.value, fields: { ref: 'required' } };
    await mount(AuthView, { mode: 'register' });
    expect(field(host, example.fields!['ref']!.label()).required).toBe(true);
  });

  it('is checked before submitting and sent under "fields"', async () => {
    site.value = { ...siteInfo.value, fields: { ref: 'required' } };
    const register = vi.spyOn(authApi, 'register').mockResolvedValue({ user: ADMIN });
    await mount(AuthView, { mode: 'register' });
    const ref = example.fields!['ref']!;

    type(field(host, t('username')), 'newperson');
    type(field(host, t('password')), 'a-strong-password');
    button(host, t('createAccount')).click();
    await settle();
    expect(register).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(ref.required());

    type(field(host, ref.label()), '0123');
    button(host, t('createAccount')).click();
    await settle();
    expect(register).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(ref.invalid());

    type(field(host, ref.label()), '10001');
    button(host, t('createAccount')).click();
    await settle();
    expect(register).toHaveBeenCalledWith(expect.objectContaining({ fields: { ref: '10001' } }));
  });

  it('is not drawn for a field the server names but no plugin describes', async () => {
    installPlugins([]);
    site.value = { ...siteInfo.value, fields: { ref: 'required' } };
    await mount(AuthView, { mode: 'register' });
    expect(host.querySelector('input[name="field-ref"]')).toBeNull();
  });
});

describe('a plugin\'s settings on a backoffice page', () => {
  const MAIL: AdminMailSettings = {
    host: '', port: 465, username: '', from: '', implicit_tls: true, public_url: '', password_set: false,
  };
  const USER_CHECK: AdminUserCheckSettings = {
    enabled: false, exempt_domains: [], failure_mode: 'reject', api_key_set: false,
  };

  async function mountSecurity(values: Record<string, string>): Promise<void> {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: values, groups: [], mail_configured: false });
    vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
    vi.spyOn(adminApi, 'securityEvents').mockResolvedValue({ events: [], total: 0, limit: 20, offset: 0 });
    vi.spyOn(adminApi, 'applications').mockResolvedValue({ applications: [], issuer: '', scopes: [] });
    vi.spyOn(adminApi, 'twoFactorAdoption').mockResolvedValue({
      policy: 'optional', accounts: 0, enabled: 0, admins: 0, admins_enabled: 0,
      admins_without: [], remember_days: 0, issuer_fallback: 'Arc', available: true,
    });
    vi.spyOn(adminApi, 'mail').mockResolvedValue(MAIL);
    vi.spyOn(adminApi, 'userCheck').mockResolvedValue(USER_CHECK);
    await mount(AdminSecurity);
  }

  function openCategory(label: string): void {
    const tab = [...host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab')]
      .find((node) => node.querySelector('strong')?.textContent === label);
    if (!tab) throw new Error(`Missing category: ${label}`);
    tab.click();
  }

  beforeEach(() => {
    adopt(ADMIN);
    site.value = { ...siteInfo.value };
  });

  it('draws the card under its category and saves its keys with the page\'s, secrets only when typed', async () => {
    installPlugins([example]);
    await mountSecurity({ 'example.base_url': '/rc', 'example.site': 'arc', 'example.secret_key': '••••••••', 'example.on_login': 'true' });
    openCategory(t('controlVerification'));
    await nextTick();

    const card = host.querySelector<HTMLElement>('#secExample');
    if (!card) throw new Error('the plugin card was not drawn');
    expect(field(card, 'Service address').value).toBe('/rc');
    // The secret is shown as the stored mask, never as its value.
    expect(field(card, 'Site secret').value).toBe('');
    expect(field(card, 'Site secret').placeholder).toBe('••••••••');

    const saved = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    type(field(card, 'Site key'), 'arc-2');
    button(actions, t('save')).click();
    await settle();
    expect(saved).toHaveBeenCalledWith(expect.objectContaining({
      'example.base_url': '/rc', 'example.site': 'arc-2', 'example.secret_key': '', 'example.on_login': 'true',
    }));
  });

  it('draws and sends only the keys the server showed this account', async () => {
    // A plugin can keep where its service lives to a super administrator, and
    // the server leaves those keys out for anybody else. Drawing them empty
    // and sending them back had the whole page's save refused.
    installPlugins([example]);
    await mountSecurity({ 'example.on_login': 'true', 'turnstile.on_login': 'false' });
    openCategory(t('controlVerification'));
    await nextTick();

    const card = host.querySelector<HTMLElement>('#secExample');
    if (!card) throw new Error('the plugin card was not drawn');
    expect(card.textContent).not.toContain('Service address');
    expect(card.textContent).not.toContain('Site secret');

    const saved = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    button(actions, t('save')).click();
    await settle();
    const payload = saved.mock.calls[0]![0] as Record<string, string>;
    expect(payload['example.on_login']).toBe('true');
    expect(payload['turnstile.on_login']).toBe('false');
    for (const hidden of ['example.base_url', 'example.site', 'example.secret_key', 'oauth.oidc_issuer']) {
      expect(payload).not.toHaveProperty(hidden);
    }
  });

  it('offers a plugin\'s sign-up challenge in the page\'s own select', async () => {
    installPlugins([example]);
    await mountSecurity({ 'registration.captcha_mode': 'example' });
    openCategory(t('controlVerification'));
    await nextTick();
    expect(host.querySelector('#secVerificationScenes')!.textContent).toContain('Example check only');
  });

  it('draws and sends nothing of a plugin that is not loaded', async () => {
    await mountSecurity({});
    expect(host.querySelector('#secExample')).toBeNull();
    const saved = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    button(actions, t('save')).click();
    await settle();
    const sent = saved.mock.calls[0]![0];
    expect(Object.keys(sent).some((key) => key.startsWith('example.'))).toBe(false);
  });
});

describe('a plugin\'s list and actions in the backoffice', () => {
  const spec = example.lists![0]!;

  it('pages the plugin\'s endpoint and draws its cells', async () => {
    installPlugins([example]);
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      records: [{ username: 'ada', mode: 'delete' }],
      total: 1,
    });
    await mount(PluginList, { spec });
    expect(get).toHaveBeenCalledWith('/api/admin/example/records?limit=20&offset=0');
    expect(host.textContent).toContain('ada');
    expect(host.textContent).toContain('1–1 of 1');
    expect(host.querySelector('.oa-badge')!.textContent).toContain('Deleted');
  });

  it('asks in place before an account-ending action, then reports what it did', async () => {
    const action = example.userActions![0]!;
    const post = vi.spyOn(api, 'post').mockResolvedValue({ result: { due: 2, taken: 1 } });
    const done = vi.fn();
    const plugin: ArcPlugin = example;
    installPlugins([plugin]);
    await mount({ setup: () => () => h(PluginUserActions, { spec: action, userId: 'u1', username: 'ada', onDone: done }) });

    const disable = button(host, action.buttons[0]!.label());
    disable.click();
    await nextTick();
    // Armed, not run: the first press only asks.
    expect(post).not.toHaveBeenCalled();
    button(host, t('confirmWord')).click();
    await settle();
    expect(post).toHaveBeenCalledWith('/api/admin/users/u1/process', { mode: 'disable', note: '' });
    expect(done).toHaveBeenCalledWith(false);
    expect(host.textContent).toContain('2 due, 1 taken back');
  });
});
