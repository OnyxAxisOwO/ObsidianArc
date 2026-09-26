import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';

/**
 * Why a registration was refused, in the reader's own language.
 *
 * One place, because there are two screens that open accounts now — the
 * sign-up form and the step a provider sign-in stops at when this server
 * wants something the provider could not supply — and they are refused by the
 * same endpoint's rules for the same reasons. Two copies would drift, and the
 * half that drifts is the one nobody tests: the wording of a failure.
 *
 * `domains` is what the instance accepts, for the case where the server
 * refused an address without naming them.
 */
export function refusalText(failure: unknown, domains: string[] = []): string {
  if (!(failure instanceof ApiError)) return String(failure);

  switch (failure.code) {
    case 'account_banned':
      return t('accountBanned');
    case 'signup_ip_blocked':
      return t('signupBlocked');
    case 'registration_closed':
      return t('registrationClosed');
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
      return failure.message;
    }
  }
}

function allowed(failure: ApiError, fallback: string[]): string[] {
  const named = failure.details['allowed_domains'];
  if (Array.isArray(named) && named.length) return named.map(String);
  return fallback;
}
