import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { type Component } from 'vue';
import * as authApi from '../src/api/auth';
import { ApiError } from '../src/api/client';
import * as twoFactorApi from '../src/api/twofactor';
import type { TwoFactorStatus } from '../src/api/twofactor';
import { changeLanguage, t } from '../src/composables/useI18n';
import { serverOwned } from '../src/lib/next';
import {
  adopt, currentUser, forget, pendingSecondFactor, site, siteInfo, startSession,
} from '../src/stores/session';
import AuthView from '../src/views/AuthView.vue';
import SecuritySection from '../src/views/settings/SecuritySection.vue';
import TwoFactorWizard from '../src/views/settings/TwoFactorWizard.vue';
import { mountComponent, settle, stubLocation, button } from './helpers';
import { makeAccount } from './fixtures/account';

// Two-step verification from the browser's side: the code step of signing in,
// the setup wizard, and the security settings that switch it on and off.

const route = { path: '/login', query: {} as Record<string, string> };
const replace = vi.fn();
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn(), replace }),
  useRoute: () => route,
}));

const ACCOUNT = makeAccount({ id: '01ARZ3NDEKTSV4RRFFQ69G5FAV', email: '', created_at: 0, updated_at: 0 });

const SETUP = {
  secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
  uri: 'otpauth://totp/Arc:ada?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Arc',
  issuer: 'Arc',
  account: 'ada',
  qr: { size: 21, path: 'M0 0h7v1h-7z' },
};

const CODES = ['abcde-fghjk', 'mnpqr-stvwx', '01234-56789'];

let unmount: (() => void) | undefined;
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
  unmount?.();
  unmount = undefined;
  vi.restoreAllMocks();
  site.value = null;
  forget();
});

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  unmount = await mountComponent(component, props, { host });
}

function type(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
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

    button(host, t('twoFactorUseRecovery')).click();
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
    unmount?.();

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
    button(host, t('twoFactorOtherAccount')).click();
    await settle();
    expect(out).toHaveBeenCalled();
    expect(pendingSecondFactor.value).toBe(false);
    expect(host.querySelector('input[type="password"]')).not.toBeNull();
  });

  // Another site sends a visitor through the sign-in with its own request as
  // the destination, and the router has no screen by that name.
  it('follows a destination the server owns with a real navigation', async () => {
    const { assign } = stubLocation();
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
    button(host, t('twoFactorAppHaveOne')).click();
    await settle();

    // The picture carries its quiet zone, and the key is there as text.
    const svg = host.querySelector('svg.oa-2fa-qr')!;
    expect(svg.getAttribute('viewBox')).toBe('-4 -4 29 29');
    expect(host.querySelector('.oa-2fa-qr-dark')!.getAttribute('d')).toBe(SETUP.qr.path);
    expect(host.textContent).toContain(t('twoFactorScanBody', { issuer: 'Arc' }));
    button(host, t('twoFactorCantScan')).click();
    await settle();
    expect(host.querySelector('.oa-2fa-secret')!.textContent).toContain('JBSW Y3DP EHPK 3PXP');

    button(host, t('twoFactorContinue')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();
    expect(enable).toHaveBeenCalledWith('123456');

    // The codes, once, and no way past them without saying they are kept.
    const codes = [...host.querySelectorAll('.oa-2fa-codes li')].map((node) => node.textContent);
    expect(codes).toEqual(CODES);
    const finish = button(host, t('twoFactorFinish'));
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
    button(host, t('twoFactorAppHaveOne')).click();
    await settle();
    button(host, t('twoFactorContinue')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();
    expect(host.textContent).toContain(t('twoFactorAppTitle'));
    expect(host.textContent).toContain(t('twoFactorNoSetup'));
  });

  describe('for an account with a password', () => {
    async function toTheCodeStep(): Promise<void> {
      await mount(TwoFactorWizard);
      button(host, t('twoFactorAppHaveOne')).click();
      await settle();
      button(host, t('twoFactorContinue')).click();
      await settle();
    }

    it('sends the password with the code, and does not submit on the code alone', async () => {
      vi.spyOn(twoFactorApi, 'beginTwoFactor').mockResolvedValue({ ...SETUP, password_required: true });
      const enable = vi.spyOn(twoFactorApi, 'enableTwoFactor')
        .mockResolvedValue({ recovery_codes: CODES, user: { ...ACCOUNT, two_factor_at: 1 } });
      await toTheCodeStep();

      type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
      await settle();
      expect(enable).not.toHaveBeenCalled();
      expect(button(host, t('twoFactorVerifyAndEnable')).disabled).toBe(true);

      type(host.querySelector<HTMLInputElement>('input[type="password"]')!, 'a-good-password');
      await settle();
      button(host, t('twoFactorVerifyAndEnable')).click();
      await settle();
      expect(enable).toHaveBeenCalledWith('123456', 'a-good-password');
      expect(host.querySelectorAll('.oa-2fa-codes li')).toHaveLength(CODES.length);
    });

    it('asks for the password again when it was wrong, keeping the code', async () => {
      vi.spyOn(twoFactorApi, 'beginTwoFactor').mockResolvedValue({ ...SETUP, password_required: true });
      vi.spyOn(twoFactorApi, 'enableTwoFactor')
        .mockRejectedValue(new ApiError(400, 'current_password_wrong', 'wrong'));
      await toTheCodeStep();

      type(host.querySelector<HTMLInputElement>('input[type="password"]')!, 'nope');
      type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
      await settle();
      expect(host.textContent).toContain(t('currentPasswordWrong'));
      expect(host.querySelector<HTMLInputElement>('input[type="password"]')!.value).toBe('');
      expect(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!.value).toBe('123456');
    });
  });

  it('asks an account with no password for none', async () => {
    vi.spyOn(twoFactorApi, 'beginTwoFactor').mockResolvedValue({ ...SETUP, password_required: false });
    await mount(TwoFactorWizard);
    button(host, t('twoFactorAppHaveOne')).click();
    await settle();
    button(host, t('twoFactorContinue')).click();
    await settle();
    expect(host.querySelector('input[type="password"]')).toBeNull();
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
    button(host, t('twoFactorTurnOn')).click();
    await settle();
    expect(host.querySelector('.oa-2fa-wizard')).not.toBeNull();
  });

  it('turns it off with a code', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    const disable = vi.spyOn(twoFactorApi, 'disableTwoFactor').mockResolvedValue({ user: ACCOUNT });
    await mount(SecuritySection);

    expect(host.textContent).toContain(t('twoFactorRecoveryRowHint', { count: 8 }));
    button(host, t('twoFactorTurnOffShort')).click();
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
    expect(() => button(host, t('twoFactorTurnOffShort'))).toThrow();
    expect(() => button(host, t('twoFactorRegenerateShort'))).not.toThrow();
  });

  it('warns when the recovery codes are running out', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue({ ...ON, recovery_remaining: 2 });
    await mount(SecuritySection);
    expect(host.textContent).toContain(t('twoFactorRecoveryLow', { count: 2 }));
  });

  it('keeps freshly regenerated codes on screen until they are acknowledged', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    const regenerate = vi.spyOn(twoFactorApi, 'regenerateRecovery').mockResolvedValue({ recovery_codes: CODES });
    await mount(SecuritySection);

    button(host, t('twoFactorRegenerateShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
    expect(regenerate).toHaveBeenCalledWith('123456');
    const codesShown = () => [...host.querySelectorAll('.oa-2fa-codes li')].map((node) => node.textContent);
    expect(codesShown()).toEqual(CODES);

    // Opening the other action must not throw away codes the server no longer holds.
    button(host, t('twoFactorTurnOffShort')).click();
    await settle();
    expect(codesShown()).toEqual(CODES);

    // Finish waits for the reader to say the codes are kept.
    expect(button(host, t('twoFactorFinish')).disabled).toBe(true);
    const saved = host.querySelector<HTMLInputElement>('.oa-2fa-codes-box input[type="checkbox"]')!;
    saved.checked = true;
    saved.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(button(host, t('twoFactorFinish')).disabled).toBe(false);
    button(host, t('twoFactorFinish')).click();
    await settle();
    expect(codesShown()).toEqual([]);
  });
});

describe('leaving the setup wizard with codes unsaved', () => {
  it('asks before the page is left on the save step, and not once the codes are acknowledged', async () => {
    vi.spyOn(twoFactorApi, 'beginTwoFactor').mockResolvedValue(SETUP);
    vi.spyOn(twoFactorApi, 'enableTwoFactor')
      .mockResolvedValue({ recovery_codes: CODES, user: { ...ACCOUNT, two_factor_at: 1 } });
    await mount(TwoFactorWizard, { onDone: vi.fn() });
    button(host, t('twoFactorAppHaveOne')).click();
    await settle();
    button(host, t('twoFactorContinue')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    await settle();

    const leaving = (): boolean => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    };
    expect(leaving()).toBe(true);

    const saved = host.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    saved.checked = true;
    saved.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(leaving()).toBe(false);
  });

  it('does not ask on the earlier steps, where nothing has changed yet', async () => {
    await mount(TwoFactorWizard, { onDone: vi.fn() });
    const leaving = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(leaving);
    expect(leaving.defaultPrevented).toBe(false);
  });
});

describe('leaving with regenerated codes unsaved', () => {
  const ON: TwoFactorStatus = {
    available: true, enabled: true, enabled_at: Date.UTC(2026, 8, 1), recovery_remaining: 8,
    mandatory: false, policy: 'optional', remember_days: 0,
  };

  it('asks before the page is left while the new codes are unsaved, and not once they are acknowledged', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    vi.spyOn(twoFactorApi, 'regenerateRecovery').mockResolvedValue({ recovery_codes: CODES });
    await mount(SecuritySection);
    button(host, t('twoFactorRegenerateShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();

    const leaving = (): boolean => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    };
    expect(leaving()).toBe(true);

    const saved = host.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    saved.checked = true;
    saved.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();
    expect(leaving()).toBe(false);
  });

  it('does not ask while no codes are on screen', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    await mount(SecuritySection);
    const leaving = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(leaving);
    expect(leaving.defaultPrevented).toBe(false);
  });

  // Leaving the settings page inside the app unmounts the section, and the
  // server cannot show the codes again: a remount in the same load has to find
  // them still on screen, not lost.
  it('shows unacknowledged codes again after the settings page is left and returned to', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    vi.spyOn(twoFactorApi, 'regenerateRecovery').mockResolvedValue({ recovery_codes: CODES });
    await mount(SecuritySection);
    button(host, t('twoFactorRegenerateShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();

    unmount?.();
    unmount = undefined;
    await mount(SecuritySection);

    expect([...host.querySelectorAll('.oa-2fa-codes li')].map((node) => node.textContent)).toEqual(CODES);
  });

  it('does not show the codes to the next account signed in on the same page', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON);
    vi.spyOn(twoFactorApi, 'regenerateRecovery').mockResolvedValue({ recovery_codes: CODES });
    await mount(SecuritySection);
    button(host, t('twoFactorRegenerateShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();

    unmount?.();
    unmount = undefined;
    forget();
    adopt({ ...ACCOUNT, id: '01ARZ3NDEKTSV4RRFFQ69G5FAW', two_factor_at: 1 });
    await mount(SecuritySection);

    expect(host.querySelectorAll('.oa-2fa-codes li')).toHaveLength(0);
  });
});

describe('turning it off after a regeneration', () => {
  it('drops the codes it was showing, so they do not come back when it is switched on again', async () => {
    adopt({ ...ACCOUNT, two_factor_at: 1 });
    const ON_NOW: TwoFactorStatus = {
      available: true, enabled: true, enabled_at: Date.UTC(2026, 8, 1), recovery_remaining: 8,
      mandatory: false, policy: 'optional', remember_days: 0,
    };
    vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(ON_NOW);
    vi.spyOn(twoFactorApi, 'regenerateRecovery').mockResolvedValue({ recovery_codes: CODES });
    const disable = vi.spyOn(twoFactorApi, 'disableTwoFactor').mockResolvedValue({ user: ACCOUNT });
    await mount(SecuritySection);

    button(host, t('twoFactorRegenerateShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '123456');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
    expect(host.querySelectorAll('.oa-2fa-codes li')).toHaveLength(CODES.length);

    button(host, t('twoFactorTurnOffShort')).click();
    await settle();
    type(host.querySelector<HTMLInputElement>('input.oa-2fa-code')!, '654321');
    host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
    expect(disable).toHaveBeenCalledWith('654321');
    expect(host.querySelectorAll('.oa-2fa-codes li')).toHaveLength(0);
  });
});

describe('which destinations belong to the server', () => {
  it('sends the authorisation request to the server and nothing under the API', () => {
    expect(serverOwned('/oauth/authorize?client_id=x')).toBe(true);
    expect(serverOwned('/oauth/authorize')).toBe(true);
    expect(serverOwned('/oauth/authorizeX')).toBe(false);
    expect(serverOwned('/api/auth/me')).toBe(false);
  });
});
