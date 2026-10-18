import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type AdminMailSettings, type AdminUserCheckSettings } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { settingsRefusalText } from '../src/lib/refusal';
import { adopt, forget } from '../src/stores/session';
import AdminSecurity from '../src/views/admin/AdminSecurity.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle, button } from './helpers';
import { makeAccount } from './fixtures/account';

const ADMIN = makeAccount({
  id: 'u1', username: 'root', email: 'root@example.com', nickname: 'Root', role: 'super_admin', last_login_at: 1,
});

const MAIL: AdminMailSettings = {
  host: 'smtp.example.com', port: 465, username: 'mailer', from: 'arc@example.com',
  implicit_tls: true, public_url: 'https://arc.example.com', password_set: true,
};

const USER_CHECK: AdminUserCheckSettings = {
  enabled: false, exempt_domains: ['gmail.com', 'outlook.com'], failure_mode: 'reject', api_key_set: true,
};

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;


function type(input: HTMLInputElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function fieldInput(root: ParentNode, label: string): HTMLInputElement {
  const field = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === label);
  const input = field?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error(`Missing field: ${label}`);
  return input;
}

async function mount(): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/');
  await router.isReady();
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminSecurity);
  } });
  app.use(router);
  app.mount(host);
  await settle();
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
  vi.restoreAllMocks();
  forget();
});

describe('a refused OpenID Connect address', () => {
  it('names the field it was typed into, in either language', async () => {
    const failure = new ApiError(400, 'oidc_url_not_https', 'Setting "oauth.oidc_token_url" must be an https address.', {
      setting: 'oauth.oidc_token_url',
    });
    await changeLanguage('en');
    expect(settingsRefusalText(failure)).toBe(t('oidcURLNotHTTPS', { field: t('oauthOIDCTokenURL') }));
    expect(settingsRefusalText(failure)).toContain('Token endpoint');

    await changeLanguage('zh');
    expect(settingsRefusalText(failure)).toContain('令牌端点');
    expect(settingsRefusalText(failure)).toContain('https');
    await changeLanguage('en');
  });

  it('is worded as any other refusal when it names no field the form has', async () => {
    await changeLanguage('en');
    expect(settingsRefusalText(new ApiError(400, 'invite_invalid', 'server text'))).toBe(t('inviteInvalid'));
    expect(settingsRefusalText(new ApiError(400, 'oidc_url_not_https', 'server text', { setting: 'oauth.unknown' })))
      .toBe(t('authRequestFailed'));
  });

  it('is what the security screen says when the save is refused', async () => {
    adopt(ADMIN);
    vi.spyOn(adminApi, 'settings').mockResolvedValue({
      settings: { 'oauth.oidc_token_url': 'http://idp.example.com/token' },
      groups: [],
      mail_configured: true,
    });
    vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
    vi.spyOn(adminApi, 'securityEvents').mockResolvedValue({ events: [], total: 0, limit: 20, offset: 0 });
    vi.spyOn(adminApi, 'applications').mockResolvedValue({ applications: [], issuer: '', scopes: [] });
    vi.spyOn(adminApi, 'twoFactorAdoption').mockResolvedValue({
      policy: 'optional', accounts: 0, enabled: 0, admins: 0, admins_enabled: 0,
      admins_without: [], remember_days: 0, issuer_fallback: 'Arc', available: true,
    });
    vi.spyOn(adminApi, 'mail').mockResolvedValue(MAIL);
    vi.spyOn(adminApi, 'userCheck').mockResolvedValue(USER_CHECK);
    const save = vi.spyOn(adminApi, 'saveSettings').mockRejectedValue(
      new ApiError(400, 'oidc_url_not_https', 'Setting "oauth.oidc_token_url" must be an https address.', {
        setting: 'oauth.oidc_token_url',
      }),
    );
    await mount();

    const category = [...host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab')]
      .find((node) => node.querySelector('strong')?.textContent === t('controlSignIn'));
    if (!category) throw new Error('Missing sign-in category');
    category.click();
    await nextTick();

    const card = host.querySelector<HTMLElement>('#secOAuth');
    if (!card) throw new Error('Missing OpenID Connect card');
    type(fieldInput(card, t('oauthOIDCTokenURL')), 'http://idp.example.com/other-token');
    button(actions, t('save')).click();
    await settle();

    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0]?.[0]).toMatchObject({ 'oauth.oidc_token_url': 'http://idp.example.com/other-token' });
    const lines = [...host.querySelectorAll('[role="status"]')].map((node) => node.textContent?.trim());
    expect(lines).toContain(t('oidcURLNotHTTPS', { field: t('oauthOIDCTokenURL') }));
  });
});
