// The strip that tells someone their address is still unconfirmed.
//
// It sits above the chat rather than replacing it: an account in this state
// can sign in, read, and change its address — it just cannot spend anything
// until the link is opened. Locking it out of the interface entirely would
// leave nowhere to press resend from.

import { ApiError } from '../api/client';
import { resendVerification } from '../api/auth';
import { t } from '../i18n';
import { currentUser, siteInfo } from '../session';
import { button, el } from '../ui/dom';

/**
 * Returns the banner, or null when there is nothing to say — which is the
 * common case, so callers append the result conditionally rather than
 * hiding an empty strip.
 */
export function createVerifyBanner(): HTMLElement | null {
  const account = currentUser();
  if (!account || account.email_verified) return null;
  // The server decides whether verification is in force. Without mail it is
  // inert, and an account left unverified from a period when it was on should
  // not be nagged about something it can no longer do.
  if (!(siteInfo().verify_email ?? false)) return null;

  const wrap = el('div', 'oa-verify-banner');

  const text = el('div', 'oa-verify-text');
  text.appendChild(el('strong', null, t('verifyBannerTitle')));
  text.appendChild(el('span', null, account.email
    ? t('verifyBannerBody', { email: account.email })
    : t('verifyBannerNoAddress')));
  wrap.appendChild(text);

  if (!account.email) return wrap;

  const resend = button('oa-btn', t('verifyResend'), () => {
    resend.disabled = true;
    void resendVerification()
      .then(() => {
        resend.textContent = t('verifyResendSent');
      })
      .catch((error: unknown) => {
        // Including the throttle, which is a real answer rather than a
        // failure: a link went out recently and is worth looking for.
        resend.textContent = error instanceof ApiError ? error.message : t('failed');
      });
  });
  wrap.appendChild(resend);
  return wrap;
}
