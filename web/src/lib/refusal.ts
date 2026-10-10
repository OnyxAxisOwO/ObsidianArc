import { ApiError } from '@/api/client';
import { t, type StringKey } from '@/composables/useI18n';
import { pluginRefusal } from '@/plugins/registry';

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
    case 'account_banned': {
      const reason = failure.details['ban_reason'];
      if (typeof reason === 'string' && reason.trim()) {
        return t('accountBannedWithReason', { reason: reason.trim() });
      }
      return t('accountBanned');
    }
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
      // A plugin's own codes, and its fields' <key>_taken and friends.
      const plugin = pluginRefusal(failure.code);
      if (plugin) return plugin;
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

/**
 * Why a settings save was refused. The server names the setting whose address
 * it will not keep in plain http, so the line can say which field to change;
 * anything else is worded as refusalText words it. Kept beside refusalText so
 * the screen has one place to look for what a refusal means.
 */
export function settingsRefusalText(failure: ApiError): string {
  if (failure.code === 'oidc_url_not_https') {
    const field = oidcURLField(String(failure.details['setting'] ?? ''));
    if (field) return t('oidcURLNotHTTPS', { field: t(field) });
  }
  return refusalText(failure);
}

function oidcURLField(setting: string): StringKey | undefined {
  switch (setting) {
    case 'oauth.oidc_issuer':
      return 'oauthOIDCIssuer';
    case 'oauth.oidc_auth_url':
      return 'oauthOIDCAuthURL';
    case 'oauth.oidc_token_url':
      return 'oauthOIDCTokenURL';
    case 'oauth.oidc_userinfo_url':
      return 'oauthOIDCUserInfoURL';
    default:
      return undefined;
  }
}

function allowed(failure: ApiError, fallback: string[]): string[] {
  const named = failure.details['allowed_domains'];
  if (Array.isArray(named) && named.length) return named.map(String);
  return fallback;
}
