import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type AdminMailSettings, type AdminUserCheckSettings } from '../src/admin/api';
import * as authApi from '../src/api/auth';
import type { Account } from '../src/api/auth';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import AuthView from '../src/views/AuthView.vue';
import VerifyBanner from '../src/announce/VerifyBanner.vue';
import VerifyView from '../src/views/VerifyView.vue';
import AdminSecurity from '../src/views/admin/AdminSecurity.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import { refusalText } from '../src/lib/refusal';
import { verificationErrorText } from '../src/lib/verification-error';

const ACCOUNT: Account = {
  id: 'u1', username: 'ada', email: 'ada@example.com', qq: '', nickname: 'Ada', avatar: '', bio: '',
  role: 'user', group_id: 'g1', group_expires_at: 0, group_name: 'Default', status: 'active',
  created_at: 1, updated_at: 1, last_login_at: 1, email_verified: false,
  allow_stats: true, allow_delete_conversations: true, api_restricted: false,
  api_restricted_until: 0, api_restriction_source: '',
};

const MAIL: AdminMailSettings = {
  host: 'smtp.example.com', port: 465, username: 'mailer', from: 'arc@example.com',
  implicit_tls: true, public_url: 'https://arc.example.com', password_set: true,
};

const USER_CHECK: AdminUserCheckSettings = {
  enabled: false, exempt_domains: ['gmail.com', 'outlook.com'], failure_mode: 'reject', api_key_set: true,
};

let app: App | undefined;
let testRouter: ReturnType<typeof createRouter> | undefined;
let host: HTMLElement;
let actions: HTMLElement;

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

function fieldInput(root: ParentNode, label: string): HTMLInputElement {
  const field = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === label);
  const input = field?.querySelector<HTMLInputElement>('input');
  if (!input) throw new Error(`Missing field: ${label}`);
  return input;
}

function fieldArea(root: ParentNode, label: string): HTMLTextAreaElement {
  const field = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === label);
  const area = field?.querySelector<HTMLTextAreaElement>('textarea');
  if (!area) throw new Error(`Missing field: ${label}`);
  return area;
}

function type(input: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label || node.getAttribute('aria-label') === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

async function mount(component: Component, path = '/'): Promise<void> {
  testRouter = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await testRouter.push(path);
  await testRouter.isReady();
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(component);
  } });
  app.use(testRouter);
  app.mount(host);
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
  site.value = { ...siteInfo.value, verify_email: true };
  adopt(ACCOUNT);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  testRouter = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  forget();
  site.value = null;
});

describe('email verification controls', () => {
  it('strips non-digits and verifies a six-digit code, then refreshes the signed-in account', async () => {
    const verify = vi.spyOn(authApi, 'verifyEmailCode').mockResolvedValue();
    const refresh = vi.spyOn(authApi, 'fetchMe').mockResolvedValue({
      user: { ...ACCOUNT, email_verified: true }, preferences: {},
    });
    await mount(VerifyBanner);

    const input = host.querySelector<HTMLInputElement>('.oa-verify-code input')!;
    type(input, '12a34-56');
    await nextTick();
    expect(input.value).toBe('123456');

    button(host, t('verifyCodeSubmit')).click();
    await settle();

    expect(verify).toHaveBeenCalledWith('123456');
    expect(refresh).toHaveBeenCalledOnce();
    expect(host.querySelector('.oa-verify-banner')).toBeNull();
  });

  it('shows a successful resend state and surfaces throttling errors', async () => {
    const resend = vi.spyOn(authApi, 'resendVerification').mockResolvedValue();
    await mount(VerifyBanner);

    button(host, t('verifyResend')).click();
    await settle();
    expect(resend).toHaveBeenCalledOnce();
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('verifyResendSent'));
    expect(button(host, t('verifyResend')).disabled).toBe(false);

    resend.mockRejectedValueOnce(new ApiError(429, 'verification_resend_too_soon', 'server English text'));
    button(host, t('verifyResend')).click();
    await settle();
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('verifyResendTooSoon'));
  });

  it('localizes invalid codes and supports both languages for verification errors', async () => {
    const verify = vi.spyOn(authApi, 'verifyEmailCode')
      .mockRejectedValueOnce(new ApiError(400, 'verification_invalid', 'server English text'))
      .mockRejectedValueOnce(new ApiError(429, 'verification_code_limited', 'server English text'));
    await mount(VerifyBanner);
    type(host.querySelector<HTMLInputElement>('.oa-verify-code input')!, '123456');
    await nextTick();
    button(host, t('verifyCodeSubmit')).click();
    await settle();
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('verifyErrorInvalid'));

    await changeLanguage('zh');
    await nextTick();
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('verifyErrorInvalid'));
    expect(verificationErrorText(new ApiError(400, 'verification_expired', 'server text'))).toBe(t('verifyErrorExpired'));
    expect(verificationErrorText(new ApiError(400, 'verification_code_expired', 'server text'))).toBe(t('verifyCodeExpired'));
    expect(verificationErrorText(new ApiError(429, 'verification_code_limited', 'server text'))).toBe(t('verifyCodeLimited'));
    expect(t('verifyCodeLimited')).toContain('邮件中的验证链接');
    expect(t('verifyCodeLimited')).toContain('24 小时');
    expect(verificationErrorText(new ApiError(409, 'already_verified', 'server text'))).toBe(t('verifyAlreadyDone'));
    expect(verificationErrorText(new ApiError(429, 'verification_resend_too_soon', 'server text'))).toBe(t('verifyResendTooSoon'));
    expect(verificationErrorText(new ApiError(503, 'mail_unavailable', 'server text'))).toBe(t('mailDeliveryUnavailable'));

    button(host, t('verifyCodeSubmit')).click();
    await settle();
    expect(verify).toHaveBeenCalledTimes(2);
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('verifyCodeLimited'));
    await changeLanguage('en');
    await nextTick();
    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('verifyCodeLimited'));
    expect(host.querySelector('[role="status"]')?.textContent).toContain('email link');
    expect(host.querySelector('[role="status"]')?.textContent).toContain('24 hours');
  });

  it('waits for a deliberate click before using the link, then refreshes the signed-in account', async () => {
    const verify = vi.spyOn(authApi, 'verifyEmail').mockResolvedValue();
    const refresh = vi.spyOn(authApi, 'fetchMe').mockResolvedValue({
      user: { ...ACCOUNT, email_verified: true }, preferences: {},
    });
    await mount(VerifyView, '/verify?token=single-use-token');
    await settle();

    expect(verify).not.toHaveBeenCalled();
    expect(testRouter?.currentRoute.value.query['token']).toBeUndefined();
    expect(host.querySelector('h1')?.textContent).toBe(t('verifyPageReadyTitle'));
    expect(host.querySelector('.oa-auth-sub')?.textContent).toBe(t('verifyPageReadyBody'));

    await changeLanguage('zh');
    await nextTick();
    expect(host.querySelector('h1')?.textContent).toBe(t('verifyPageReadyTitle'));
    expect(button(host, t('verifyPageConfirm')).textContent).toBe(t('verifyPageConfirm'));
    await changeLanguage('en');
    await nextTick();
    button(host, t('verifyPageConfirm')).click();
    await settle();

    expect(verify).toHaveBeenCalledWith('single-use-token');
    expect(refresh).toHaveBeenCalledOnce();
    expect(host.querySelector('h1')?.textContent).toBe(t('verifyPageDone'));
    expect(host.querySelector('button')?.textContent).toBe(t('verifyPageContinue'));
  });

  it('does not submit without a token and offers a sign-in route', async () => {
    const verify = vi.spyOn(authApi, 'verifyEmail');
    await mount(VerifyView, '/verify');

    expect(verify).not.toHaveBeenCalled();
    expect(host.querySelector('h1')?.textContent).toBe(t('verifyPageMissingTitle'));
    expect(host.querySelector('.oa-auth-sub')?.textContent).toBe(t('verifyPageMissingBody'));
    button(host, t('signIn')).click();
    await settle();
    expect(testRouter?.currentRoute.value.path).toBe('/login');
  });

  it('shows a localized error when a verification link has expired', async () => {
    const verify = vi.spyOn(authApi, 'verifyEmail').mockRejectedValue(
      new ApiError(400, 'verification_expired', 'server English text'),
    );
    await mount(VerifyView, '/verify?token=expired-token');
    await settle();

    expect(verify).not.toHaveBeenCalled();
    button(host, t('verifyPageConfirm')).click();
    await settle();
    expect(verify).toHaveBeenCalledWith('expired-token');
    expect(host.querySelector('h1')?.textContent).toBe(t('verifyPageFailedTitle'));
    expect(host.querySelector('.oa-auth-sub')?.textContent).toBe(t('verifyErrorExpired'));
    button(host, t('signIn')).click();
    await settle();
    expect(testRouter?.currentRoute.value.path).toBe('/login');
  });

  it('translates disposable-address and screening-outage registration errors', () => {
    expect(refusalText(new ApiError(400, 'disposable_email', 'raw'))).toBe(t('disposableEmailRejected'));
    expect(refusalText(new ApiError(503, 'email_screening_unavailable', 'raw'))).toBe(t('emailScreeningUnavailable'));
  });

  it('explains the link and six-digit code on the registration form in both languages', async () => {
    await mount({ setup: () => () => h(AuthView, { mode: 'register' }) }, '/register');
    const email = host.querySelector<HTMLInputElement>('.oa-field input[type="email"]');
    const hint = email?.closest('.oa-field')?.querySelector('.oa-field-hint');
    expect(hint?.textContent).toContain('six-digit code');
    expect(hint?.textContent).toContain('while signed in');

    await changeLanguage('zh');
    await nextTick();
    expect(hint?.textContent).toContain('6 位验证码');
    expect(hint?.textContent).toContain('登录后');
  });
});

describe('administrator mail settings', () => {
  async function mountSecurity(): Promise<HTMLElement> {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: {}, groups: [], mail_configured: true });
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

    const category = [...host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab')]
      .find((node) => node.querySelector('strong')?.textContent === t('controlVerification'));
    if (!category) throw new Error('Missing verification category');
    category.click();
    await nextTick();
    const card = host.querySelector<HTMLElement>('#secMail');
    if (!card) throw new Error('Missing mail settings card');
    return card;
  }

  it('keeps an empty password on save, sends a test email, and confirms explicit password clearing', async () => {
    const saved = vi.spyOn(adminApi, 'saveMail')
      .mockResolvedValueOnce(MAIL)
      .mockResolvedValueOnce({ ...MAIL, password_set: false });
    const test = vi.spyOn(adminApi, 'testMail').mockResolvedValue();
    const card = await mountSecurity();

    type(fieldInput(card, t('mailHost')), 'smtp-new.example.com');
    button(card, t('save')).click();
    await settle();
    expect(saved).toHaveBeenNthCalledWith(1, {
      host: 'smtp-new.example.com', port: 465, username: 'mailer', from: 'arc@example.com',
      implicit_tls: true, public_url: 'https://arc.example.com', password: '', clear_password: false,
    });

    type(fieldInput(card, t('mailTestTo')), 'ops@example.com');
    await nextTick();
    button(card, t('mailTestSend')).click();
    await settle();
    expect(test).toHaveBeenCalledWith('ops@example.com');

    type(fieldInput(card, t('mailUsername')), 'unsaved-edit');
    button(card, t('mailClearPassword')).click();
    await nextTick();
    button(card, t('mailClearPasswordConfirm')).click();
    await settle();
    expect(saved).toHaveBeenNthCalledWith(2, {
      host: MAIL.host, port: MAIL.port, username: MAIL.username, from: MAIL.from,
      implicit_tls: MAIL.implicit_tls, public_url: MAIL.public_url,
      password: '', clear_password: true,
    });
    expect(fieldInput(card, t('mailUsername')).value).toBe('unsaved-edit');
  });

  it('requires saved SMTP settings before the test action uses the live sender', async () => {
    let finishSave!: (settings: AdminMailSettings) => void;
    vi.spyOn(adminApi, 'saveMail').mockImplementation(() => new Promise((resolve) => { finishSave = resolve; }));
    const test = vi.spyOn(adminApi, 'testMail').mockResolvedValue();
    const card = await mountSecurity();

    type(fieldInput(card, t('mailHost')), 'smtp-new.example.com');
    type(fieldInput(card, t('mailTestTo')), 'ops@example.com');
    await nextTick();
    expect(card.textContent).toContain(t('mailTestSaveFirst'));
    expect(button(card, t('mailTestSend')).disabled).toBe(true);
    button(card, t('mailTestSend')).click();
    expect(test).not.toHaveBeenCalled();

    button(card, t('save')).click();
    await nextTick();
    expect(button(card, t('mailTestSend')).disabled).toBe(true);
    finishSave({ ...MAIL, host: 'smtp-new.example.com' });
    await settle();
    expect(button(card, t('mailTestSend')).disabled).toBe(false);
    button(card, t('mailTestSend')).click();
    await settle();
    expect(test).toHaveBeenCalledWith('ops@example.com');
  });

  it('localizes SMTP and UserCheck endpoint failures instead of showing server English', async () => {
    const testMail = vi.spyOn(adminApi, 'testMail')
      .mockRejectedValueOnce(new ApiError(400, 'error', 'SMTP is not configured.'))
      .mockRejectedValueOnce(new ApiError(500, 'unrecognized_mail_error', 'private English detail'));
    const card = await mountSecurity();
    type(fieldInput(card, t('mailTestTo')), 'ops@example.com');
    await nextTick();
    button(card, t('mailTestSend')).click();
    await settle();
    expect(card.querySelector('[role="alert"]')?.textContent).toBe(t('mailTestInvalid'));

    await changeLanguage('zh');
    await nextTick();
    expect(card.querySelector('[role="alert"]')?.textContent).toBe(t('mailTestInvalid'));

    button(card, t('mailTestSend')).click();
    await settle();
    expect(card.querySelector('[role="alert"]')?.textContent).toBe(t('failed'));
    expect(card.textContent).not.toContain('private English detail');
    expect(testMail).toHaveBeenCalledTimes(2);

    vi.spyOn(adminApi, 'testUserCheck').mockRejectedValue(
      new ApiError(429, 'usercheck_test_cooldown', 'A UserCheck test can be run once every five minutes.'),
    );
    const userCard = host.querySelector<HTMLElement>('#secUserCheck')!;
    type(fieldInput(userCard, t('userCheckTestEmail')), 'ada@example.com');
    await nextTick();
    button(userCard, t('userCheckTest')).click();
    await settle();
    expect(userCard.querySelector('[role="alert"]')?.textContent).toBe(t('userCheckTestCooldown'));
    await changeLanguage('en');
    await nextTick();
    expect(userCard.querySelector('[role="alert"]')?.textContent).toBe(t('userCheckTestCooldown'));
  });

  it('saves exact-match domain exemptions, tests an address, and disables screening when clearing its key', async () => {
    const saved = vi.spyOn(adminApi, 'saveUserCheck')
      .mockResolvedValueOnce({ ...USER_CHECK, enabled: true, exempt_domains: ['gmail.com', 'example.com'] })
      .mockResolvedValueOnce({ ...USER_CHECK, enabled: false, exempt_domains: ['gmail.com', 'example.com'], api_key_set: false });
    const test = vi.spyOn(adminApi, 'testUserCheck').mockResolvedValue({ disposable: true });
    await mountSecurity();
    const card = host.querySelector<HTMLElement>('#secUserCheck');
    if (!card) throw new Error('Missing UserCheck settings card');

    expect(card.textContent).toContain(t('userCheckExemptDomainsHint'));
    const enabled = [...card.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.closest('label')?.textContent?.includes(t('userCheckEnabled')));
    if (!enabled) throw new Error('Missing UserCheck enable switch');
    enabled.click();
    type(fieldArea(card, t('userCheckExemptDomains')), 'gmail.com\nexample.com\nGMAIL.COM');
    const selector = card.querySelector<HTMLButtonElement>('.oa-select');
    if (!selector) throw new Error('Missing UserCheck failure mode selector');
    selector.click();
    await settle();
    const allow = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((option) => option.textContent?.trim() === t('userCheckFailureAllow'));
    if (!allow) throw new Error('Missing allow-registration failure mode');
    allow.click();
    await nextTick();

    button(card, t('save')).click();
    await settle();
    expect(saved).toHaveBeenNthCalledWith(1, {
      enabled: true, exempt_domains: ['gmail.com', 'example.com'], failure_mode: 'allow',
      api_key: '', clear_api_key: false,
    });

    type(fieldInput(card, t('userCheckTestEmail')), 'throwaway@temp.example');
    await nextTick();
    button(card, t('userCheckTest')).click();
    await settle();
    expect(test).toHaveBeenCalledWith('throwaway@temp.example');
    expect(card.querySelector('[role="alert"]')?.textContent).toBe(t('userCheckTestDisposable'));

    button(card, t('userCheckClearAPIKey')).click();
    await nextTick();
    button(card, t('userCheckClearAPIKeyConfirm')).click();
    await settle();
    expect(saved).toHaveBeenNthCalledWith(2, {
      enabled: false, exempt_domains: ['gmail.com', 'example.com'], failure_mode: 'reject',
      api_key: '', clear_api_key: true,
    });
  });
});
