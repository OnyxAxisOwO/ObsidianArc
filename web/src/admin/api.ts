// The administrative API, typed.
//
// Kept apart from the rest of the client so the shape of what an
// administrator can do is one file. Note what is not here: no provider API
// key, in either direction beyond writing a new one. The server never sends
// one back, and there is no field on these types that could carry it.

import { api, ApiError } from '../api/client';
import type { ApiKey } from '../api/keys';
import type { UsageSummary } from '../api/usage';
import { t } from '../composables/useI18n';

// Re-exported so an admin screen imports one module, the way every other
// shape on this surface already does.
export type { ApiKey };

/** A session on the operator's side of the same list `DeviceSession` is for
 *  an account's own screen — no "current" flag, since nothing about the
 *  operator's own browser belongs in somebody else's device list. */
export interface AdminSession {
  id: string;
  created_at: number;
  last_seen_at: number;
  ip: string;
  user_agent: string;
}
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
  name?: string;
  windows?: string[];
  cards: number;
  claimed: number;
  card_days: number;
  expires_at: number;
  note: string;
  created_at: number;
}

export interface CodeRedemption {
  user_id: string;
  username: string;
  nickname: string;
  redeemed_at: number;
}

export type InviteStatus = 'active' | 'used_up' | 'expired' | 'revoked';

/** What a code is for. Independent of `owner_id`, which only ever tells "an
 *  account's own" (personal) from "nobody's" (batch and partner are both
 *  `owner_id: ''`) — only `kind` tells those last two apart. */
export type InviteKind = 'batch' | 'partner' | 'personal';

/** One code — a batch entry, a partner's named link, or a personal one an
 *  account carries. */
export interface InviteCode {
  id: string;
  code: string;
  owner_id: string;
  owner_username: string;
  owner_nickname: string;
  kind: InviteKind;
  /** The partner's name. Set only for a partner code. */
  name: string;
  /** Whether an account that already exists may also claim this code,
   *  on top of whoever registers through it. */
  allow_existing: boolean;
  group_id: string;
  group_name: string;
  /** The fixed length when group_days_max is 0, otherwise the range's floor. */
  group_days: number;
  /** 0 means fixed — group_days is the whole answer. */
  group_days_max: number;
  /** 0 means unlimited. */
  max_uses: number;
  uses: number;
  expires_at: number;
  revoked_at: number;
  note: string;
  created_by: string;
  created_at: number;
  status: InviteStatus;
  /** How many invite_claims rows name this code — an existing account
   *  having claimed it, distinct from `uses`, which counts registrations. */
  claims: number;
}

export interface InviteUse {
  user_id: string;
  username: string;
  nickname: string;
  /** What this particular signup was granted — a code's range differs per use. */
  group_days: number;
  created_at: number;
  rewarded_at: number;
  reward_cards: number;
  /** 'same_ip' | 'limit' | 'disabled' | '' (rewarded, or nothing to reward). */
  reward_skipped: string;
  /** 'register' for a new account seated by this code, 'claim' for an
   *  existing account that redeemed it instead. */
  via: 'register' | 'claim';
}

export interface InviteTopInviter {
  user_id: string;
  username: string;
  nickname: string;
  invites: number;
  rewarded: number;
}

/** One row of the partner leaderboard `InviteStats.partners` returns. */
export interface InvitePartnerStat {
  id: string;
  code: string;
  name: string;
  registrations: number;
  claims: number;
}

export interface InviteStats {
  active: number;
  uses_total: number;
  uses_7d: number;
  top_inviters: InviteTopInviter[];
  partners: InvitePartnerStat[];
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
  supports_image_gen: boolean;
  supports_chat_image_gen: boolean;
  emulate_tools: boolean;
  request_override?: string;
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
  allow_terminal: boolean;
  show_expiry: boolean;
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
  /** Distinct accounts behind the rows: on a model's row, how widely it is used. */
  users: number;
  /** Distinct models behind the rows: on an account's row, how many it moves between. */
  models: number;
  /** Summed, never averaged on the server: divide by requests for the mean. */
  duration_ms: number;
}

export interface UsageBreakdown extends UsageTotals {
  key: string;
  label: string;
  /** The provider under a model, the handle under an account's nickname. */
  detail?: string;
  last_at: number;
}

export interface UsagePoint extends UsageTotals {
  at: number;
}

/** One hour of one weekday, on the reader's clock. 0 is Sunday. */
export interface UsageSlot {
  weekday: number;
  hour: number;
  requests: number;
  total_tokens: number;
}

export interface UsageCell extends UsageTotals {
  row: string;
  col: string;
}

/** The account-by-model grid: the keys it was counted for, and the cells. */
export interface UsageMatrix {
  rows: string[];
  cols: string[];
  cells: UsageCell[];
}

/** What the usage page reads in one request. */
export interface UsageReport {
  totals: UsageTotals;
  /** The same span immediately before; null for "all time", which has no before. */
  previous: UsageTotals | null;
  by_model: UsageBreakdown[];
  by_provider: UsageBreakdown[];
  by_user: UsageBreakdown[];
  by_group: UsageBreakdown[];
  by_status: UsageBreakdown[];
  series: UsagePoint[];
  bucket_ms: number;
  heatmap: UsageSlot[];
  matrix: UsageMatrix;
  current_rpm?: number;
}

export type UsageDimension = 'model' | 'provider' | 'user' | 'group' | 'status';

export interface UsageRecord {
  id: string;
  user_id: string;
  username: string;
  nickname?: string;
  model_name: string;
  provider_name: string;
  conversation_id: string;
  input_tokens: number;
  output_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  credits: number;
  /** The provider reported no usage; the tokens were estimated from the text. */
  estimated?: boolean;
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
  /** The 24 hours before those, so each figure can say which way it moved. */
  prev_24h: UsageTotals;
  last_7d: UsageTotals;
  top_models: UsageBreakdown[];
  top_users: UsageBreakdown[];
  series: UsagePoint[];
  bucket_ms: number;
  heatmap: UsageSlot[];
  recent: UsageRecord[];
}

export interface Meta {
  provider_kinds: ProviderKind[];
  reasoning_styles: ReasoningStyle[];
}

// The reader's client already describes this shape, and one row of JSON
// should not have two declarations that can drift apart.
import type { Announcement, DisplayMode } from '../api/announcements';
import type {
  Feedback, FeedbackKind, FeedbackPriority, FeedbackReply, FeedbackStatus, FeedbackThread,
} from '../api/feedback';

export type { Announcement, DisplayMode };
export type { Feedback, FeedbackKind, FeedbackPriority, FeedbackReply, FeedbackStatus, FeedbackThread };

/**
 * The count block above the operator's feedback list.
 *
 * Answered alongside the page rather than by a second endpoint: it is four
 * numbers over the same table the list has just been read from, and a
 * summary that can disagree with the list under it is worse than no summary.
 */
export interface FeedbackSummary {
  total: number;
  open: number;
  bugs: number;
  ideas: number;
  high_open: number;
  /** Threads whose last word is the reader's, and so are owed an answer. */
  awaiting: number;
}

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

/** One access decision, kept separately from the request log. */
export interface SecurityEvent {
  id: string;
  at: number;
  event: 'signup_review' | 'api_restriction' | 'api_restriction_lifted' | 'chat_challenge' | string;
  severity: 'info' | 'warning' | 'danger';
  user_id?: string;
  username?: string;
  actor_id?: string;
  actor_username?: string;
  ip?: string;
  source?: string;
  decision?: string;
  reason?: string;
}

/** How far the instance is from the two-step policy it wants. */
export interface TwoFactorAdoption {
  policy: 'optional' | 'backoffice' | 'admins' | 'everyone';
  accounts: number;
  enabled: number;
  admins: number;
  admins_enabled: number;
  /** At most fifty, by username: the administrators a stricter policy would
   *  stop at the door. */
  admins_without: { id: string; username: string; nickname: string }[];
  remember_days: number;
  /** The name an app files the entry under when no issuer is set. */
  issuer_fallback: string;
  available: boolean;
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

/**
 * How a breakdown is ranked. Several defensible answers to "the most"; `users`
 * is the one that asks how popular something is rather than how busy.
 */
export type UsageMetric = 'requests' | 'tokens' | 'credits' | 'users';

/**
 * The reader's distance from UTC in minutes east, which is what the server
 * aligns a day to. Asked each time rather than once, so a laptop that crossed
 * a time zone since the page opened draws its days where the reader now is.
 */
export function zoneQuery(): string {
  return `tz=${-new Date().getTimezoneOffset()}`;
}

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

export interface ProbeProgress {
  completed: number;
  total: number;
  succeeded: number;
  failed: number;
}

/** What one account is holding in reset cards. */
export interface CardHolding {
  available: number;
  used: number;
  expired: number;
  total: number;
  /** The unused, unexpired ones, soonest to expire first. */
  cards: Array<{ id: string; name?: string; windows?: string[]; source: string; expires_at: number; created_at: number }>;
}

export type GroupOption = Pick<Group, 'id' | 'name'>;
export type ModelOption = Pick<AdminModel, 'id' | 'display_name' | 'model_id' | 'enabled' | 'provider_name'>;
export type ProviderOption = Pick<Provider, 'id' | 'name' | 'kind' | 'enabled'>;
/** An application registered to sign people in with accounts from here. */
export interface SignInApplication {
  id: string;
  client_id: string;
  name: string;
  description: string;
  /** Matched by exact string equality when a code is issued. */
  redirect_uris: string[];
  scopes: string[];
  /** Skips the consent screen. For the operator's own services only. */
  trusted: boolean;
  disabled: boolean;
  /** False for an application with nowhere to keep a secret, which must use PKCE. */
  confidential: boolean;
  created_at: number;
  updated_at: number;
}

export interface ApplicationInput {
  name?: string;
  description?: string;
  /** One per line, as the textarea collects them. */
  redirect_uris?: string;
  scopes?: string[];
  trusted?: boolean;
  disabled?: boolean;
  public?: boolean;
}

export interface AdminMailSettings {
  host: string;
  port: number;
  username: string;
  from: string;
  implicit_tls: boolean;
  public_url: string;
  password_set: boolean;
}

export interface AdminMailUpdate extends Omit<AdminMailSettings, 'password_set'> {
  password: string;
  clear_password: boolean;
}

export interface AdminUserCheckSettings {
  enabled: boolean;
  exempt_domains: string[];
  failure_mode: 'allow' | 'reject';
  api_key_set: boolean;
}

export interface AdminUserCheckUpdate extends Omit<AdminUserCheckSettings, 'api_key_set'> {
  api_key: string;
  clear_api_key: boolean;
}

export interface AdminUserCheckTestResult {
  disposable: boolean;
  skipped?: boolean;
}

export const adminApi = {
  mail: () => api.get<AdminMailSettings>('/api/admin/mail'),
  saveMail: (body: AdminMailUpdate) => api.put<AdminMailSettings>('/api/admin/mail', body),
  testMail: (to: string) => api.post<void>('/api/admin/mail/test', { to }),
  userCheck: () => api.get<AdminUserCheckSettings>('/api/admin/usercheck'),
  saveUserCheck: (body: AdminUserCheckUpdate) => api.put<AdminUserCheckSettings>('/api/admin/usercheck', body),
  testUserCheck: (email: string) => api.post<AdminUserCheckTestResult>('/api/admin/usercheck/test', { email }),
  groupOptions: () => api.get<{ groups: GroupOption[] }>('/api/admin/references'),
  modelOptions: () => api.get<{ models: ModelOption[] }>('/api/admin/references'),
  providerOptions: () => api.get<{ providers: ProviderOption[] }>('/api/admin/references'),
  memberOptions: (query: string) => api.get<{ users: Account[]; total: number }>(`/api/admin/member-options${query}`),
  dashboard: (metric: UsageMetric = 'credits') =>
    api.get<Dashboard>(`/api/admin/dashboard?metric=${metric}&${zoneQuery()}`),
  meta: () => api.get<Meta>('/api/admin/meta'),
  tryReview: (body: Record<string, unknown>) =>
    api.post<{ ran: boolean; decision: 'allow' | 'restrict' | 'refuse'; reason: string }>(
      '/api/admin/security/review', body),
  // The applications allowed to use this instance as a sign-in. Registering
  // one answers with its client secret, once; nothing can show it again.
  applications: () =>
    api.get<{ applications: SignInApplication[]; issuer: string; scopes: string[] }>(
      '/api/admin/applications',
    ),
  createApplication: (input: ApplicationInput) =>
    api.post<{ application: SignInApplication; client_secret: string }>(
      '/api/admin/applications', input,
    ),
  updateApplication: (id: string, input: ApplicationInput) =>
    api.patch<{ application: SignInApplication }>(`/api/admin/applications/${id}`, input),
  rotateApplicationSecret: (id: string) =>
    api.post<{ client_secret: string }>(`/api/admin/applications/${id}/secret`, {}),
  deleteApplication: (id: string) =>
    api.delete<void>(`/api/admin/applications/${id}`),
  twoFactorAdoption: () => api.get<TwoFactorAdoption>('/api/admin/security/two-factor'),
  securityEvents: (query = '') =>
    api.get<{ events: SecurityEvent[]; total: number; limit: number; offset: number }>(
      `/api/admin/security/events${query}`,
    ),
  health: (hours = 24) =>
    api.get<{
      hours: number;
      models: ModelHealth[];
      policy: {
        probe: boolean;
        window_mins: number;
        disable_after: number;
        disable_below?: number;
        warn_below?: number;
        show_users?: boolean;
        retain_days?: number;
        reset_at?: number;
      };
    }>(`/api/admin/health?hours=${hours}`),
  probeAllModels: (onProgress: (progress: ProbeProgress) => void) =>
    streamProbeAllModels(onProgress),
  resetHealth: () =>
    api.post<{ reset_at: number; probes_cleared: number; models_reenabled: number }>(
      '/api/admin/health/reset',
    ),
  resources: () => api.get<Resources>('/api/admin/resources'),

  users: (query: string) => api.get<{ users: Account[]; total: number }>(`/api/admin/users${query}`),
  user: (id: string) =>
    api.get<{
      user: Account;
      usage: UsageSummary;
      lifetime: UsageTotals;
      /** Every model this account has used, by tokens: what it spends on. */
      models?: UsageBreakdown[];
      policy: QuotaPolicy;
      cards: CardHolding;
    }>(`/api/admin/users/${id}`),
  updateUser: (id: string, patch: Record<string, unknown>) =>
    api.patch<{ user: Account }>(`/api/admin/users/${id}`, patch),
  deleteUser: (id: string) => api.delete<void>(`/api/admin/users/${id}`),
  resetPassword: (id: string, newPassword: string) =>
    api.post<void>(`/api/admin/users/${id}/password`, { new_password: newPassword }),
  resetTwoFactor: (id: string) =>
    api.delete<{ user: Account }>(`/api/admin/users/${id}/two-factor`),
  // Only ever the record, never the token: the server keeps a digest, so
  // there is nothing an administrator could be shown even in principle.
  userKeys: (id: string) => api.get<{ keys: ApiKey[] }>(`/api/admin/users/${id}/keys`),
  revokeUserKey: (id: string, keyID: string) =>
    api.delete<void>(`/api/admin/users/${id}/keys/${keyID}`),

  userSessions: (id: string) => api.get<{ sessions: AdminSession[] }>(`/api/admin/users/${id}/sessions`),
  revokeUserSession: (id: string, sessionID: string) =>
    api.delete<void>(`/api/admin/users/${id}/sessions/${sessionID}`),
  revokeAllUserSessions: (id: string) => api.delete<void>(`/api/admin/users/${id}/sessions`),

  userConversations: (id: string, query = '') =>
    api.get<{ conversations: Conversation[]; total: number }>(`/api/admin/users/${id}/conversations${query}`),
  userTranscript: (id: string, conversationID: string) =>
    api.get<{ conversation: Conversation; messages: Message[] }>(
      `/api/admin/users/${id}/conversations/${conversationID}`,
    ),

  groups: () => api.get<{ groups: Group[]; policies: QuotaPolicy[] }>('/api/admin/groups'),
  assignGroupMembers: (id: string, user_ids: string[], expires_at: number) =>
    api.post<{ updated: number }>(`/api/admin/groups/${id}/members`, { user_ids, expires_at }),
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
  codeRedemptions: (id: string) =>
    api.get<{ redemptions: CodeRedemption[] }>(`/api/admin/codes/${encodeURIComponent(id)}/redemptions`),
  deleteCode: (id: string) => api.delete<void>(`/api/admin/codes/${id}`),
  invites: (query: string) =>
    api.get<{ codes: InviteCode[]; total: number }>(`/api/admin/invites${query}`),
  createInvites: (body: Record<string, unknown>) =>
    api.post<{ codes: InviteCode[] }>('/api/admin/invites', body),
  revokeInvite: (id: string) =>
    api.delete<{ code: InviteCode }>(`/api/admin/invites/${encodeURIComponent(id)}`),
  inviteUses: (id: string) =>
    api.get<{ uses: InviteUse[] }>(`/api/admin/invites/${encodeURIComponent(id)}/uses`),
  inviteStats: () => api.get<InviteStats>('/api/admin/invites/stats'),
  grantCards: (userID: string, body: { name?: string; windows?: string[]; cards: number; expires_at: number }) =>
    api.post<void>(`/api/admin/users/${userID}/cards`, body),
  // Moves cards the account already holds. Omitting card_ids means every
  // unused one, expired included — which is what "their card ran out" asks
  // for and what granting another one would get wrong.
  rescheduleCards: (userID: string, body: { expires_at: number; card_ids?: string[] }) =>
    api.patch<{ moved: number }>(`/api/admin/users/${userID}/cards`, body),
  // Takes one unused card back. Nothing is sent to the account: cards arrive
  // without a message and they leave the same way.
  revokeCard: (userID: string, cardID: string) =>
    api.delete<void>(`/api/admin/users/${userID}/cards/${encodeURIComponent(cardID)}`),
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
    api.get<UsageReport>(`/api/admin/usage${query}`),
  // One dimension alone — who used a model, what an account used — for a panel
  // that should not pay for the whole report.
  usageBreakdown: (dimension: UsageDimension, query = '') =>
    api.get<{ rows: UsageBreakdown[] }>(`/api/admin/usage/breakdown?dimension=${dimension}${query ? `&${query}` : ''}`),
  usageRecords: (query: string) =>
    api.get<{ records: UsageRecord[]; total: number }>(`/api/admin/usage/records${query}`),
  rpm: (query = '') =>
    api.get<{ rpm: number }>(`/api/admin/usage/rpm${query}`),

  policies: () => api.get<{ policies: QuotaPolicy[] }>('/api/admin/quota/policies'),
  savePolicy: (policy: Record<string, unknown>) =>
    api.put<{ policy: QuotaPolicy }>('/api/admin/quota/policies', policy),
  deletePolicy: (scope: string, scopeID: string) =>
    api.delete<void>(`/api/admin/quota/policies/${scope}?scope_id=${encodeURIComponent(scopeID)}`),

  settings: () =>
    api.get<{
      settings: Record<string, string>;
      groups?: GroupOption[];
      mail_configured?: boolean;
      attachments?: HeldAttachments;
      login_background?: Record<string, string>;
      logo_url?: string;
    }>(
      '/api/admin/settings',
    ),
  saveSettings: (values: Record<string, string>) =>
    api.put<{ settings: Record<string, string> }>('/api/admin/settings', values),
  uploadLoginBackground: (variant: string, mime: string, data: string) =>
    api.put<{ url: string; updated_at: number }>(`/api/admin/login-background/${variant}`, { mime, data }),
  deleteLoginBackground: (variant: string) =>
    api.delete<void>(`/api/admin/login-background/${variant}`),
  uploadSiteLogo: (mime: string, data: string) =>
    api.put<{ url: string; updated_at: number }>('/api/admin/logo', { mime, data }),
  deleteSiteLogo: () =>
    api.delete<void>('/api/admin/logo'),
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

  feedback: (query = '') =>
    api.get<{
      feedback: Feedback[];
      total: number;
      offset: number;
      summary: FeedbackSummary;
    }>(`/api/admin/feedback${query}`),
  // Fetching a thread is also what marks it read on the operator's side:
  // there is no other reason to open one.
  feedbackThread: (id: string) => api.get<FeedbackThread>(`/api/admin/feedback/${id}`),
  replyToFeedback: (id: string, body: string) =>
    api.post<FeedbackReply>(`/api/admin/feedback/${id}/replies`, { body }),
  deleteFeedbackReply: (id: string, replyID: string) =>
    api.delete<void>(`/api/admin/feedback/${id}/replies/${replyID}`),
  // Status is the only thing about the report itself an operator may change:
  // the words are what somebody wrote.
  setFeedbackStatus: (id: string, status: FeedbackStatus) =>
    api.patch<Feedback>(`/api/admin/feedback/${id}`, { status }),
  deleteFeedback: (id: string) => api.delete<void>(`/api/admin/feedback/${id}`),
};

async function streamProbeAllModels(
  onProgress: (progress: ProbeProgress) => void,
): Promise<ProbeProgress> {
  const response = await fetch('/api/admin/health/probe', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { Accept: 'text/event-stream' },
  });
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as
      | { error?: { code?: string; message?: string } }
      | null;
    throw new ApiError(
      response.status,
      body?.error?.code ?? 'probe',
      body?.error?.message ?? t('probeAllModelsFailed'),
    );
  }
  if (!response.body) throw new ApiError(0, 'stream', t('probeAllModelsFailed'));

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let finished: ProbeProgress | null = null;

  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    let boundary = /\r?\n\r?\n/.exec(buffer);
    while (boundary) {
      const result = probeFrame(buffer.slice(0, boundary.index), onProgress);
      if (result) finished = result;
      buffer = buffer.slice(boundary.index + boundary[0].length);
      boundary = /\r?\n\r?\n/.exec(buffer);
    }
  }
  if (!finished) throw new ApiError(0, 'stream', t('probeAllModelsFailed'));
  return finished;
}

function probeFrame(
  frame: string,
  onProgress: (progress: ProbeProgress) => void,
): ProbeProgress | null {
  let event = '';
  let data = '';
  for (const line of frame.split(/\r?\n/)) {
    if (line.startsWith('event:')) event = line.slice(6).trim();
    else if (line.startsWith('data:')) data += line.slice(5).trim();
  }
  if (!data) return null;

  let payload: ProbeProgress;
  try {
    payload = JSON.parse(data) as ProbeProgress;
  } catch {
    return null;
  }
  if (event === 'error') {
    throw new ApiError(500, 'probe', t('probeAllModelsFailed'));
  }
  if (event === 'progress') onProgress(payload);
  return event === 'done' ? payload : null;
}

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
