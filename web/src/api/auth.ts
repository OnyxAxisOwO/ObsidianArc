import { api } from './client';

export type Role = 'user' | 'admin' | 'super_admin';
export type AccountStatus = 'active' | 'disabled';

export interface Account {
  id: string;
  username: string;
  email: string;
  qq: string;
  nickname: string;
  avatar: string;
  bio: string;
  role: Role;
  admin_permissions?: string[];
  group_id: string;
  group_expires_at: number;
  group_name: string;
  /** Markdown written by the operator for members of this group. */
  group_description?: string;
  /** Whether the operator configured this group to show expiry date in usage drawer. */
  group_show_expiry?: boolean;
  status: AccountStatus;
  created_at: number;
  updated_at: number;
  last_login_at: number;
  last_active_at?: number;
  // False only while an unconfirmed address is holding the account
  // back. True for everyone else, including accounts with no address.
  email_verified: boolean;
  // What this account's group permits. The server checks both again on the
  // endpoints that act; these decide only what is worth drawing.
  allow_stats: boolean;
  allow_delete_conversations: boolean;
  allow_archive_conversations?: boolean;
  /** Whether the account menu offers the terminal. Optional because an
   *  older server never sent it, and absent must not read as refused. */
  allow_terminal?: boolean;
  /** A user-level brake over the group's API permission. A zero expiry means
   *  the restriction remains until an administrator lifts it. */
  api_restricted: boolean;
  api_restricted_until: number;
  api_restriction_source: string;
  /** Where the account registered from. Administrators only; '' where it
   *  could not be resolved, and on accounts created before it was recorded. */
  signup_ip?: string;
  /** The client reported when this account was registered. Empty on accounts
   *  created before it was recorded. */
  signup_user_agent?: string;
  /** When two-step verification was turned on; zero while it is off. */
  two_factor_at?: number;
  /** The operator's two-step policy, answered for this account: must it
   *  enrol before anything else works, may it turn the step off, and does the
   *  backoffice refuse it until it enrols. The server holds the same lines;
   *  these only decide what to draw. Optional because an older server never
   *  sent them, and absent must read as "nothing required". */
  two_factor_enrol?: boolean;
  two_factor_mandatory?: boolean;
  two_factor_backoffice?: boolean;
  /** Whether the backoffice asks this account for a code of its own at the
   *  door, and how often: every visit, after idling, or on a schedule. Empty
   *  when it does not. The minutes are that mode's clock; locked is whether
   *  the request this came with would have been turned away for want of one. */
  two_factor_backoffice_verify?: '' | 'visit' | 'idle' | 'interval';
  two_factor_backoffice_minutes?: number;
  two_factor_backoffice_locked?: boolean;
}

// What a visitor with no account is shown at the address. The server settles
// this — an instance still being set up always gets the sign-in card, whatever
// is configured — so the client only has to draw what it is told.
export interface Landing {
  /** 'site' is the product's own front page, which no operator has to write. */
  mode: 'login' | 'intro' | 'chat' | 'site';
  /** HTML the operator wrote. Only meaningful in 'intro' mode. */
  intro: string;
  /** Whether a visitor may actually send a message in 'chat' mode. */
  trial: boolean;
  trial_turns: number;
}

export interface SiteInfo {
  name: string;
  description: string;
  /** Already resolved against `name` server-side (settings.Service.BrowserTitle):
   *  empty only on a server old enough to predate the setting. */
  browser_title?: string;
  registration_enabled: boolean;
  health_show_users?: boolean;
  /** Whether readers may open the leaderboard. Absent on an older server. */
  leaderboard_show_users?: boolean;
  allow_archive_conversations?: boolean;
  // True while the instance has no accounts at all: the first person to
  // register becomes the administrator.
  setup_required: boolean;
  // The instance's email policy, so the sign-up form can say what is
  // acceptable before it is submitted. Both are false/empty while the
  // instance still has no accounts.
  require_email?: boolean;
  email_domains?: string[];
  require_qq?: boolean;
  qq_requirement?: 'off' | 'optional' | 'required';
  /** Served only where a challenge is actually switched on. */
  turnstile_site_key?: string;
  turnstile_on_login?: boolean;
  turnstile_on_signup?: boolean;
  turnstile_on_api_key?: boolean;
  turnstile_on_feedback?: boolean;
  turnstile_on_redeem?: boolean;
  turnstile_on_chat_speed?: boolean;
  /** How long a browser may skip the sign-in code after one is entered; zero
   *  means the code step offers no such choice. */
  two_factor_remember_days?: number;
  /** Whether a model reads each sign-up, so the button can say it is happening. */
  signup_review?: boolean;
  /**
   * The sign-ins that do not start with a password here. Served only for
   * providers the operator has both configured and switched on, so an empty
   * list means the card draws no divider and no buttons.
   */
  oauth?: { id: string; name: string }[];
  // Whether a new account has to confirm its address before it can
  // send anything. False whenever the server cannot post mail,
  // whatever the setting says.
  verify_email?: boolean;
  // Absent on a server older than the landing-page setting; the fallback in
  // session.ts supplies the behaviour that server had.
  landing?: Landing;
  // The About panel as the operator wrote it. Either field may be empty, which
  // means "use the built-in wording" rather than "render nothing".
  about?: { title: string; body: string };
  // The standing notice above the chat. Not an announcement: no read state,
  // no date, and it stays until an operator clears it.
  home_notice?: { text: string; dismissible: boolean };
  /** Derived from registration.enabled + invites.required: whether signing up
   *  needs no code, needs one, or is off altogether. Absent reads as 'open',
   *  the behaviour a server without the setting always had. */
  invite_mode?: 'open' | 'invite' | 'closed';
  /** Whether an account gets a personal invite code (invites.user_enabled),
   *  so the sign-up form knows an admin-issued code is not the only kind. */
  user_invites?: boolean;
  /** Background image URLs per variant (landscape_light, landscape_dark, portrait_light, portrait_dark). */
  login_background?: Record<string, string>;
  /** Custom site logo URL, or empty if the built-in mark/favicon is used. */
  logo_url?: string;
}

// The presentation state the server keeps for an account. Deliberately loose:
// the server stores what it is given and does not interpret most of it, so
// adding a preference is a frontend-only change.
export type Preferences = Record<string, unknown>;

export function verifyEmail(token: string): Promise<void> {
  return api.post<void>('/api/auth/verify', { token });
}

export function resendVerification(): Promise<void> {
  return api.post<void>('/api/profile/verify/resend', {});
}

export function fetchSite(): Promise<SiteInfo> {
  return api.get<SiteInfo>('/api/site');
}

export function fetchMe(): Promise<{ user: Account; preferences: Preferences }> {
  return api.get<{ user: Account; preferences: Preferences }>('/api/auth/me');
}

/**
 * Either the account, or — when it has two-step verification — word that the
 * password was right and a code is wanted. In the second case the server has
 * set a cookie that opens nothing but completeSignIn.
 */
export interface LoginResult {
  user?: Account;
  two_factor?: true;
}

export function login(identifier: string, password: string, turnstile?: string): Promise<LoginResult> {
  return api.post<LoginResult>('/api/auth/login', {
    identifier,
    password,
    ...(turnstile ? { turnstile } : {}),
  });
}

/** The second step: a code from the app, or a recovery code. */
export function completeSignIn(code: string, remember: boolean): Promise<{ user: Account }> {
  return api.post<{ user: Account }>('/api/auth/two-factor', { code, remember });
}

export interface RegisterInput {
  username: string;
  password: string;
  email?: string;
  qq?: string;
  nickname?: string;
  turnstile?: string;
  inviteCode?: string;
}

export function register(input: RegisterInput): Promise<{ user: Account }> {
  return api.post<{ user: Account }>('/api/auth/register', {
    username: input.username,
    password: input.password,
    email: input.email ?? '',
    qq: input.qq ?? '',
    nickname: input.nickname ?? '',
    turnstile: input.turnstile ?? '',
    invite_code: input.inviteCode ?? '',
  });
}

export function logout(): Promise<void> {
  return api.post<void>('/api/auth/logout');
}

export interface ProfilePatch {
  nickname?: string;
  avatar?: string;
  bio?: string;
  email?: string;
  qq?: string;
}

export function updateProfile(patch: ProfilePatch): Promise<{ user: Account }> {
  return api.patch<{ user: Account }>('/api/profile', patch);
}

export function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  return api.post<void>('/api/profile/password', {
    current_password: currentPassword,
    new_password: newPassword,
  });
}

export function savePreferences(patch: Preferences): Promise<{ preferences: Preferences }> {
  return api.patch<{ preferences: Preferences }>('/api/preferences', patch);
}
