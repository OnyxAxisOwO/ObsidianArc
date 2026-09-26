import { ApiError } from '@/api/client';
import { t, type StringKey } from '@/composables/useI18n';

/** Keep server wording out of the verification surface, which also runs in Chinese. */
export function verificationErrorKey(failure: unknown): StringKey {
  if (!(failure instanceof ApiError)) return 'failed';

  switch (failure.code) {
    case 'verification_invalid': return 'verifyErrorInvalid';
    case 'verification_expired': return 'verifyErrorExpired';
    case 'verification_code_expired': return 'verifyCodeExpired';
    case 'verification_code_limited': return 'verifyCodeLimited';
    case 'already_verified': return 'verifyAlreadyDone';
    case 'verification_resend_too_soon':
    case 'resend_too_soon': return 'verifyResendTooSoon';
    case 'verification_no_address': return 'verifyNoAddress';
    case 'mail_unavailable': return 'mailDeliveryUnavailable';
    default: return 'failed';
  }
}

export function verificationErrorText(failure: unknown): string {
  return t(verificationErrorKey(failure));
}
