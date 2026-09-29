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
  installPlugins, loadPlugins, pinnedProvider, pluginInviteeNote, pluginOAuthError, pluginRefusal, pluginStrings,
  plugins,
} from '../src/plugins/registry';
import qqgroup from '../src/plugins/qqgroup/qqgroup.plugin';
import riskcontrol from '../src/plugins/riskcontrol/riskcontrol.plugin';
import type { ArcPlugin } from '../src/plugins/types';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import AuthView from '../src/views/AuthView.vue';
import AdminSecurity from '../src/views/admin/AdminSecurity.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import PluginList from '../src/views/admin/PluginList.vue';
import PluginUserActions from '../src/views/admin/PluginUserActions.vue';

// The plugin framework's browser half: which plugins load, how the core
// forms draw what a plugin declares, and that a plugin's settings travel
// with the page's own save.

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
  forget();
  site.value = null;
});

describe('loading plugins', () => {
  it('fetches the plugins the server names, and skips a name this build has no code for', async () => {
    await loadPlugins({ qqgroup: {}, riskcontrol: { on_signup: false }, nosuchplugin: {} });
    expect(plugins().map((plugin) => plugin.name)).toEqual(['qqgroup', 'riskcontrol']);
  });

  it('loads nothing for a server that names nothing', async () => {
    await loadPlugins(undefined);
    expect(plugins()).toEqual([]);
  });
});

describe('what the core asks a plugin', () => {
  it('words a field\'s refusals from its spec, and a plugin\'s own codes from its table', () => {
    installPlugins([qqgroup, riskcontrol]);
    const qq = qqgroup.fields!['qq']!;
    expect(pluginRefusal('qq_taken')).toBe(qq.taken());
    expect(pluginRefusal('invalid_qq')).toBe(qq.invalid());
    expect(pluginRefusal('qq_required')).toBe(qq.required());
    expect(pluginRefusal('risk_blocked')).toBe(riskcontrol.refusals!['risk_blocked']!());
    expect(pluginRefusal('badge_taken')).toBeNull();
    expect(refusalText(new ApiError(409, 'qq_taken', 'That qq is already registered.', {}))).toBe(qq.taken());
    expect(pluginOAuthError('qq_required')).toBe(qqgroup.oauthErrors!['qq_required']!());
    expect(pinnedProvider('oidc')?.hint()).toBe(qqgroup.pinnedProviders!['oidc']!.hint());
    expect(pinnedProvider('github')).toBeNull();
  });

  it('says nothing of a plugin that is not loaded', () => {
    expect(pluginRefusal('qq_taken')).toBeNull();
    expect(pinnedProvider('oidc')).toBeNull();
    // The code a server without the plugin would never send reads as the
    // generic refusal rather than a key.
    expect(refusalText(new ApiError(409, 'qq_taken', 'x', {}))).toBe(t('authRequestFailed'));
  });

  it('lets a plugin word its own notifications and invitee rows', () => {
    installPlugins([qqgroup]);
    const text = describeNotification({
      id: 'n1', kind: 'invite_departed', params: { username: 'ada', cards_due: 2, cards_revoked: 2 },
      link: '', created_at: 1, read: false,
    } as unknown as Parameters<typeof describeNotification>[0]);
    expect(text.body).toContain('ada');
    expect(text.body).toContain('2');
    expect(pluginInviteeNote({ departed: true, departure_mode: 'delete', cards_revoked: 1 }))
      .toContain('account removed');
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
    installPlugins([qqgroup]);
  });

  it('is drawn only where the server asks for it, and marked required when it must be', async () => {
    site.value = { ...siteInfo.value, fields: { qq: 'off' } };
    await mount(AuthView, { mode: 'register' });
    expect(() => field(host, qqgroup.fields!['qq']!.label())).toThrow();
    app!.unmount();
    app = undefined;

    site.value = { ...siteInfo.value, fields: { qq: 'required' } };
    await mount(AuthView, { mode: 'register' });
    expect(field(host, qqgroup.fields!['qq']!.label()).required).toBe(true);
  });

  it('is checked before submitting and sent under "fields"', async () => {
    site.value = { ...siteInfo.value, fields: { qq: 'required' } };
    const register = vi.spyOn(authApi, 'register').mockResolvedValue({ user: ADMIN });
    await mount(AuthView, { mode: 'register' });
    const qq = qqgroup.fields!['qq']!;

    type(field(host, t('username')), 'newperson');
    type(field(host, t('password')), 'a-strong-password');
    button(host, t('createAccount')).click();
    await settle();
    expect(register).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(qq.required());

    type(field(host, qq.label()), '0123');
    button(host, t('createAccount')).click();
    await settle();
    expect(register).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(qq.invalid());

    type(field(host, qq.label()), '10001');
    button(host, t('createAccount')).click();
    await settle();
    expect(register).toHaveBeenCalledWith(expect.objectContaining({ fields: { qq: '10001' } }));
  });

  it('is not drawn for a field the server names but no plugin describes', async () => {
    installPlugins([]);
    site.value = { ...siteInfo.value, fields: { qq: 'required' } };
    await mount(AuthView, { mode: 'register' });
    expect(host.querySelector('input[name="field-qq"]')).toBeNull();
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
    installPlugins([riskcontrol]);
    await mountSecurity({ 'risk.base_url': '/rc', 'risk.site': 'arc', 'risk.secret_key': '••••••••', 'risk.on_login': 'true' });
    openCategory(t('controlVerification'));
    await nextTick();

    const card = host.querySelector<HTMLElement>('#secRisk');
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
      'risk.base_url': '/rc', 'risk.site': 'arc-2', 'risk.secret_key': '', 'risk.on_login': 'true',
    }));
  });

  it('offers a plugin\'s sign-up challenge in the page\'s own select', async () => {
    installPlugins([riskcontrol]);
    await mountSecurity({ 'registration.captcha_mode': 'risk' });
    openCategory(t('controlVerification'));
    await nextTick();
    expect(host.querySelector('#secVerificationScenes')!.textContent).toContain('Super risk control only');
  });

  it('draws and sends nothing of a plugin that is not loaded', async () => {
    await mountSecurity({});
    expect(host.querySelector('#secRisk')).toBeNull();
    const saved = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: {} });
    button(actions, t('save')).click();
    await settle();
    const sent = saved.mock.calls[0]![0];
    expect(Object.keys(sent).some((key) => key.startsWith('risk.') || key.startsWith('bot.'))).toBe(false);
    expect('registration.qq_requirement' in sent).toBe(false);
  });
});

describe('a plugin\'s list and actions in the backoffice', () => {
  const spec = qqgroup.lists![0]!;

  it('pages the plugin\'s endpoint and draws its cells', async () => {
    installPlugins([qqgroup]);
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      departures: [{ username: 'ada', qq: '10001', inviter_name: '', mode: 'delete', reward_cards_due: 1, cards_revoked: 1, source: 'bot', created_at: 0 }],
      total: 1,
    });
    await mount(PluginList, { spec });
    expect(get).toHaveBeenCalledWith('/api/admin/departures?limit=20&offset=0');
    expect(host.textContent).toContain('ada');
    expect(host.textContent).toContain('1 / 1');
    expect(host.querySelector('.oa-badge')!.textContent).toContain('Deleted');
  });

  it('asks in place before an account-ending action, then reports what it did', async () => {
    const action = qqgroup.userActions![0]!;
    const post = vi.spyOn(api, 'post').mockResolvedValue({ departure: { cards_due: 2, cards_revoked: 1 } });
    const done = vi.fn();
    const plugin: ArcPlugin = qqgroup;
    installPlugins([plugin]);
    await mount({ setup: () => () => h(PluginUserActions, { spec: action, userId: 'u1', username: 'ada', onDone: done }) });

    const disable = button(host, action.buttons[0]!.label());
    disable.click();
    await nextTick();
    // Armed, not run: the first press only asks.
    expect(post).not.toHaveBeenCalled();
    button(host, t('confirmWord')).click();
    await settle();
    expect(post).toHaveBeenCalledWith('/api/admin/users/u1/departure', { mode: 'disable', note: '' });
    expect(done).toHaveBeenCalledWith(false);
    expect(host.textContent).toContain('2 card(s) due, 1 taken back');
  });
});
