import { api } from './client';

// Signing in with an account somebody already holds at GitHub or Google.
//
// The sign-in itself is not a request from here. It is a navigation: the
// browser leaves for the provider and comes back to a callback that sets the
// session cookie, so there is nothing to fetch and nothing to await. What
// this module has is the address to leave for, and the two calls the settings
// screen makes about connections that already exist.

/** One provider account linked to this account. */
export interface OAuthConnection {
  provider: string;
  /** What the provider calls the person, as of their last sign-in. */
  login: string;
  email: string;
  created_at: number;
  last_login_at: number;
}

/** A provider this build knows, and whether this server offers it. */
export interface OAuthProvider {
  id: string;
  name: string;
  enabled: boolean;
}

export interface OAuthConnections {
  connections: OAuthConnection[];
  providers: OAuthProvider[];
  /**
   * Whether this account also has a password. It decides two things on the
   * screen: whether the password box says "set" or "change", and whether the
   * last connection may be removed.
   */
  has_password: boolean;
}

/**
 * Where the button goes.
 *
 * A URL rather than a fetch: the response is a redirect to somebody else's
 * site, which is a navigation the browser has to make itself. `link` says
 * this is an account adding a connection rather than a visitor signing in;
 * `next` is where to land afterwards, and the server keeps it to a path of
 * this site whatever is passed.
 */
export function signInURL(provider: string, options: { link?: boolean; next?: string } = {}): string {
  const query = new URLSearchParams();
  if (options.link) query.set('link', '1');
  if (options.next) query.set('next', options.next);
  const suffix = query.toString();
  return `/api/auth/oauth/start/${encodeURIComponent(provider)}${suffix ? `?${suffix}` : ''}`;
}

/**
 * A sign-in that stopped to ask something.
 *
 * The provider has said who this is; what is missing is a QQ number or an
 * address it could not supply. Nothing exists on the server yet — the identity
 * is held in a signed cookie, and the account is opened by completeSignup.
 */
export interface PendingSignup {
  provider: string;
  provider_name: string;
  /** What the provider calls them, so the form can say whose sign-in this is. */
  login: string;
  /**
   * A QQ number the provider has already answered with — an IdP whose subject
   * is one, verified before it vouched. Prefilled, still confirmed by hand.
   */
  qq?: string;
  /** Only ever an address the provider proved. Empty otherwise. */
  email: string;
  needs: { qq: boolean; email: boolean };
  email_domains: string[];
  /** Whether a typed address will be sent a confirmation link. */
  verify_email: boolean;
}

export function fetchPendingSignup(): Promise<PendingSignup> {
  return api.get<PendingSignup>('/api/auth/oauth/signup');
}

/** Opens the account, and answers with where to go next. */
export function completeSignup(
  details: { qq?: string; email?: string; inviteCode?: string },
): Promise<{ redirect: string }> {
  return api.post<{ redirect: string }>('/api/auth/oauth/signup', {
    qq: details.qq ?? '',
    email: details.email ?? '',
    invite_code: details.inviteCode ?? '',
  });
}

export function fetchConnections(): Promise<OAuthConnections> {
  return api.get<OAuthConnections>('/api/auth/oauth/connections');
}

export function disconnectProvider(provider: string): Promise<void> {
  return api.delete<void>(`/api/auth/oauth/connections/${encodeURIComponent(provider)}`);
}
