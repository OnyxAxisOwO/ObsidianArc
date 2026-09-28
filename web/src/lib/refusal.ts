import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';

/**
 * Why an authentication request was refused, in the reader's own language.
 *
 * Password sign-in, its second factor, and provider sign-up share these
 * responses. Keeping their wording here means the server's English strings
 * never have to be shown as the only explanation on a translated screen.
 *
 * `domains` is what the instance accepts, for the case where the server
 * refused an address without naming them.
 */
export function refusalText(failure: unknown, domains: string[] = []): string {
  if (!(failure instanceof ApiError)) return t('authRequestFailed');

  if (failure.details['code_detail'] === 'invalid_credentials' || failure.code === 'invalid_credentials') {
    return t('invalidCredentials');
  }

  switch (failure.code) {
    case 'network':
      return t('connectionFailed');
    case 'account_banned':
      return t('accountBanned');
    case 'signup_ip_blocked':
      return t('signupBlocked');
    case 'registration_closed':
      return t('registrationClosed');
    case 'third_party_only_registration':
    case 'oidc_only_registration':
      return t('oauthThirdPartyOnly');
    case 'username_taken':
      return t('usernameTaken');
    case 'invalid_username':
      return t('usernameInvalid');
    case 'signup_closed':
      return t('oauthSignupClosed');
    case 'address_taken':
      return t('oauthAddressTaken');
    case 'signup_refused': {
      // The operator's own words when they wrote any — a way to appeal is the
      // whole reason to write them — and a plain sentence otherwise.
      const notice = failure.details['notice'];
      return typeof notice === 'string' && notice.trim() ? notice : t('signupRefused');
    }
    case 'disposable_email':
      return t('disposableEmailRejected');
    case 'email_screening_unavailable':
      return t('emailScreeningUnavailable');
    case 'challenge_failed':
      return t('challengeFailed');
    case 'challenge_unavailable':
      return t('challengeUnavailable');
    case 'pow_required':
      return t('powRequired');
    case 'pow_expired':
      return t('powExpired');
    case 'pow_invalid_signature':
    case 'pow_max_exceeded':
    case 'pow_invalid_nonce':
    case 'pow_replayed':
      return t('powVerificationFailed');
    case 'pow_rate_limited':
      return t('powRateLimited');
    case 'qq_required':
      return t('qqRequiredHere');
    case 'invalid_qq':
      return t('qqInvalid');
    case 'qq_taken':
      return t('qqTaken');
    case 'email_required':
      return t('emailRequiredHere');
    case 'email_domain':
      return t('emailDomainRejected', { domains: allowed(failure, domains).join(', ') });
    case 'invite_required':
      return t('inviteRequiredHere');
    case 'invite_invalid':
      return t('inviteInvalid');
    case 'signups_throttled':
      return t('signupsThrottled', { count: Number(failure.details['retry_after_seconds'] ?? 60) });
    case 'too_many_attempts':
      return t('tooManyAttempts', { count: Number(failure.details['retry_after_seconds'] ?? 60) });
    case 'two_factor_code':
      return t('twoFactorCodeWrong');
    case 'two_factor_expired':
      return t('twoFactorExpired');
    default: {
      const named = allowed(failure, []);
      if (named.length) return t('emailDomainRejected', { domains: named.join(', ') });
      // A server that refused an address without saying which are acceptable,
      // on an instance the client knows the list for.
      if (domains.length && failure.status === 400 && /email/i.test(failure.message)) {
        return t('emailDomainRejected', { domains: domains.join(', ') });
      }
      if (failure.status === 0) return t('connectionFailed');
      if (failure.status >= 500) return t('authServiceFailed');
      return t('authRequestFailed');
    }
  }
}

/** Keep unknown accounts indistinguishable from wrong passwords at sign-in. */
export function loginRefusalText(failure: unknown): string {
  if (failure instanceof ApiError && failure.status === 401) return t('invalidCredentials');
  return refusalText(failure);
}

function allowed(failure: ApiError, fallback: string[]): string[] {
  const named = failure.details['allowed_domains'];
  if (Array.isArray(named) && named.length) return named.map(String);
  return fallback;
}
