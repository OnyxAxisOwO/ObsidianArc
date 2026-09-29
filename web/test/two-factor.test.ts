import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import * as authApi from '../src/api/auth';
import type { Account } from '../src/api/auth';
import { ApiError } from '../src/api/client';
import * as twoFactorApi from '../src/api/twofactor';
import type { TwoFactorStatus } from '../src/api/twofactor';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { serverOwned } from '../src/lib/next';
import {
  adopt, currentUser, forget, pendingSecondFactor, site, siteInfo, startSession,
} from '../src/stores/session';
import AuthView from '../src/views/AuthView.vue';
import SecuritySection from '../src/views/settings/SecuritySection.vue';
import TwoFactorWizard from '../src/views/settings/TwoFactorWizard.vue';

// Two-step verification from the browser's side: the code step of signing in,
// the setup wizard, and the security settings that switch it on and off.

const route = { path: '/login', query: {} as Record<string, string> };
const replace = vi.fn();
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace }),
  useRoute: () => route,
}));

const ACCOUNT: Account = {
  id: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
  username: 'ada',
  email: '',
  nickname: 'Ada',
  avatar: '',
  bio: '',
  role: 'user',
  group_id: 'g1',
  group_expires_at: 0,
  group_name: 'Default',
  status: 'active',
  created_at: 0,
  updated_at: 0,
  last_login_at: 0,
  email_verified: true,
  allow_stats: true,
  allow_delete_conversations: true,
  api_restricted: false,
  api_restricted_until: 0,
  api_restriction_source: '',
};

const SETUP = {
  secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
  uri: 'otpauth://totp/Arc:ada?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Arc',
  issuer: 'Arc',
  account: 'ada',
  qr: { size: 21, path: 'M0 0h7v1h-7z' },
};

const CODES = ['abcde-fghjk', 'mnpqr-stvwx', '01234-56789'];

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  route.path = '/login';
  route.query = {};
  replace.mockReset();
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  site.value = null;
  forget();
});

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  const panels = document.createElement('div');
  document.body.append(panels);
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      return () => h(component, props);
    },
  });
  app.mount(host);
  await settle();
}

function type(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

describe('the code step of signing in', () => {
  async function signInWithPassword(): Promise<void> {
    await mount(AuthView, { mode: 'login' });
    const [identity, password] = [...host.querySelectorAll<HTMLInputElement>('input')];
    type(identity!, 'ada');
    type(password!, 'a-good-password');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
  }

  it('asks for the code once the password was right, and finishes with it', async () => {
    vi.spyOn(authApi, 'login').mockResolvedValue({ two_factor: true });
    const finish = vi.spyOn(authApi, 'completeSignIn').mockResolvedValue({ user: ACCOUNT });
    await signInWithPassword();

    expect(host.querySelector('.oa-auth-title')!.textContent).toBe(t('twoFactorSignInTitle'));
    const field = host.querySelector<HTMLInputElement>('input.oa-2fa-code')!;
    expect(field.getAttribute('autocomplete')).toBe('one-time-code');
    // Nothing is signed in on the password alone.
    expect(currentUser.value).toBeNull();

    // Six digits is the whole answer, so it goes without a click.
    type(field, '123 456');
    await settle();
    expect(finish).toHaveBeenCalledWith('123 456', false);
    expect(currentUser.value?.username).toBe('ada');
    expect(replace).toHaveBeenCalledWith('/');
  });

  it('says a wrong code is wrong, and keeps asking', async () => {
    pendingSecondFactor.value = true;
    vi.spyOn(authApi, 'completeSignIn').mockRejectedValue(new ApiError(400, 'two_factor_code', 'That code is not valid.'));
    await mount(AuthView, { mode: 'login' });

    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '000000');
    await settle();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('twoFactorCodeWrong'));
    expect(host.querySelector('input.oa-2fa-code')).not.toBeNull();
  });

  // A provider sign-in arrives by redirect, halfway: the card opens on the
  // code rather than asking for a password nobody typed.
  it('opens on the code when a sign-in is already halfway', async () => {
    pendingSecondFactor.value = true;
    await mount(AuthView, { mode: 'login' });
    expect(host.querySelector('input.oa-2fa-code')).not.toBeNull();
    expect(host.querySelector('input[type="password"]')).toBeNull();
  });

  it('goes back to the password when the half-way sign-in has expired', async () => {
    pendingSecondFactor.value = true;
    vi.spyOn(authApi, 'completeSignIn').mockRejectedValue(new ApiError(401, 'two_factor_expired', 'expired'));
    await mount(AuthView, { mode: 'login' });

    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();
    expect(host.querySelector('input[type="password"]')).not.toBeNull();
    expect(host.querySelector('.oa-auth-error')!.textContent).toBe(t('twoFactorExpired'));
  });

  it('takes a recovery code, which does not submit itself', async () => {
    pendingSecondFactor.value = true;
    const finish = vi.spyOn(authApi, 'completeSignIn').mockResolvedValue({ user: ACCOUNT });
    await mount(AuthView, { mode: 'login' });

    button(t('twoFactorUseRecovery')).click();
    await settle();
    expect(host.querySelector('.oa-field-label')!.textContent).toBe(t('twoFactorRecoveryLabel'));
    expect(host.textContent).toContain(t('twoFactorLostHelp'));

    const field = host.querySelector<HTMLInputElement>('input.oa-2fa-code')!;
    type(field, 'abcde-fghjk');
    await settle();
    expect(finish).not.toHaveBeenCalled();
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
    expect(finish).toHaveBeenCalledWith('abcde-fghjk', false);
  });

  it('offers to remember the browser only where the operator allows it', async () => {
    pendingSecondFactor.value = true;
    await mount(AuthView, { mode: 'login' });
    expect(host.querySelector('input[type="checkbox"]')).toBeNull();
    app?.unmount();

    site.value = { ...siteInfo.value, two_factor_remember_days: 30 };
    const finish = vi.spyOn(authApi, 'completeSignIn').mockResolvedValue({ user: ACCOUNT });
    await mount(AuthView, { mode: 'login' });
    expect(host.textContent).toContain(t('twoFactorRemember', { days: 30 }));
    const remember = host.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    remember.checked = true;
    remember.dispatchEvent(new Event('change', { bubbles: true }));
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '654321');
    await settle();
    expect(finish).toHaveBeenCalledWith('654321', true);
  });

  it('leaves the half-way sign-in for another account', async () => {
    pendingSecondFactor.value = true;
    const out = vi.spyOn(authApi, 'logout').mockResolvedValue();
    await mount(AuthView, { mode: 'login' });
    button(t('twoFactorOtherAccount')).click();
    await settle();
    expect(out).toHaveBeenCalled();
    expect(pendingSecondFactor.value).toBe(false);
    expect(host.querySelector('input[type="password"]')).not.toBeNull();
  });

  // Another site sends a visitor through the sign-in with its own request as
  // the destination, and the router has no screen by that name.
  it('follows a destination the server owns with a real navigation', async () => {
    const assign = vi.fn();
    Object.defineProperty(window, 'location', { configurable: true, writable: true, value: { assign, href: '' } });
    route.query = { next: '/oauth/authorize?client_id=wiki&state=s' };
    pendingSecondFactor.value = true;
    vi.spyOn(authApi, 'completeSignIn').mockResolvedValue({ user: ACCOUNT });
    await mount(AuthView, { mode: 'login' });

    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();
    expect(assign).toHaveBeenCalledWith('/oauth/authorize?client_id=wiki&state=s');
    expect(replace).not.toHaveBeenCalled();
    expect(serverOwned('/settings')).toBe(false);
  });
});

describe('the session store', () => {
  it('remembers that a sign-in is halfway', async () => {
    vi.spyOn(authApi, 'fetchMe').mockRejectedValue(new ApiError(401, 'two_factor_pending', 'Enter the code.'));
    vi.spyOn(authApi, 'fetchSite').mockResolvedValue(siteInfo.value);
    await startSession();
    expect(pendingSecondFactor.value).toBe(true);
    expect(currentUser.value).toBeNull();

    adopt(ACCOUNT);
    expect(pendingSecondFactor.value).toBe(false);
  });
});

describe('the setup wizard', () => {
  it('walks from an app to a picture to a code to the recovery codes', async () => {
    const begin = vi.spyOn(twoFactorApi, 'beginTwoFactor').mockResolvedValue(SETUP);
    const enable = vi.spyOn(twoFactorApi, 'enableTwoFactor')
      .mockResolvedValue({ recovery_codes: CODES, user: { ...ACCOUNT, two_factor_at: 1 } });
    const done = vi.fn();
    await mount(TwoFactorWizard, { onDone: done });

    // Nothing is asked of the server until somebody says they have an app.
    expect(begin).not.toHaveBeenCalled();
    expect(host.textContent).toContain(t('twoFactorAppTitle'));
    button(t('twoFactorAppHaveOne')).click();
    await settle();

    // The picture carries its quiet zone, and the key is there as text.
    const svg = host.querySelector('svg.oa-2fa-qr')!;
    expect(svg.getAttribute('viewBox')).toBe('-4 -4 29 29');
    expect(host.querySelector('.oa-2fa-qr-dark')!.getAttribute('d')).toBe(SETUP.qr.path);
    expect(host.textContent).toContain(t('twoFactorScanBody', { issuer: 'Arc' }));
    button(t('twoFactorCantScan')).click();
    await settle();
    expect(host.querySelector('.oa-2fa-secret')!.textContent).toContain('JBSW Y3DP EHPK 3PXP');

    button(t('twoFactorContinue')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();
    expect(enable).toHaveBeenCalledWith('123456');

    // The codes, once, and no way past them without saying they are kept.
    const codes = [...host.querySelectorAll('.oa-2fa-codes li')].map((node) => node.textContent);
    expect(codes).toEqual(CODES);
    const finish = button(t('twoFactorFinish'));
    expect(finish.disabled).toBe(true);
    // Not handed over yet: a gate watching the account would vanish with
    // the codes still unsaved.
    expect(done).not.toHaveBeenCalled();

    const saved = host.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    saved.checked = true;
    saved.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    finish.click();
    expect(done).toHaveBeenCalledWith({ ...ACCOUNT, two_factor_at: 1 });
  });

  it('starts again when the setup it was confirming has expired', async () => {
    vi.spyOn(twoFactorApi, 'beginTwoFactor').mockResolvedValue(SETUP);
    vi.spyOn(twoFactorApi, 'enableTwoFactor')
      .mockRejectedValue(new ApiError(400, 'two_factor_no_setup', 'expired'));
    await mount(TwoFactorWizard);
    button(t('twoFactorAppHaveOne')).click();
    await settle();
    button(t('twoFactorContinue')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();
    expect(host.textContent).toContain(t('twoFactorAppTitle'));
    expect(host.textContent).toContain(t('twoFactorNoSetup'));
  });
});

describe('the security settings', () => {
  const ON: TwoFactorStatus = {
    available: true, enabled: true, enabled_at: Date.UTC(2026, 8, 1), recovery_remaining: 8,
    mandatory: false, policy: 'optional', remember_days: 0,
  };

  it('offers the wizard while it is off', async () => {
    adopt(ACCOUNT);
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue({ ...ON, enabled: false, enabled_at: 0, recovery_remaining: 0 });
    await mount(SecuritySection);
    expect(host.textContent).toContain(t('twoFactorOff'));
    button(t('twoFactorTurnOn')).click();
    await settle();
    expect(host.querySelector('.oa-2fa-wizard')).not.toBeNull();
  });

  it('turns it off with a code', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    const disable = vi.spyOn(twoFactorApi, 'disableTwoFactor').mockResolvedValue({ user: ACCOUNT });
    await mount(SecuritySection);

    expect(host.textContent).toContain(t('twoFactorRecoveryRowHint', { count: 8 }));
    button(t('twoFactorTurnOffShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
    expect(disable).toHaveBeenCalledWith('123456');
    expect(currentUser.value?.two_factor_at ?? 0).toBe(0);
  });

  it('has no way to turn it off where the policy requires it', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue({ ...ON, mandatory: true, policy: 'everyone' });
    await mount(SecuritySection);
    expect(host.textContent).toContain(t('twoFactorMandatoryNote'));
    expect(() => button(t('twoFactorTurnOffShort'))).toThrow();
    expect(() => button(t('twoFactorRegenerateShort'))).not.toThrow();
  });

  it('warns when the recovery codes are running out', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue({ ...ON, recovery_remaining: 2 });
    await mount(SecuritySection);
    expect(host.textContent).toContain(t('twoFactorRecoveryLow', { count: 2 }));
  });
});
