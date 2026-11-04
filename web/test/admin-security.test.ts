import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, type SignInApplication } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import AdminSecurity from '../src/views/admin/AdminSecurity.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import settingsFixture from './fixtures/settings.json';
import { settle, button } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let actions: HTMLElement;

// Real timers: the page's own delays (a label reset, a confirm arming) are
// cleared on unmount, and nothing here depends on advancing them.

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, actions);
  vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
  vi.spyOn(adminApi, 'securityEvents').mockResolvedValue({ events: [], total: 0, limit: 25, offset: 0 });
  vi.spyOn(adminApi, 'twoFactorAdoption').mockResolvedValue({
    policy: 'optional', accounts: 0, enabled: 0, admins: 0, admins_enabled: 0, admins_without: [],
    remember_days: 0, issuer_fallback: 'Obsidian Arc', available: true,
  });
  vi.spyOn(adminApi, 'mail').mockResolvedValue({
    host: '', port: 587, username: '', from: '', implicit_tls: false, public_url: '', password_set: false,
  });
  vi.spyOn(adminApi, 'userCheck').mockResolvedValue({
    enabled: false, exempt_domains: [], failure_mode: 'reject', api_key_set: false,
  });
  vi.spyOn(adminApi, 'applications').mockResolvedValue({ applications: [], issuer: 'https://arc.test', scopes: [] });
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
});

async function mountSecurity(): Promise<void> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
  app = createApp({ setup() {
    provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
    return () => h(AdminSecurity);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

describe('a retry after a failed first load', () => {
  it('keeps Save disabled until the reload has a baseline', async () => {
    let finishReload: (value: { settings: Record<string, string> }) => void = () => {};
    const reloaded = new Promise<{ settings: Record<string, string> }>((resolve) => { finishReload = resolve; });
    vi.spyOn(adminApi, 'settings')
      .mockRejectedValueOnce(new Error('network down'))
      .mockReturnValueOnce(reloaded);
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSecurity();
    expect(host.textContent).toContain(t('couldNotLoad'));

    button(host, t('tryAgain')).click();
    await settle();
    expect(button(actions, t('save')).disabled).toBe(true);

    finishReload({ settings: { ...settingsFixture } });
    await settle();
    expect(button(actions, t('save')).disabled).toBe(false);
    expect(save).not.toHaveBeenCalled();
  });
});

describe('the two-factor prompt on save', () => {
  it('returns the Save label when the code prompt is cancelled', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockRejectedValue(
      new ApiError(403, 'two_factor_code_required', 'Enter your two-factor code.'),
    );
    await mountSecurity();

    const registration = host.querySelector<HTMLInputElement>('#secRegistration input[type="checkbox"]');
    expect(registration).not.toBeNull();
    registration!.click();
    await nextTick();

    button(actions, t('save')).click();
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    const form = document.querySelector<HTMLElement>('.oa-auth-form');
    expect(form).not.toBeNull();

    button(form!, t('cancel')).click();
    await settle();
    expect(document.querySelector('.oa-auth-form')).toBeNull();
    expect(button(actions, t('save')).textContent?.trim()).toBe(t('save'));
  });
});

describe('a value past the server limit', () => {
  // The server refuses the whole save over one value this long. The refusal has
  // to come before the two-factor prompt opens, or the operator types a code for
  // a save that was never going to be accepted.
  it('refuses a signup review prompt past the limit before the code prompt', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const save = vi.spyOn(adminApi, 'saveSettings').mockResolvedValue({ settings: { ...settingsFixture } });
    await mountSecurity();

    const prompt = [...host.querySelectorAll<HTMLLabelElement>('label.oa-field')]
      .find((label) => label.querySelector('.oa-field-label')?.textContent === t('signupReviewPrompt'));
    const area = prompt?.querySelector<HTMLTextAreaElement>('textarea');
    expect(area).toBeTruthy();
    area!.value = 'a'.repeat(8 * 1024 + 1);
    area!.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();

    button(actions, t('save')).click();
    await settle();
    expect(save).not.toHaveBeenCalled();
    expect(document.querySelector('.oa-auth-form')).toBeNull();
    expect(host.querySelector('.oa-control-flash')?.textContent).toBe(t('settingTooLong'));
  });
});

describe('the user-check card', () => {
  it('draws an empty exempt-domain list the server sends as null', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    vi.spyOn(adminApi, 'userCheck').mockResolvedValue({
      enabled: false, exempt_domains: null as unknown as string[], failure_mode: 'reject', api_key_set: false,
    });
    await mountSecurity();

    const card = host.querySelector<HTMLElement>('#secUserCheck');
    // The load failure panel is what a TypeError on the null list turns the card into.
    expect(card?.querySelector('.oa-notice')).toBeNull();
    const domains = card?.querySelector<HTMLTextAreaElement>('textarea');
    expect(domains).not.toBeNull();
    expect(domains!.value).toBe('');
  });
});

// The log is read by an operator in their own language. An event type with no
// label falls through to its raw identifier, which is what this guards against.
describe('the security log names the events the server writes', () => {
  it('words an administrator password reset and account deletion', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    vi.spyOn(adminApi, 'securityEvents').mockResolvedValue({
      events: [
        { id: 'e1', at: Date.now(), event: 'password_reset', severity: 'warning' },
        { id: 'e2', at: Date.now(), event: 'account_deleted', severity: 'danger' },
      ],
      total: 2, limit: 25, offset: 0,
    });
    await mountSecurity();

    expect(host.textContent).toContain(t('securityEventPasswordReset'));
    expect(host.textContent).toContain(t('securityEventAccountDeleted'));
    expect(host.textContent).not.toContain('password_reset');
    expect(host.textContent).not.toContain('account_deleted');
  });
});

describe('rotating an application secret', () => {
  const application: SignInApplication = {
    id: 'app-1', client_id: 'client-1', name: 'Reporter', description: '', redirect_uris: [], scopes: [],
    trusted: false, disabled: false, confidential: true, created_at: 0, updated_at: 0,
  };

  it('asks for a second click before the old secret is replaced', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    vi.spyOn(adminApi, 'applications').mockResolvedValue({ applications: [application], issuer: 'https://arc.test', scopes: [] });
    const rotate = vi.spyOn(adminApi, 'rotateApplicationSecret').mockResolvedValue({ client_secret: 'new-secret' });
    await mountSecurity();

    const row = host.querySelector<HTMLElement>('.oa-app-actions');
    expect(row).not.toBeNull();
    button(row!, t('applicationRotate')).click();
    await settle();
    expect(rotate).not.toHaveBeenCalled();

    button(row!, t('confirmWord')).click();
    await settle();
    expect(rotate).toHaveBeenCalledTimes(1);
    expect(rotate).toHaveBeenCalledWith(application.id);
  });
});

function fieldFor(root: ParentNode, label: string): HTMLElement {
  const found = [...root.querySelectorAll<HTMLElement>('.oa-field')]
    .find((node) => node.querySelector('.oa-field-label')?.textContent === label);
  if (!found) throw new Error(`Missing field: ${label}`);
  return found;
}

// The server falls back to Turnstile for a mode it does not know, an empty one
// included, whatever the sign-up switch says. The select has to say the same,
// or it reports a sign-up with no challenge that in fact has one.
describe('an empty sign-up challenge mode', () => {
  it('reads as Turnstile, the mode the server applies', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({
      settings: { ...settingsFixture, 'registration.captcha_mode': '', 'turnstile.on_signup': 'false' },
    });
    await mountSecurity();

    const select = fieldFor(host, t('captchaMode')).querySelector('.oa-select-label');
    expect(select?.textContent).toBe(t('captchaModeTurnstile'));
  });
});

// The form is what an operator came for; the cards beside it each have their
// own loading state, so a slow one must not keep the whole page on "Loading".
describe('the first load', () => {
  it('draws the form before the cards that load on their own have answered', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    const never = new Promise<never>(() => {});
    vi.spyOn(adminApi, 'securityEvents').mockReturnValue(never);
    vi.spyOn(adminApi, 'applications').mockReturnValue(never);
    vi.spyOn(adminApi, 'twoFactorAdoption').mockReturnValue(never);
    vi.spyOn(adminApi, 'mail').mockReturnValue(never);
    vi.spyOn(adminApi, 'userCheck').mockReturnValue(never);
    await mountSecurity();

    expect(host.querySelector('#secRegistration')).not.toBeNull();
    expect(button(actions, t('save')).disabled).toBe(false);
    // Not "no applications are registered" while the list is still on its way.
    const applications = host.querySelector<HTMLElement>('#secApplications');
    expect(applications?.textContent).not.toContain(t('applicationsEmpty'));
    expect(applications?.textContent).toContain(t('loading'));
  });
});

describe('the user-check test', () => {
  it('waits while a save is still in flight', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: { ...settingsFixture } });
    vi.spyOn(adminApi, 'userCheck').mockResolvedValue({
      enabled: true, exempt_domains: [], failure_mode: 'reject', api_key_set: true,
    });
    vi.spyOn(adminApi, 'saveUserCheck').mockReturnValue(new Promise<never>(() => {}));
    const probe = vi.spyOn(adminApi, 'testUserCheck').mockResolvedValue({ skipped: false, disposable: false });
    await mountSecurity();

    const card = host.querySelector<HTMLElement>('#secUserCheck')!;
    const email = fieldFor(card, t('userCheckTestEmail')).querySelector('input')!;
    email.value = 'someone@example.com';
    email.dispatchEvent(new Event('input', { bubbles: true }));
    button(card, t('save')).click();
    await settle();

    const test = button(card, t('userCheckTest'));
    expect(test.disabled).toBe(true);
    test.click();
    await settle();
    expect(probe).not.toHaveBeenCalled();
  });
});
