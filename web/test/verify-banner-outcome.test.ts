// The banner is removed the moment the refreshed account is verified, so a
// success line drawn "on the way out" is never seen. What the reader does see
// is the case where the code was accepted but the account could not be
// refreshed: the banner stays up, and must then say a reload is needed rather
// than sit beside a success line.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import * as authApi from '@/api/auth';
import VerifyBanner from '@/announce/VerifyBanner.vue';
import { changeLanguage, t } from '@/composables/useI18n';
import { adopt, currentUser, forget, site, siteInfo } from '@/stores/session';
import { settle } from './helpers';
import { makeAccount } from './fixtures/account';

const ACCOUNT = makeAccount({ id: 'u1', last_login_at: 1, email_verified: false });

let app: App | null = null;
let host: HTMLElement;


async function submitCode(code: string): Promise<void> {
  app = createApp({ render: () => h(VerifyBanner) });
  app.mount(host);
  await settle();
  const input = host.querySelector<HTMLInputElement>('.oa-verify-code input')!;
  input.value = code;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  host.querySelector<HTMLButtonElement>('.oa-verify-code button[type="submit"]')!.click();
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.appendChild(host);
  site.value = { ...siteInfo.value, verify_email: true };
  adopt(ACCOUNT);
});

afterEach(() => {
  app?.unmount();
  app = null;
  vi.restoreAllMocks();
  forget();
  site.value = null;
});

describe('verification banner outcome', () => {
  it('discards a verified account response after the session changes', async () => {
    vi.spyOn(authApi, 'verifyEmailCode').mockResolvedValue();
    let finish!: (result: Awaited<ReturnType<typeof authApi.fetchMe>>) => void;
    vi.spyOn(authApi, 'fetchMe').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    await submitCode('123456');
    expect(authApi.fetchMe).toHaveBeenCalled();
    app!.unmount();
    app = null;
    forget();
    adopt(makeAccount({ id: 'next' }));
    finish({ user: ACCOUNT, preferences: {} });
    await settle();
    expect(currentUser.value?.id).toBe('next');
  });

  it('says a reload is needed when the code is accepted but the account cannot be refreshed', async () => {
    vi.spyOn(authApi, 'verifyEmailCode').mockResolvedValue();
    vi.spyOn(authApi, 'fetchMe').mockRejectedValue(new Error('offline'));

    await submitCode('123456');

    const status = host.querySelector('[role="status"]')?.textContent;
    expect(status).toBe(t('verifyCodeAcceptedReload'));
    // The banner still reads the unconfirmed account, so it stays up with the line.
    expect(host.querySelector('.oa-verify-banner')).not.toBeNull();
  });
});
