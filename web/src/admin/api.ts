// The administrative API, typed.
//
// Kept apart from the rest of the client so the shape of what an
// administrator can do is one file. Note what is not here: no provider API
// key, in either direction beyond writing a new one. The server never sends
// one back, and there is no field on these types that could carry it.

import { api } from '../api/client';
import type { ApiKey } from '../api/keys';
import type { UsageSummary } from '../api/usage';

// Re-exported so an admin screen imports one module, the way every other
// shape on this surface already does.
export type { ApiKey };
import type { Account, Role, AccountStatus } from '../api/auth';
import type { Conversation, Message } from '../api/chat';

export type ProviderKind = 'openai' | 'anthropic';
export type ReasoningStyle = 'auto' | 'none' | 'anthropic' | 'openai_effort' | 'openrouter' | 'qwen';

export interface Provider {
  id: string;
  name: string;
  kind: ProviderKind;
  base_url: string;
  allow_insecure: boolean;
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

/** One named amount of thinking a model offers. See ReasoningTier in Go. */
export interface ReasoningTier {
  /** Sent to the endpoint as reasoning_effort, and remembered by the account. */
  id: string;
  /** Shown to the reader as written: administrator's words, not the dictionary's. */
  name: string;
  /** Anthropic thinking tokens. Zero derives it from the id. */
  budget: number;
}

export interface RedemptionCode {
  id: string;
  code: string;
  cards: number;
  claimed: number;
  card_days: number;
  expires_at: number;
  note: string;
  created_at: number;
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
  api_name: string;
  system_prompt: string;
  auto_disabled: boolean;
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
  /** Empty means the three the client has built in. */
  reasoning_tiers: ReasoningTier[];

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
  allow_stats: boolean;
  allow_delete_conversations: boolean;
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
  top_users: UsageBreakdown[];
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

/** What the retention policy is currently holding on to. */
export interface HeldAttachments {
  held: number;
  bytes: number;
}

/** One answered request, as the log recorded it. */
export interface LogEntry {
  id: string;
  at: number;
  method: string;
  path: string;
  status: number;
  duration_ms: number;
  bytes: number;
  user_id?: string;
  username?: string;
  channel?: string;
  ip?: string;
  user_agent?: string;
  request_id?: string;
  model_id?: string;
  model_name?: string;
  error_code?: string;
}

export interface LogOption {
  value: string;
  label: string;
  count: number;
}

/** The values actually present in the log, so the filters offer what exists. */
export interface LogFacets {
  users: LogOption[];
  models: LogOption[];
  error_codes: LogOption[];
  statuses: LogOption[];
  total: number;
  /** Entries lost to a full buffer since boot: a gap the screen admits to. */
  dropped: number;
  oldest: number;
}

/** How a breakdown is ranked. Three defensible answers to "the most". */
export type UsageMetric = 'requests' | 'tokens' | 'credits';

export interface UserStorage {
  user_id: string;
  name: string;
  count: number;
  bytes: number;
}

export interface Resources {
  storage: {
    held_bytes: number;
    held_count: number;
    discarded_count: number;
    by_user: UserStorage[];
  };
  memory: {
    heap_bytes: number;
    heap_sys_bytes: number;
    sys_bytes: number;
    gc_count: number;
    gc_pause_ms: number;
    goroutines: number;
  };
  // percent, window_sec and process_sec are absent where the platform has no
  // answer, and percent is absent on the first read of a process: a rate
  // needs two samples and there has only been one.
  cpu: {
    cores: number;
    gomaxprocs: number;
    process_sec?: number;
    percent?: number;
    window_sec?: number;
  };
  sampled_at: number;
}

/** One model's liveness, as the backoffice reads it. */
export interface ModelHealth {
  model_id: string;
  name: string;
  provider: string;
  enabled: boolean;
  /** True when the system turned it off, which is the only kind it turns on. */
  auto_disabled: boolean;
  status: {
    state: 'up' | 'down' | 'unknown';
    uptime: number;
    samples: number;
    user_samples: number;
    system_samples: number;
    failures_in_a_row: number;
    last_ok_at: number;
    last_error_at: number;
    last_code: string;
    last_message: string;
    errors: Array<{ code: string; message: string; count: number; last_at: number }>;
  };
}

/** What one account is holding in reset cards. */
export interface CardHolding {
  available: number;
  used: number;
  expired: number;
  total: number;
  /** The unused, unexpired ones, soonest to expire first. */
  cards: Array<{ id: string; source: string; expires_at: number; created_at: number }>;
}

export const adminApi = {
  dashboard: (metric: UsageMetric = 'credits') =>
    api.get<Dashboard>(`/api/admin/dashboard?metric=${metric}`),
  meta: () => api.get<Meta>('/api/admin/meta'),
  tryReview: (body: Record<string, unknown>) =>
    api.post<{ ran: boolean; allow: boolean; reason: string }>(
      '/api/admin/security/review', body),
  health: (hours = 24) =>
    api.get<{
      hours: number;
      models: ModelHealth[];
      policy: { probe: boolean; window_mins: number; disable_after: number };
    }>(`/api/admin/health?hours=${hours}`),
  resources: () => api.get<Resources>('/api/admin/resources'),

  users: (query: string) => api.get<{ users: Account[]; total: number }>(`/api/admin/users${query}`),
  user: (id: string) =>
    api.get<{
      user: Account;
      usage: UsageSummary;
      lifetime: UsageTotals;
      policy: QuotaPolicy;
      cards: CardHolding;
    }>(`/api/admin/users/${id}`),
  updateUser: (id: string, patch: Record<string, unknown>) =>
    api.patch<{ user: Account }>(`/api/admin/users/${id}`, patch),
  deleteUser: (id: string) => api.delete<void>(`/api/admin/users/${id}`),
  resetPassword: (id: string, newPassword: string) =>
    api.post<void>(`/api/admin/users/${id}/password`, { new_password: newPassword }),
  // Only ever the record, never the token: the server keeps a digest, so
  // there is nothing an administrator could be shown even in principle.
  userKeys: (id: string) => api.get<{ keys: ApiKey[] }>(`/api/admin/users/${id}/keys`),
  revokeUserKey: (id: string, keyID: string) =>
    api.delete<void>(`/api/admin/users/${id}/keys/${keyID}`),

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
  importModels: (models: unknown[]) =>
    api.post<{ created: number; updated: number; skipped: string[] }>(
      '/api/admin/models/import', { models }),
  updateModel: (id: string, body: Record<string, unknown>) =>
    api.patch<{ model: AdminModel }>(`/api/admin/models/${id}`, body),
  codes: () => api.get<{ codes: RedemptionCode[] }>('/api/admin/codes'),
  createCode: (body: Record<string, unknown>) =>
    api.post<{ codes: RedemptionCode[] }>('/api/admin/codes', body),
  deleteCode: (id: string) => api.delete<void>(`/api/admin/codes/${id}`),
  grantCards: (userID: string, body: { cards: number; card_days: number }) =>
    api.post<void>(`/api/admin/users/${userID}/cards`, body),
  reorderModels: (ids: string[]) =>
    api.put<void>('/api/admin/models/order', { ids }),
  resetQuota: (body: { scope: 'all' | 'group' | 'user'; id?: string }) =>
    api.post<{ accounts: number }>('/api/admin/usage/reset', body),
  deleteModel: (id: string) => api.delete<void>(`/api/admin/models/${id}`),

  logs: (query: string) =>
    api.get<{ entries: LogEntry[]; total: number; limit: number; offset: number }>(
      `/api/admin/logs${query}`,
    ),
  logFacets: (query: string) => api.get<LogFacets>(`/api/admin/logs/facets${query}`),
  pruneLogs: (days: number) =>
    api.post<{ removed: number }>('/api/admin/logs/prune', { days }),

  usage: (query: string) =>
    api.get<{
      totals: UsageTotals;
      by_model: UsageBreakdown[];
      by_provider: UsageBreakdown[];
      by_user: UsageBreakdown[];
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
    api.get<{
      settings: Record<string, string>;
      groups: Group[];
      mail_configured: boolean;
      attachments: HeldAttachments;
    }>(
      '/api/admin/settings',
    ),
  saveSettings: (values: Record<string, string>) =>
    api.put<{ settings: Record<string, string> }>('/api/admin/settings', values),
  // More forgiving than saveSettings: identifiers that mean nothing on this
  // instance are cleared and named back rather than failing the whole file.
  // The daily purge, on demand. Same operation, without waiting for 03:00.
  purgeAttachments: () =>
    api.post<{ purged: number; attachments: HeldAttachments }>('/api/admin/attachments/purge'),
  importSettings: (values: Record<string, string>) =>
    api.post<{ settings: Record<string, string>; applied: number; skipped: string[] }>(
      '/api/admin/settings/import',
      values,
    ),

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
