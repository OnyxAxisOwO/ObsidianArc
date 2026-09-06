import { api } from './client';

export type Role = 'user' | 'admin';
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
  group_id: string;
  group_name: string;
  status: AccountStatus;
  created_at: number;
  updated_at: number;
  last_login_at: number;
  // False only while an unconfirmed address is holding the account
  // back. True for everyone else, including accounts with no address.
  email_verified: boolean;
  // What this account's group permits. The server checks both again on the
  // endpoints that act; these decide only what is worth drawing.
  allow_stats: boolean;
  allow_delete_conversations: boolean;
}

// What a visitor with no account is shown at the address. The server settles
// this — an instance still being set up always gets the sign-in card, whatever
// is configured — so the client only has to draw what it is told.
export interface Landing {
  mode: 'login' | 'intro' | 'chat';
  /** HTML the operator wrote. Only meaningful in 'intro' mode. */
  intro: string;
  /** Whether a visitor may actually send a message in 'chat' mode. */
  trial: boolean;
  trial_turns: number;
}

export interface SiteInfo {
  name: string;
  description: string;
  registration_enabled: boolean;
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

export function login(identifier: string, password: string): Promise<{ user: Account }> {
  return api.post<{ user: Account }>('/api/auth/login', { identifier, password });
}

export interface RegisterInput {
  username: string;
  password: string;
  email?: string;
  qq?: string;
  nickname?: string;
}

export function register(input: RegisterInput): Promise<{ user: Account }> {
  return api.post<{ user: Account }>('/api/auth/register', {
    username: input.username,
    password: input.password,
    email: input.email ?? '',
    qq: input.qq ?? '',
    nickname: input.nickname ?? '',
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
