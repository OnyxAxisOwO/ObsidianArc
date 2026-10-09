import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import * as authApi from '../src/api/auth';
import type { Account } from '../src/api/auth';
import * as consentApi from '../src/api/consent';
import * as oauthApi from '../src/api/oauth';
import { signInURL } from '../src/api/oauth';
import * as backupApi from '../src/api/backup';
import { ApiError } from '../src/api/client';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { safeNext } from '../src/lib/next';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import AuthView from '../src/views/AuthView.vue';
import CompleteSignupView from '../src/views/CompleteSignupView.vue';
import ConsentView from '../src/views/ConsentView.vue';
import TwoFactorEnrolView from '../src/views/TwoFactorEnrolView.vue';
import VerifyView from '../src/views/VerifyView.vue';
import AccountSection from '../src/views/settings/AccountSection.vue';
import { installPlugins } from '../src/plugins/registry';
import example, { type ExampleCheck } from './fixtures/example.plugin';

// A plugin's account field, as the forms label and refuse it.
const refField = example.fields!['ref']!;

// Signing in with an account from elsewhere, and letting somebody else's site
// sign people in with an account here. Two features pointing opposite ways,
// tested together because the screens they touch are the same three.

const route = { path: '/login', query: {} as Record<string, string> };
const replace = vi.fn();
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace }),
  useRoute: () => route,
}));

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  route.path = '/login';
  route.query = {};
  replace.mockReset();
  host = document.createElement('div');
  panels = document.createElement('div');
  document.body.append(host, panels);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  site.value = null;
  installPlugins([]);
  forget();
});

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

function type(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      return () => h(component, props);
    },
  });
  app.mount(host);
  await settle();
}

function offer(providers: { id: string; name: string }[]): void {
  site.value = { ...siteInfo.value, oauth: providers };
}

function fieldInput(label: string): HTMLInputElement {
  const field = [...host.querySelectorAll('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === label);
  if (!field) throw new Error(`Field not found: ${label}`);
  return field.querySelector('input')!;
}

const NEW_ACCOUNT: Account = {
  id: 'u2', username: 'newperson', email: '', nickname: '', avatar: '', bio: '',
  role: 'user', group_id: '', group_expires_at: 0, group_name: '',
  status: 'active', created_at: 0, updated_at: 0, last_login_at: 0,
  email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

describe('password sign-in errors', () => {
  it('shows a localized message for an incorrect password', async () => {
    await changeLanguage('zh');
    vi.spyOn(authApi, 'login').mockRejectedValue(new ApiError(
      401,
      'unauthorized',
      'Incorrect username or password.',
      { code_detail: 'invalid_credentials' },
    ));
    await mount(AuthView, { mode: 'login' });

    type(fieldInput(t('usernameOrEmail')), 'visitor');
    type(fieldInput(t('password')), 'wrong-password');
    button(host, t('signIn')).click();
    await settle();

    expect(host.querySelector('.oa-auth-error')?.textContent).toBe(t('invalidCredentials'));
  });

  it('localizes network failures instead of showing browser error text', async () => {
    await changeLanguage('zh');
    vi.spyOn(authApi, 'login').mockRejectedValue(new ApiError(
      0,
      'network',
      'Could not reach the server: Failed to fetch',
    ));
    await mount(AuthView, { mode: 'login' });

    type(fieldInput(t('usernameOrEmail')), 'visitor');
    type(fieldInput(t('password')), 'secret');
    button(host, t('signIn')).click();
    await settle();

    expect(host.querySelector('.oa-auth-error')?.textContent).toBe(t('connectionFailed'));
  });
});

describe('signing in with an account from elsewhere', () => {
  it('draws a button for each provider the operator switched on', async () => {
    offer([{ id: 'github', name: 'GitHub' }, { id: 'google', name: 'Google' }]);
    await mount(AuthView, { mode: 'login' });

    const links = [...host.querySelectorAll<HTMLAnchorElement>('.oa-auth-provider')];
    expect(links).toHaveLength(2);
    expect(links[0]!.textContent).toContain(t('continueWith', { provider: 'GitHub' }));
    // A link, not a button: the server answers with a redirect to somebody
    // else's site, which a fetch could not follow anywhere useful.
    expect(links[0]!.getAttribute('href')).toBe('/api/auth/oauth/start/github');
    expect(links[1]!.getAttribute('href')).toBe('/api/auth/oauth/start/google');
    // Each one carries its own mark.
    expect(links[0]!.querySelector('svg')).not.toBeNull();
  });

  it('draws nothing at all when none is configured', async () => {
    offer([]);
    await mount(AuthView, { mode: 'login' });
    expect(host.querySelector('.oa-auth-provider')).toBeNull();
    expect(host.querySelector('.oa-auth-or')).toBeNull();
  });

  // The callback is a redirect, so a refusal has nowhere to put a response
  // body: it arrives as a code in the query and is worded here.
  it('says why a provider sign-in came back empty-handed', async () => {
    offer([{ id: 'github', name: 'GitHub' }]);
    route.query = { oauth_error: 'address_taken' };
    await mount(AuthView, { mode: 'login' });

    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('oauthAddressTaken'));
    // And out of the address bar, so a reload does not raise it again.
    expect(replace).toHaveBeenCalledWith({ path: '/login', query: {} });
  });

  it('explains when third-party signup was refused because oidc is required', async () => {
    offer([{ id: 'github', name: 'GitHub' }, { id: 'oidc', name: 'OpenID Connect' }]);
    route.query = { oauth_error: 'oidc_only' };
    await mount(AuthView, { mode: 'login' });

    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('oauthOIDCOnly'));
    expect(replace).toHaveBeenCalledWith({ path: '/login', query: {} });
  });

  it('renders dedicated OIDC signup button when oidc_only_signup is active on register page', async () => {
    site.value = {
      ...siteInfo.value,
      registration_enabled: true,
      oidc_only_signup: true,
      oauth: [{ id: 'oidc', name: 'Company SSO' }],
    };
    route.path = '/register';
    await mount(AuthView, { mode: 'register' });

    expect(host.querySelector('.oa-auth-sub')!.textContent).toBe(t('oidcOnlySignupNotice'));
    expect(host.querySelector('input[type="password"]')).toBeNull();
    const oidcButton = host.querySelector<HTMLAnchorElement>('.oa-auth-provider')!;
    expect(oidcButton).not.toBeNull();
    expect(oidcButton.getAttribute('href')).toBe('/api/auth/oauth/start/oidc?register=1');
    expect(oidcButton.textContent).toContain('Company SSO');
  });

  it('renders all configured OAuth providers on register page when oauth_only_signup is active', async () => {
    site.value = {
      ...siteInfo.value,
      registration_enabled: true,
      oauth_only_signup: true,
      oauth: [
        { id: 'github', name: 'GitHub' },
        { id: 'google', name: 'Google' },
        { id: 'oidc', name: 'Company SSO' },
      ],
    };
    route.path = '/register';
    await mount(AuthView, { mode: 'register' });

    expect(host.querySelector('.oa-auth-sub')!.textContent).toBe(t('oauthThirdPartyOnlyNotice'));
    expect(host.querySelector('input[type="password"]')).toBeNull();
    const buttons = host.querySelectorAll<HTMLAnchorElement>('.oa-auth-provider');
    expect(buttons).toHaveLength(3);
    expect(buttons[0]!.getAttribute('href')).toBe('/api/auth/oauth/start/github?register=1');
    expect(buttons[1]!.getAttribute('href')).toBe('/api/auth/oauth/start/google?register=1');
    expect(buttons[2]!.getAttribute('href')).toBe('/api/auth/oauth/start/oidc?register=1');
  });

  it('requires captcha verification before clicking provider button when guarded', async () => {
    site.value = {
      ...siteInfo.value,
      turnstile_on_login: true,
      turnstile_site_key: '0x4AAAAAAABBBBBBB',
      oauth: [{ id: 'github', name: 'GitHub' }],
    };
    route.path = '/login';
    await mount(AuthView, { mode: 'login' });

    const assign = vi.fn();
    Object.defineProperty(window, 'location', {
      configurable: true, writable: true, value: { assign, href: '' },
    });

    const link = host.querySelector<HTMLAnchorElement>('.oa-auth-provider')!;
    link.click();
    await settle();

    expect(assign).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('challengeRequired'));
  });

  it('falls back to a general sentence for a code it does not know', async () => {
    route.query = { oauth_error: 'something-new' };
    await mount(AuthView, { mode: 'login' });
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('oauthFailed'));
  });

  // Somebody sent to sign in from the consent screen has to come back to it,
  // because what is waiting is not a page but another site's request.
  it('carries where it was going through the provider buttons', async () => {
    offer([{ id: 'github', name: 'GitHub' }]);
    route.query = { next: '/oauth/consent?request=abc' };
    await mount(AuthView, { mode: 'login' });

    const link = host.querySelector<HTMLAnchorElement>('.oa-auth-provider')!;
    expect(link.getAttribute('href')).toBe(
      '/api/auth/oauth/start/github?next=%2Foauth%2Fconsent%3Frequest%3Dabc',
    );
  });

  it('keeps a destination on this site and drops one that is not', () => {
    for (const safe of ['/', '/settings', '/oauth/consent?request=abc']) {
      expect(safeNext(safe)).toBe(safe);
    }
    for (const unsafe of ['//evil.example', 'https://evil.example', '/\\evil.example', '', 7, null]) {
      expect(safeNext(unsafe)).toBe('');
    }
  });

  it('builds the start URL without empty parameters in it', () => {
    expect(signInURL('github')).toBe('/api/auth/oauth/start/github');
    expect(signInURL('github', { next: '' })).toBe('/api/auth/oauth/start/github');
    expect(signInURL('github', { link: true })).toBe('/api/auth/oauth/start/github?link=1');
  });
});

// registration.enabled + invites.required, as the sign-up form reads them
// back through the derived `invite_mode` the server sends on /api/site.
describe('registering with an invite code', () => {
  beforeEach(() => {
    route.path = '/register';
  });

  it('keeps the field collapsed behind a link in open mode, until asked for', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'open' };
    await mount(AuthView, { mode: 'register' });

    expect(() => fieldInput(t('inviteCodeOptionalLabel'))).toThrow();
    button(host, t('haveInviteCode')).click();
    await settle();
    expect(fieldInput(t('inviteCodeOptionalLabel'))).not.toBeNull();
  });

  it('shows the field open and required outright in invite-only mode, with no toggle', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'invite' };
    await mount(AuthView, { mode: 'register' });

    expect(fieldInput(t('inviteCodeLabel'))).not.toBeNull();
    expect(() => button(host, t('haveInviteCode'))).toThrow();
  });

  it('refuses to submit without one in invite-only mode, before asking the server', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'invite' };
    const register = vi.spyOn(authApi, 'register');
    await mount(AuthView, { mode: 'register' });

    type(fieldInput(t('username')), 'newperson');
    type(fieldInput(t('password')), 'a-strong-password');
    button(host, t('createAccount')).click();
    await settle();

    expect(register).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('inviteRequiredHere'));
  });

  it('prefills the field from a ?invite= link and opens it, even in open mode', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'open' };
    route.query = { invite: 'PARTNERX' };
    await mount(AuthView, { mode: 'register' });

    expect(fieldInput(t('inviteCodeOptionalLabel')).value).toBe('PARTNERX');
  });

  it('sends the typed code to the server, trimmed', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'invite' };
    const register = vi.spyOn(authApi, 'register').mockResolvedValue({ user: NEW_ACCOUNT });
    await mount(AuthView, { mode: 'register' });

    type(fieldInput(t('username')), 'newperson');
    type(fieldInput(t('password')), 'a-strong-password');
    type(fieldInput(t('inviteCodeLabel')), '  abcd-2345  ');
    button(host, t('createAccount')).click();
    await settle();

    expect(register).toHaveBeenCalledWith(expect.objectContaining({ inviteCode: 'abcd-2345' }));
  });

  it('words the server\'s refusal of an unknown or spent code', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'invite' };
    const { ApiError } = await import('../src/api/client');
    vi.spyOn(authApi, 'register').mockRejectedValue(new ApiError(400, 'invite_invalid', 'nope', {}));
    await mount(AuthView, { mode: 'register' });

    type(fieldInput(t('username')), 'newperson');
    type(fieldInput(t('password')), 'a-strong-password');
    type(fieldInput(t('inviteCodeLabel')), 'DEADCODE');
    button(host, t('createAccount')).click();
    await settle();

    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('inviteInvalid'));
  });
});

// A plugin's guard stands in front of the same form. The check is fake here —
// a real one would be somebody else's script on the page — so what is under
// test is the wiring: init on the way in, a token on the way out, and a
// refusal that never reaches the server.
describe('a plugin\'s guard on the sign-up and sign-in forms', () => {
  let init: ReturnType<typeof vi.fn>;
  let execute: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    route.path = '/register';
    init = vi.fn();
    execute = vi.fn(async () => 'guard-token-1');
    window.ExampleCheck = { init, execute } as unknown as ExampleCheck;
  });

  afterEach(() => {
    delete window.ExampleCheck;
  });

  // An instance running the plugin, as /api/site would describe it.
  function guardedSite(): void {
    const block = { site: 'arc-test', on_signup: true, on_login: true };
    site.value = { ...siteInfo.value, plugins: { example: block } };
    installPlugins([example], { example: block });
  }

  it('inits the check when the card opens and submits the token it mints', async () => {
    guardedSite();
    const register = vi.spyOn(authApi, 'register').mockResolvedValue({ user: NEW_ACCOUNT });
    await mount(AuthView, { mode: 'register' });

    type(fieldInput(t('username')), 'newperson');
    type(fieldInput(t('password')), 'a-strong-password');
    button(host, t('createAccount')).click();
    await settle();

    expect(init).toHaveBeenCalledWith({ site: 'arc-test' });
    expect(execute).toHaveBeenCalledWith('register', expect.any(HTMLFormElement));
    expect(register).toHaveBeenCalledWith(expect.objectContaining({ guards: { example: 'guard-token-1' } }));
  });

  it('ends the attempt with its own words when the check refuses this browser', async () => {
    guardedSite();
    execute = vi.fn(async () => { throw new Error('rejected'); });
    window.ExampleCheck = { init, execute } as unknown as ExampleCheck;
    const register = vi.spyOn(authApi, 'register');
    await mount(AuthView, { mode: 'register' });

    type(fieldInput(t('username')), 'newperson');
    type(fieldInput(t('password')), 'a-strong-password');
    button(host, t('createAccount')).click();
    await settle();

    expect(register).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(example.guards![0]!.failed());
  });

  it('asks for nothing on an instance whose plugin block says the door is open', async () => {
    const block = { site: '', on_signup: false, on_login: false };
    site.value = { ...siteInfo.value, plugins: { example: block } };
    installPlugins([example], { example: block });
    const register = vi.spyOn(authApi, 'register').mockResolvedValue({ user: NEW_ACCOUNT });
    await mount(AuthView, { mode: 'register' });

    type(fieldInput(t('username')), 'newperson');
    type(fieldInput(t('password')), 'a-strong-password');
    button(host, t('createAccount')).click();
    await settle();

    expect(init).not.toHaveBeenCalled();
    expect(execute).not.toHaveBeenCalled();
    expect(register).toHaveBeenCalledWith(expect.objectContaining({ guards: {} }));
  });

  it('stands in front of sign-in too, and names the action it is checking', async () => {
    guardedSite();
    route.path = '/login';
    const login = vi.spyOn(authApi, 'login').mockResolvedValue({ user: NEW_ACCOUNT });
    await mount(AuthView, { mode: 'login' });

    type(fieldInput(t('usernameOrEmail')), 'somebody');
    type(fieldInput(t('password')), 'a-strong-password');
    button(host, t('signIn')).click();
    await settle();

    expect(execute).toHaveBeenCalledWith('login', expect.any(HTMLFormElement));
    expect(login).toHaveBeenCalledWith('somebody', 'a-strong-password', '', { example: 'guard-token-1' });
  });
});

// The same rule applies to the other door an account gets created through:
// a provider sign-in the server has no way to finish without asking first.
describe('finishing a sign-up with an invite code', () => {
  const pending = {
    provider: 'github', provider_name: 'GitHub', login: 'octocat', email: '',
    needs: { fields: [] as string[], email: false }, email_domains: [] as string[], verify_email: false,
  };

  beforeEach(() => {
    route.path = '/oauth/complete';
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
  });

  it('requires a code in invite-only mode, before asking the server', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'invite' };
    const complete = vi.spyOn(oauthApi, 'completeSignup');
    await mount(CompleteSignupView);

    button(host, t('signupCompleteSubmit')).click();
    await settle();

    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('inviteRequiredHere'));
  });

  it('sends the code once one is entered', async () => {
    site.value = { ...siteInfo.value, invite_mode: 'invite' };
    const complete = vi.spyOn(oauthApi, 'completeSignup').mockResolvedValue({ redirect: '/' });
    await mount(CompleteSignupView);

    type(fieldInput(t('inviteCodeLabel')), 'PARTNERX');
    button(host, t('signupCompleteSubmit')).click();
    await settle();

    expect(complete).toHaveBeenCalledWith({ username: 'octocat', fields: {}, email: '', inviteCode: 'PARTNERX' });
  });
});

describe('the connections an account holds', () => {
  const account = {
    id: 'u1', username: 'reader', email: '', nickname: '', avatar: '', bio: '',
    role: 'user' as const, group_id: '', group_expires_at: 0, group_name: '',
    status: 'active' as const, created_at: 0, updated_at: 0, last_login_at: 0,
    email_verified: true, allow_stats: true, allow_delete_conversations: true,
    api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  };

  beforeEach(() => {
    adopt(account);
    route.path = '/settings';
    vi.spyOn(consentApi, 'fetchAuthorizations').mockResolvedValue({ authorizations: [] });
    vi.spyOn(backupApi, 'exportAccount').mockResolvedValue({} as never);
  });

  it('offers a connect link for a provider and a way out of one it holds', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [{
        provider: 'github', login: 'octocat', email: 'cat@example.com',
        created_at: Date.now(), last_login_at: Date.now(),
      }],
      providers: [
        { id: 'github', name: 'GitHub', enabled: true },
        { id: 'google', name: 'Google', enabled: true },
      ],
      has_password: true,
    });
    await mount(AccountSection);

    const rows = [...host.querySelectorAll('.oa-connection')];
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain('octocat');
    // The one that is connected has no link to connect it again.
    expect(rows[0]!.querySelector('a')).toBeNull();
    expect(rows[1]!.querySelector('a')!.getAttribute('href'))
      .toBe('/api/auth/oauth/start/google?link=1&next=%2Fsettings');
  });

  // A provider the operator has since switched off is still a way into this
  // account, so it stays listed: hiding it would hide the only control that
  // can remove it.
  it('keeps listing a provider that is connected but no longer offered', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [{
        provider: 'github', login: 'octocat', email: '', created_at: 0, last_login_at: 0,
      }],
      providers: [
        { id: 'github', name: 'GitHub', enabled: false },
        { id: 'google', name: 'Google', enabled: false },
      ],
      has_password: true,
    });
    await mount(AccountSection);

    const rows = [...host.querySelectorAll('.oa-connection')];
    expect(rows).toHaveLength(1);
    expect(rows[0]!.textContent).toContain('GitHub');
  });

  it('says a connection cannot be the last way in', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [{
        provider: 'github', login: 'octocat', email: '', created_at: 0, last_login_at: 0,
      }],
      providers: [{ id: 'github', name: 'GitHub', enabled: true }],
      has_password: false,
    });
    const { ApiError } = await import('../src/api/client');
    vi.spyOn(oauthApi, 'disconnectProvider').mockRejectedValue(
      new ApiError(409, 'last_way_in', 'nope', {}),
    );
    await mount(AccountSection);

    const remove = host.querySelector<HTMLButtonElement>('.oa-connection button')!;
    remove.click();
    await settle();
    remove.click();
    await settle();

    expect(host.textContent).toContain(t('oauthLastWayIn'));
  });

  // An account opened through a provider has no password. The box has to say
  // "set" rather than "change", and must not ask for one that never existed.
  it('offers to set a first password when there is none', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [], providers: [], has_password: false,
    });
    await mount(AccountSection);

    expect(host.textContent).toContain(t('setPassword'));
    expect(host.textContent).not.toContain(t('currentPassword'));
  });

  it('asks for the current password when there is one', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [], providers: [], has_password: true,
    });
    await mount(AccountSection);

    expect(host.textContent).toContain(t('currentPassword'));
    expect(host.textContent).not.toContain(t('setPasswordHint'));
  });

  // The address is where a reset link goes, so moving it takes the password —
  // but a nickname edit sends the unchanged address too and must not.
  it('asks for the password only while the email is being changed', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [], providers: [], has_password: true,
    });
    const save = vi.spyOn(authApi, 'updateProfile')
      .mockResolvedValue({ user: { ...account, email: 'new@example.com' } });
    await mount(AccountSection);
    expect(host.textContent).not.toContain(t('emailChangePasswordHint'));

    type(fieldInput(t('email')), 'new@example.com');
    await settle();
    expect(host.textContent).toContain(t('emailChangePasswordHint'));

    button(host, t('save')).click();
    await settle();
    expect(save).not.toHaveBeenCalled();

    type(fieldInput(t('currentPassword')), 'a-good-password');
    await settle();
    button(host, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      email: 'new@example.com', current_password: 'a-good-password',
    }));
  });

  it('does not ask an account with no password for one to change its email', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [], providers: [], has_password: false,
    });
    await mount(AccountSection);

    type(fieldInput(t('email')), 'new@example.com');
    await settle();
    expect(host.textContent).not.toContain(t('emailChangePasswordHint'));
  });

  it('lists the sites this account has signed into, and removes one', async () => {
    vi.spyOn(oauthApi, 'fetchConnections').mockResolvedValue({
      connections: [], providers: [], has_password: true,
    });
    vi.mocked(consentApi.fetchAuthorizations).mockResolvedValue({
      authorizations: [{
        app_id: 'a1', client_id: 'c1', name: 'The Wiki',
        scopes: ['openid', 'profile'], created_at: Date.now(), last_used_at: Date.now(),
      }],
    });
    const withdraw = vi.spyOn(consentApi, 'withdrawAuthorization').mockResolvedValue();
    await mount(AccountSection);

    expect(host.textContent).toContain('The Wiki');
    const button = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((node) => node.textContent?.trim() === t('withdraw'))!;
    button.click();
    await settle();
    button.click();
    await settle();
    expect(withdraw).toHaveBeenCalledWith('a1');
  });
});

describe('letting another site sign somebody in', () => {
  beforeEach(() => {
    route.path = '/oauth/consent';
    // jsdom refuses a real navigation, and the whole answer of this screen is
    // where it sends the browser.
    Object.defineProperty(window, 'location', {
      configurable: true, writable: true, value: { href: '' },
    });
  });

  const request = {
    application: { name: 'The Wiki', description: 'A wiki for the team.', client_id: 'c1' },
    scopes: ['openid', 'profile', 'email'],
    redirect_uri: 'https://wiki.example.com/oidc/callback',
  };

  it('says who is asking, what they would learn, and where you are going', async () => {
    route.query = { request: 'a-signed-ticket' };
    const read = vi.spyOn(consentApi, 'fetchConsent').mockResolvedValue(request);
    await mount(ConsentView);

    expect(read).toHaveBeenCalledWith('a-signed-ticket');
    expect(host.textContent).toContain(t('consentTitle', { application: 'The Wiki' }));
    // The operator's own description of the application, and whose account is
    // about to be handed over. Both are on screen, not one or the other.
    expect(host.textContent).toContain('A wiki for the team.');

    const lines = [...host.querySelectorAll('.oa-consent-scopes li')].map((n) => n.textContent);
    expect(lines).toHaveLength(3);
    expect(lines[0]).toContain(t('scopeOpenID'));
    expect(lines[2]).toContain(t('scopeEmail'));

    // The host, not the whole callback: it is the part worth reading and the
    // part an impostor cannot fake.
    expect(host.textContent).toContain(t('consentDestination', { host: 'wiki.example.com' }));
  });

  it('sends the browser to the callback when it is allowed', async () => {
    route.query = { request: 'a-signed-ticket' };
    vi.spyOn(consentApi, 'fetchConsent').mockResolvedValue(request);
    const decide = vi.spyOn(consentApi, 'decideConsent').mockResolvedValue({
      redirect: 'https://wiki.example.com/oidc/callback?code=abc&state=s',
    });
    await mount(ConsentView);

    const allow = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((node) => node.textContent?.trim() === t('consentAllow'))!;
    allow.click();
    await settle();

    expect(decide).toHaveBeenCalledWith('a-signed-ticket', true);
    expect(window.location.href).toBe('https://wiki.example.com/oidc/callback?code=abc&state=s');
  });

  // A refusal is something the application has to be told about: it is
  // waiting at its own callback either way.
  it('tells the application when the answer is no', async () => {
    route.query = { request: 'a-signed-ticket' };
    vi.spyOn(consentApi, 'fetchConsent').mockResolvedValue(request);
    const decide = vi.spyOn(consentApi, 'decideConsent').mockResolvedValue({
      redirect: 'https://wiki.example.com/oidc/callback?error=access_denied&state=s',
    });
    await mount(ConsentView);

    const refuse = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((node) => node.textContent?.trim() === t('consentRefuse'))!;
    refuse.click();
    await settle();

    expect(decide).toHaveBeenCalledWith('a-signed-ticket', false);
    expect(window.location.href).toContain('error=access_denied');
  });

  // The server refuses to redirect anywhere an application did not register,
  // so this screen is where that refusal is read.
  it('explains a request that could not be started at all', async () => {
    route.query = { error: 'bad_redirect' };
    const read = vi.spyOn(consentApi, 'fetchConsent');
    await mount(ConsentView);

    expect(read).not.toHaveBeenCalled();
    expect(host.textContent).toContain(t('consentProblemTitle'));
    expect(host.textContent).toContain(t('consentBadRedirect'));
    // Nothing to agree to, so no button to agree with.
    expect(host.querySelector('.oa-consent-scopes')).toBeNull();
  });

  it('refuses to draw anything without a ticket', async () => {
    route.query = {};
    await mount(ConsentView);
    expect(host.textContent).toContain(t('consentFailed'));
  });
});

// The step a provider sign-in stops at when this server wants something the
// provider had no way to supply. Nothing exists on the server while this is on
// screen, which is what makes walking away from it free.
describe('finishing a sign-up the provider could not', () => {
  const pending = {
    provider: 'github',
    provider_name: 'GitHub',
    login: 'octocat',
    email: '',
    needs: { fields: ['ref'], email: false },
    email_domains: [] as string[],
    verify_email: false,
  };

  // What is missing here is a plugin's account field: an instance that
  // requires one, which no provider has to give.
  beforeEach(() => {
    installPlugins([example]);
    route.path = '/oauth/complete';
    Object.defineProperty(window, 'location', {
      configurable: true, writable: true, value: { href: '' },
    });
  });

  it('asks only for what is missing, and says whose sign-in it is', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
    await mount(CompleteSignupView);

    expect(host.textContent).toContain(t('signupCompleteBody', { provider: 'GitHub' }));
    // Whose it is: without this the page is a stranger asking for details.
    expect(host.querySelector('.oa-signup-who')!.textContent).toContain('octocat');

    const labels = [...host.querySelectorAll('.oa-field-label')].map((node) => node.textContent);
    expect(labels).toContain(refField.label());
    expect(labels).not.toContain(t('email'));
  });

  it('asks for an address instead when that is the missing one', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue({
      ...pending,
      needs: { fields: [], email: true },
      email_domains: ['company.com'],
    });
    await mount(CompleteSignupView);

    const labels = [...host.querySelectorAll('.oa-field-label')].map((node) => node.textContent);
    expect(labels).toContain(t('email'));
    expect(labels).not.toContain(refField.label());
    // And which addresses would be accepted, before it is typed rather than
    // after it is refused.
    expect(host.textContent).toContain(t('emailAccepted', { domains: 'company.com' }));
  });

  it('opens the account and leaves for wherever the server says', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
    const complete = vi.spyOn(oauthApi, 'completeSignup')
      .mockResolvedValue({ redirect: '/oauth/consent?request=abc' });
    await mount(CompleteSignupView);

    type(fieldInput(refField.label()), '87654321');
    button(host, t('signupCompleteSubmit')).click();
    await settle();

    expect(complete).toHaveBeenCalledWith({ username: 'octocat', fields: { ref: '87654321' }, email: '', inviteCode: '' });
    // A whole navigation, not a route change: the session cookie has just been
    // set and the application reads the account once, at boot.
    expect(window.location.href).toBe('/oauth/consent?request=abc');
  });

  it('refuses to send a number that is not one, without asking the server', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
    const complete = vi.spyOn(oauthApi, 'completeSignup');
    await mount(CompleteSignupView);

    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(refField.required());

    type(fieldInput(refField.label()), 'nonsense');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(refField.invalid());
  });

  it('words the server\'s refusal in the reader\'s own language', async () => {
    const { ApiError } = await import('../src/api/client');
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
    vi.spyOn(oauthApi, 'completeSignup')
      .mockRejectedValue(new ApiError(409, 'ref_taken', 'That reference is already registered.', {}));
    await mount(CompleteSignupView);

    type(fieldInput(refField.label()), '87654321');
    button(host, t('signupCompleteSubmit')).click();
    await settle();

    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(refField.taken());
    // Still on the form, with what was typed still in it.
    expect(fieldInput(refField.label()).value).toBe('87654321');
  });

  it('allows customizing username and validates username format', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
    const complete = vi.spyOn(oauthApi, 'completeSignup')
      .mockResolvedValue({ redirect: '/' });
    await mount(CompleteSignupView);

    // Initial prefill from suggested/login
    expect(fieldInput(t('username')).value).toBe('octocat');

    // Clear username and submit -> requires username
    type(fieldInput(t('username')), '');
    type(fieldInput(refField.label()), '87654321');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('usernameRequired'));

    // Invalid username format -> rejected
    type(fieldInput(t('username')), 'bad@username!');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('usernameInvalid'));

    // Valid custom username -> sent to server
    type(fieldInput(t('username')), 'my_custom_name');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).toHaveBeenCalledWith({
      username: 'my_custom_name',
      fields: { ref: '87654321' },
      email: '',
      inviteCode: '',
    });
  });

  it('allows entering a password and validates password rules', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue(pending);
    const complete = vi.spyOn(oauthApi, 'completeSignup')
      .mockResolvedValue({ redirect: '/' });
    await mount(CompleteSignupView);

    const pwdInput = fieldInput(t('passwordOptional'));
    expect(pwdInput).not.toBeNull();

    // Short password -> rejected
    type(fieldInput(refField.label()), '87654321');
    type(pwdInput, '123');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('passwordTooShort'));

    // Valid password -> included in payload
    type(pwdInput, 'securepassword123');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).toHaveBeenCalledWith({
      username: 'octocat',
      password: 'securepassword123',
      fields: { ref: '87654321' },
      email: '',
      inviteCode: '',
    });
  });

  it('enforces password input when password_required is true', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue({
      ...pending,
      password_required: true,
    });
    const complete = vi.spyOn(oauthApi, 'completeSignup')
      .mockResolvedValue({ redirect: '/' });
    await mount(CompleteSignupView);

    const pwdInput = fieldInput(t('password'));
    expect(pwdInput).not.toBeNull();

    type(fieldInput(refField.label()), '87654321');
    button(host, t('signupCompleteSubmit')).click();
    await settle();
    expect(complete).not.toHaveBeenCalled();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('passwordRequired'));
  });

  it('hides password field when needs_password is false', async () => {
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue({
      ...pending,
      needs_password: false,
    });
    await mount(CompleteSignupView);

    const pwdField = [...host.querySelectorAll('.oa-field')]
      .find((node) => node.querySelector('.oa-field-label')?.textContent === t('password')
        || node.querySelector('.oa-field-label')?.textContent === t('passwordOptional'));
    expect(pwdField).toBeUndefined();
  });

  it('says so when the sign-in is no longer in progress', async () => {
    const { ApiError } = await import('../src/api/client');
    vi.spyOn(oauthApi, 'fetchPendingSignup')
      .mockRejectedValue(new ApiError(400, 'bad_request', 'gone', {}));
    await mount(CompleteSignupView);

    expect(host.textContent).toContain(t('signupCompleteGoneTitle'));
    expect(host.querySelector('input')).toBeNull();
  });
});

describe('auth card layout position', () => {
  it.each([
    { position: undefined, expectedClass: 'position-center' },
    { position: 'center', expectedClass: 'position-center' },
    { position: 'left', expectedClass: 'position-left' },
    { position: 'right', expectedClass: 'position-right' },
  ] as const)('applies $expectedClass to AuthView (login) when auth_card_position is $position', async ({ position, expectedClass }) => {
    site.value = { ...siteInfo.value };
    if (position === undefined) delete site.value.auth_card_position;
    else site.value.auth_card_position = position;
    await mount(AuthView, { mode: 'login' });
    const authElement = host.querySelector('.oa-auth');
    expect(authElement?.classList.contains(expectedClass)).toBe(true);
  });

  it.each([
    { position: 'left', expectedClass: 'position-left' },
    { position: 'right', expectedClass: 'position-right' },
    { position: 'center', expectedClass: 'position-center' },
  ] as const)('applies $expectedClass to AuthView (register) when auth_card_position is $position', async ({ position, expectedClass }) => {
    site.value = { ...siteInfo.value, auth_card_position: position };
    await mount(AuthView, { mode: 'register' });
    const authElement = host.querySelector('.oa-auth');
    expect(authElement?.classList.contains(expectedClass)).toBe(true);
  });

  it('applies position class to CompleteSignupView', async () => {
    site.value = { ...siteInfo.value, auth_card_position: 'left' };
    vi.spyOn(oauthApi, 'fetchPendingSignup').mockResolvedValue({
      provider: 'github',
      provider_name: 'GitHub',
      login: 'octocat',
      email: '',
      needs: { fields: [], email: false },
      email_domains: [],
      verify_email: false,
    });
    await mount(CompleteSignupView);
    const authElement = host.querySelector('.oa-auth');
    expect(authElement?.classList.contains('position-left')).toBe(true);
  });

  it('applies position class to TwoFactorEnrolView', async () => {
    site.value = { ...siteInfo.value, auth_card_position: 'right' };
    await mount(TwoFactorEnrolView);
    const authElement = host.querySelector('.oa-auth');
    expect(authElement?.classList.contains('position-right')).toBe(true);
  });

  it('applies position class to VerifyView', async () => {
    site.value = { ...siteInfo.value, auth_card_position: 'left' };
    await mount(VerifyView);
    const authElement = host.querySelector('.oa-auth');
    expect(authElement?.classList.contains('position-left')).toBe(true);
  });
});

describe('registering with proof-of-work (PoW)', () => {
  it('fetches PoW challenge and submits solved nonce when pow_on_signup is active', async () => {
    site.value = { ...siteInfo.value, pow_on_signup: true };

    const salt = 'a1b2c3d4e5f60718293a4b5c6d7e8f90';
    const nonce = 3;
    const challenge = (await import('node:crypto')).createHash('sha256').update(salt + nonce).digest('hex');

    const fetchSpy = vi.spyOn(authApi, 'fetchPoWChallenge').mockResolvedValue({
      challenge,
      salt,
      maxNumber: 10,
      expires: Date.now() + 300000,
      signature: 'test-sig',
    });

    const regSpy = vi.spyOn(authApi, 'register').mockResolvedValue({
      user: { id: 'u1', username: 'alice', role: 'user', status: 'active', email_verified: true, allow_stats: true, allow_delete_conversations: true, api_restricted: false, api_restricted_until: 0, api_restriction_source: '', created_at: 0, updated_at: 0, last_login_at: 0, group_id: '', group_name: '', group_expires_at: 0, email: '', nickname: '', avatar: '', bio: '' },
    });

    await mount(AuthView, { mode: 'register' });
    expect(fetchSpy).toHaveBeenCalled();

    type(fieldInput(t('username')), 'alice');
    type(fieldInput(t('password')), 'super-secret-password');

    button(host, t('createAccount')).click();
    await settle();

    expect(regSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        username: 'alice',
        password: 'super-secret-password',
        pow: expect.objectContaining({
          nonce: 3,
          challenge,
          salt,
          maxNumber: 10,
          signature: 'test-sig',
        }),
      }),
    );
  });

  it('does not fetch or send PoW when pow_on_signup is disabled', async () => {
    site.value = { ...siteInfo.value, pow_on_signup: false };

    const fetchSpy = vi.spyOn(authApi, 'fetchPoWChallenge');
    const regSpy = vi.spyOn(authApi, 'register').mockResolvedValue({
      user: { id: 'u1', username: 'bob', role: 'user', status: 'active', email_verified: true, allow_stats: true, allow_delete_conversations: true, api_restricted: false, api_restricted_until: 0, api_restriction_source: '', created_at: 0, updated_at: 0, last_login_at: 0, group_id: '', group_name: '', group_expires_at: 0, email: '', nickname: '', avatar: '', bio: '' },
    });

    await mount(AuthView, { mode: 'register' });
    expect(fetchSpy).not.toHaveBeenCalled();

    type(fieldInput(t('username')), 'bob');
    type(fieldInput(t('password')), 'super-secret-password');

    button(host, t('createAccount')).click();
    await settle();

    expect(regSpy).toHaveBeenCalledWith(
      expect.not.objectContaining({
        pow: expect.anything(),
      }),
    );
  });
});
