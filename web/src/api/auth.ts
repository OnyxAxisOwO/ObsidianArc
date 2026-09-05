import { api } from './client';

export type Role = 'user' | 'admin';
export type AccountStatus = 'active' | 'disabled';

export interface Account {
  id: string;
  username: string;
  email: string;
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
}

export interface SiteInfo {
  name: string;
  description: string;
  registration_enabled: boolean;
  // True while the instance has no accounts at all: the first person to
  // register becomes the administrator.
  setup_required: boolean;
}

// The presentation state the server keeps for an account. Deliberately loose:
// the server stores what it is given and does not interpret most of it, so
// adding a preference is a frontend-only change.
export type Preferences = Record<string, unknown>;

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
  nickname?: string;
}

export function register(input: RegisterInput): Promise<{ user: Account }> {
  return api.post<{ user: Account }>('/api/auth/register', {
    username: input.username,
    password: input.password,
    email: input.email ?? '',
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
