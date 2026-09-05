// The administrative API, typed.
//
// Kept apart from the rest of the client so the shape of what an
// administrator can do is one file. Note what is not here: no provider API
// key, in either direction beyond writing a new one. The server never sends
// one back, and there is no field on these types that could carry it.

import { api } from '../api/client';
import type { Account, Role, AccountStatus } from '../api/auth';
import type { Conversation, Message } from '../api/chat';

export type ProviderKind = 'openai' | 'anthropic';
export type ReasoningStyle = 'auto' | 'none' | 'anthropic' | 'openai_effort' | 'openrouter' | 'qwen';

export interface Provider {
  id: string;
  name: string;
  kind: ProviderKind;
  base_url: string;
  api_key_hint: string;
  headers: Record<string, string>;
  anthropic_version: string;
  reasoning_style: ReasoningStyle;
  timeout_seconds: number;
  enabled: boolean;
  sort_order: number;
  model_count: number;
  created_at: number;
  updated_at: number;
}

export interface GroupModelGrant {
  model_id: string;
  access: 'use' | 'view';
}

export interface ModelGroupGrant {
  group_id: string;
  access: 'use' | 'view';
}

export interface AdminModel {
  id: string;
  provider_id: string;
  provider_name: string;
  provider_kind: ProviderKind;
  model_id: string;
  display_name: string;
  description: string;
  avatar: string;
  enabled: boolean;
  hidden: boolean;
  sort_order: number;

  // Where a request for this model actually goes, and which reasoning flag it
  // wants. Administrative only: the model list a user is served carries
  // neither, so a route leaves no trace anywhere they can see.
  route_to_id: string;
  reasoning_style: ReasoningStyle | '';

  group_grants?: ModelGroupGrant[];

  supports_reasoning: boolean;
  supports_images: boolean;
  supports_vision: boolean;
  supports_streaming: boolean;
  supports_system_prompt: boolean;
  supports_tools: boolean;
  context_window: number;
  max_output_tokens: number;

  request_weight: number;
  input_token_weight: number;
  output_token_weight: number;
  reasoning_token_weight: number;
}

export interface Group {
  id: string;
  name: string;
  description: string;
  is_default: boolean;
  allow_all_models: boolean;
  api_access: boolean;
  sort_order: number;
  members: number;
  model_ids: string[];
  model_grants?: GroupModelGrant[];
  created_at: number;
  updated_at: number;
}

export type QuotaWindowKind = '5h' | '1w' | '1m';

export interface QuotaLimits {
  enabled: boolean | null;
  requests: number | null;
  tokens: number | null;
  credits: number | null;
}

export interface QuotaPolicy {
  id: string;
  scope: 'global' | 'group' | 'user';
  scope_id: string;
  rpm: number | null;
  tpm: number | null;
  windows: Record<QuotaWindowKind, QuotaLimits>;
  updated_at: number;
}

export interface UsageTotals {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  errors: number;
}

export interface UsageBreakdown extends UsageTotals {
  key: string;
  label: string;
}

export interface UsagePoint extends UsageTotals {
  at: number;
}

export interface UsageRecord {
  id: string;
  user_id: string;
  username: string;
  model_name: string;
  provider_name: string;
  conversation_id: string;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  status: 'ok' | 'error' | 'aborted' | 'rejected';
  error_code: string;
  started_at: number;
  duration_ms: number;
}

export interface Dashboard {
  counts: {
    users: number;
    active_users: number;
    providers: number;
    enabled_providers: number;
    models: number;
    enabled_models: number;
  };
  newest_users: Account[];
  last_24h: UsageTotals;
  last_7d: UsageTotals;
  top_models: UsageBreakdown[];
  series: UsagePoint[];
  bucket_ms: number;
  recent: UsageRecord[];
}

export interface Meta {
  provider_kinds: ProviderKind[];
  reasoning_styles: ReasoningStyle[];
}

// The reader's client already describes this shape, and one row of JSON
// should not have two declarations that can drift apart.
import type { Announcement, DisplayMode } from '../api/announcements';

export type { Announcement, DisplayMode };

// --- reads ---------------------------------------------------------------------

export const adminApi = {
  dashboard: () => api.get<Dashboard>('/api/admin/dashboard'),
  meta: () => api.get<Meta>('/api/admin/meta'),

  users: (query: string) => api.get<{ users: Account[]; total: number }>(`/api/admin/users${query}`),
  user: (id: string) =>
    api.get<{
      user: Account;
      usage: { unlimited: boolean; windows: unknown[] };
      lifetime: UsageTotals;
      policy: QuotaPolicy;
    }>(`/api/admin/users/${id}`),
  updateUser: (id: string, patch: Record<string, unknown>) =>
    api.patch<{ user: Account }>(`/api/admin/users/${id}`, patch),
  deleteUser: (id: string) => api.delete<void>(`/api/admin/users/${id}`),
  resetPassword: (id: string, newPassword: string) =>
    api.post<void>(`/api/admin/users/${id}/password`, { new_password: newPassword }),
  userConversations: (id: string) =>
    api.get<{ conversations: Conversation[] }>(`/api/admin/users/${id}/conversations`),
  userTranscript: (id: string, conversationID: string) =>
    api.get<{ conversation: Conversation; messages: Message[] }>(
      `/api/admin/users/${id}/conversations/${conversationID}`,
    ),

  groups: () => api.get<{ groups: Group[]; policies: QuotaPolicy[] }>('/api/admin/groups'),
  createGroup: (body: Record<string, unknown>) => api.post<{ group: Group }>('/api/admin/groups', body),
  updateGroup: (id: string, body: Record<string, unknown>) =>
    api.patch<{ group: Group }>(`/api/admin/groups/${id}`, body),
  deleteGroup: (id: string) => api.delete<{ moved_to: string }>(`/api/admin/groups/${id}`),

  providers: () => api.get<{ providers: Provider[] }>('/api/admin/providers'),
  createProvider: (body: Record<string, unknown>) =>
    api.post<{ provider: Provider }>('/api/admin/providers', body),
  updateProvider: (id: string, body: Record<string, unknown>) =>
    api.patch<{ provider: Provider }>(`/api/admin/providers/${id}`, body),
  deleteProvider: (id: string) => api.delete<void>(`/api/admin/providers/${id}`),
  detect: (id: string) =>
    api.post<{ models: Array<{ model_id: string; display_name: string; configured: boolean }> }>(
      `/api/admin/providers/${id}/detect`,
    ),

  models: (providerID?: string) =>
    api.get<{ models: AdminModel[] }>(
      `/api/admin/models${providerID ? `?provider_id=${providerID}` : ''}`,
    ),
  createModel: (body: Record<string, unknown>) => api.post<{ model: AdminModel }>('/api/admin/models', body),
  updateModel: (id: string, body: Record<string, unknown>) =>
    api.patch<{ model: AdminModel }>(`/api/admin/models/${id}`, body),
  deleteModel: (id: string) => api.delete<void>(`/api/admin/models/${id}`),

  usage: (query: string) =>
    api.get<{
      totals: UsageTotals;
      by_model: UsageBreakdown[];
      by_provider: UsageBreakdown[];
      series: UsagePoint[];
      bucket_ms: number;
    }>(`/api/admin/usage${query}`),
  usageRecords: (query: string) =>
    api.get<{ records: UsageRecord[]; total: number }>(`/api/admin/usage/records${query}`),

  policies: () => api.get<{ policies: QuotaPolicy[] }>('/api/admin/quota/policies'),
  savePolicy: (policy: Record<string, unknown>) =>
    api.put<{ policy: QuotaPolicy }>('/api/admin/quota/policies', policy),
  deletePolicy: (scope: string, scopeID: string) =>
    api.delete<void>(`/api/admin/quota/policies/${scope}?scope_id=${encodeURIComponent(scopeID)}`),

  settings: () =>
    api.get<{ settings: Record<string, string>; groups: Group[]; mail_configured: boolean }>(
      '/api/admin/settings',
    ),
  saveSettings: (values: Record<string, string>) =>
    api.put<{ settings: Record<string, string> }>('/api/admin/settings', values),

  announcements: () => api.get<{ announcements: Announcement[] }>('/api/admin/announcements'),
  createAnnouncement: (body: Record<string, unknown>) =>
    api.post<{ announcement: Announcement }>('/api/admin/announcements', body),
  updateAnnouncement: (id: string, body: Record<string, unknown>) =>
    api.patch<{ announcement: Announcement }>(`/api/admin/announcements/${id}`, body),
  deleteAnnouncement: (id: string) => api.delete<void>(`/api/admin/announcements/${id}`),
};

export type { Account, Role, AccountStatus, Conversation, Message };

/** The empty policy an editor starts from when a scope has no row yet. */
export function emptyPolicy(scope: QuotaPolicy['scope'], scopeID: string): QuotaPolicy {
  const blank: QuotaLimits = { enabled: null, requests: null, tokens: null, credits: null };
  return {
    id: '',
    scope,
    scope_id: scopeID,
    rpm: null,
    tpm: null,
    windows: { '5h': { ...blank }, '1w': { ...blank }, '1m': { ...blank } },
    updated_at: 0,
  };
}
