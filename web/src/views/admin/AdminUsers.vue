<script setup lang="ts">
// Users: search, edit, disable, reset, and read their conversations.
//
// That last one is an intrusion even when it is justified, so it sits behind
// its own click, says whose transcript it is, and leaves a line in the server
// log. Nothing about it is incidental to opening the account panel.

import { computed, onMounted, ref, watch } from 'vue';
import { useDebounceFn } from '@vueuse/core';
import {
  adminApi, emptyPolicy,
  type Account, type AccountStatus, type AdminSession, type ApiKey, type CardHolding, type Conversation,
  type Group, type Message, type QuotaWindowKind, type Role, type UsageBreakdown,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import type { UsageSummary } from '@/api/usage';
import { ADMIN_PERMISSIONS } from '@/admin/permissions';
import OaCheckList from '@/components/OaCheckList.vue';
import type { PageState } from '@/components/table-types';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import OaUsageWindow from '@/components/OaUsageWindow.vue';
import type { Column } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { t, tn } from '@/composables/useI18n';
import { IconTrash } from '@/icons';
import { absoluteTime, compactNumber, relativeTime } from '@/lib/format';
import { describeUserAgent } from '@/lib/ua';
import { currentUser, canAdmin, isSuperAdmin } from '@/stores/session';
import { isMasked, maskUser, maskLog, maskBilling, maskCredential } from '@/admin/safeMode';
import AdminFailure from './AdminFailure.vue';
import CreditsField from './CreditsField.vue';
import ExpiryPresets from './ExpiryPresets.vue';
import UsageBoard from './usage/UsageBoard.vue';
import { useAdminView } from './adminView';

const WINDOWS: QuotaWindowKind[] = ['5h', '1w', '1m'];

const view = useAdminView();
view.setTitle(t('usersTitle'));

const groups = ref<Pick<Group, 'id' | 'name'>[]>([]);
const users = ref<Account[]>([]);
const total = ref(0);
const paging = ref<PageState>({ page: 1, pageSize: 20 });
let request = 0;
function changePage(next: PageState): void { paging.value = next; void list(); }
function filterList(): void { paging.value.page = 1; ++request; void list(); }
const error = ref('');
const listError = ref('');
const loaded = ref(false);
const listing = ref(false);

/**
 * Kept out here so that editing an account and coming back does not silently
 * reset the filter the administrator was reading through.
 */
const filters = ref({ ...state });

const columns = computed<Array<Column<Account>>>(() => [
  // No width: the name is the column that takes what the others leave.
  { key: 'account', header: t('colAccount') },
  { key: 'email', header: t('colEmail'), text: (row) => row.email ? maskUser(row.email) : '—', secondary: true, width: '160px' },
  { key: 'group', header: t('colGroup'), text: (row) => groupName(row.group_id), width: '120px' },
  { key: 'role', header: t('colRole'), width: '120px' },
  { key: 'seen', header: t('colLastSeen'), text: (row) => relativeTime(row.last_active_at || row.last_login_at), secondary: true, width: '110px' },
]);

function groupName(id: string): string {
  return groups.value.find((group) => group.id === id)?.name ?? '—';
}

// Typing filters after a pause rather than on each keystroke: one request per
// word, not one per letter.
const debouncedList = useDebounceFn(() => void list(), 250);
watch(() => filters.value.q, () => { paging.value.page = 1; ++request; void debouncedList(); });

async function list(): Promise<void> {
  const ticket = ++request;
  Object.assign(state, filters.value);
  listing.value = true;
  listError.value = '';

  const query = new URLSearchParams({ limit: String(paging.value.pageSize), offset: String((paging.value.page - 1) * paging.value.pageSize) });
  if (filters.value.q) query.set('q', filters.value.q.trim());
  if (filters.value.role) query.set('role', filters.value.role);
  if (filters.value.status) query.set('status', filters.value.status);
  if (filters.value.group) query.set('group_id', filters.value.group);

  try {
    const result = await adminApi.users(query.toString() ? `?${query}` : '');
    if (ticket !== request) return;
    if (result.total > 0 && (paging.value.page - 1) * paging.value.pageSize >= result.total) {
      paging.value.page = Math.ceil(result.total / paging.value.pageSize);
      await list(); return;
    }
    users.value = result.users ?? [];
    total.value = result.total;
    view.setTitle(t('usersTitle'), tn(result.total, 'accountsCountOne', 'accountsCountOther'));
  } catch (failure) {
    if (ticket !== request) return;
    listError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    if (ticket === request) listing.value = false;
  }
}

// --- the account panel ------------------------------------------------------------

type PanelMode = 'account' | 'conversations' | 'transcript';

const panelOpen = ref(false);
const loadingDetail = ref(false);
// Whether the form below holds this account. Until it does the panel shows
// nothing editable: an empty form saved by mistake would clear real fields.
const detailReady = ref(false);
let opening = 0;
const mode = ref<PanelMode>('account');
const busy = ref(false);
const panelError = ref('');

const account = ref<Account | null>(null);
const usage = ref<UsageSummary | null>(null);
const lifetime = ref<{ requests: number; total_tokens: number; credits: number } | null>(null);
const cards = ref<CardHolding | null>(null);
/** Every model the account has used, by tokens: what its spending goes on. */
const models = ref<UsageBreakdown[]>([]);
const keys = ref<ApiKey[] | null>(null);
const sessions = ref<AdminSession[] | null>(null);
// Kept apart from `sessions` staying null: null-forever would read as "still
// loading" and an empty array would read as "signed in nowhere", and a 403 on
// a restricted operator is neither.
const sessionsError = ref('');
const revokingSession = ref('');
const signOutAllBusy = ref(false);
const conversations = ref<Conversation[] | null>(null);
const conversationPage = ref<PageState>({ page: 1, pageSize: 20 });
const conversationTotal = ref(0);
async function changeConversationPage(next: PageState): Promise<void> { conversationPage.value = next; await openConversations(); }
const transcript = ref<Message[] | null>(null);
const transcriptTitle = ref('');
const apiRestrictionTouched = ref(false);

const grantCount = ref<number | null>(1);
const grantName = ref('');
const grantScopePreset = ref<'full' | '5h' | '1w' | '1m' | 'custom'>('full');
const grantCustomWindows = ref<string[]>(['5h']);
const grantExpiresAt = ref(defaultGrantExpiry());
const grantLabel = ref('');
const rescheduleLabel = ref('');
const rescheduling = ref('');

function grantSelectedWindows(): string[] {
  if (grantScopePreset.value === 'custom') {
    return grantCustomWindows.value;
  }
  if (grantScopePreset.value === 'full') {
    return [];
  }
  return [grantScopePreset.value];
}

function formatCardScope(card: { windows?: string[] }): string {
  const wins = card.windows ?? [];
  if (wins.length === 0 || wins.includes('full')) {
    return t('cardFullReset');
  }
  const order = ['5h', '1w', '1m'];
  const sorted = [...wins].sort((a, b) => order.indexOf(a) - order.indexOf(b));
  if (sorted.length === 1) {
    if (sorted[0] === '5h') return t('cardScope5H');
    if (sorted[0] === '1w') return t('cardScope1W');
    if (sorted[0] === '1m') return t('cardScope1M');
  }
  const labels = sorted.map((w) => {
    if (w === '5h') return t('cardScopeLabel5H');
    if (w === '1w') return t('cardScopeLabel1W');
    if (w === '1m') return t('cardScopeLabel1M');
    return w;
  });
  return t('cardScopeCombined', { windows: labels.join(' + ') });
}

function cardTitle(card: { name?: string; windows?: string[] }): string {
  if (card.name && card.name.trim()) {
    return card.name.trim();
  }
  return formatCardScope(card);
}

function cardSubtitle(card: { name?: string; windows?: string[]; source: string; expires_at: number }): string {
  const sourceText = card.source === 'grant' ? t('cardFromAdmin') : t('cardFromCode');
  const expiryText = card.expires_at > 0 ? t('cardExpires', { when: relativeTime(card.expires_at) }) : t('noLimit');
  const scope = formatCardScope(card);
  if (card.name && card.name.trim() && card.name.trim() !== scope) {
    return `${scope} · ${sourceText} · ${expiryText}`;
  }
  return `${sourceText} · ${expiryText}`;
}

// Unused cards, expired ones included: those are what a reschedule reaches,
// and the count beside the button has to say the same thing the request does
// or the operator will read the result as a failure.
const movableCards = computed(() => (cards.value?.available ?? 0) + (cards.value?.expired ?? 0));

function dateTimeLocal(at: number): string {
  const date = new Date(at);
  return new Date(at - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}

function defaultGrantExpiry(): string {
  return dateTimeLocal(Date.now() + 30 * 24 * 3_600_000);
}


const form = ref({
  nickname: '', email: '', qq: '', bio: '', avatar: '',
  role: 'user' as Role,
  permissions: [] as string[],
  status: 'active' as AccountStatus,
  group: '',
  groupExpiresAt: '',
  apiRestricted: false,
  apiRestrictionHours: 24 as number | null,
  newPassword: '',
  rpm: null as number | null,
  tpm: null as number | null,
  windows: {} as Record<QuotaWindowKind, {
    override: boolean; enabled: boolean;
    requests: number | null; tokens: number | null; credits: number | null;
  }>,
});

/**
 * "No limits at all", as one switch over the fields that already say it.
 *
 * Not a new flag on the account: the policy model can already express this —
 * a rate limit of 0 means no rate limit, and a window set to `enabled: false`
 * at the user level is an exemption that overrides whatever the group says,
 * without clearing the numbers underneath it. What was missing was a way to
 * say all of that at once, which is the thing an operator actually wants:
 * exempt this one person, leave everybody else's group alone.
 *
 * Turning it off returns every one of those fields to inheriting the group,
 * rather than to some remembered state — the account goes back to being an
 * ordinary member of whatever group it is in, which is the only other answer
 * that means anything.
 */
const unlimitedQuota = computed({
  get: (): boolean => form.value.rpm === 0 && form.value.tpm === 0 &&
    WINDOWS.every((kind) => form.value.windows[kind]?.override && !form.value.windows[kind]?.enabled),
  set: (on: boolean): void => {
    form.value.rpm = on ? 0 : null;
    form.value.tpm = on ? 0 : null;
    for (const kind of WINDOWS) {
      const window = form.value.windows[kind];
      if (!window) continue;
      window.override = on;
      if (on) window.enabled = false;
    }
  },
});

const self = computed(() => currentUser.value?.id === account.value?.id);
const twoFactorFlash = ref('');

function apiRestrictionActive(row: Account): boolean {
  return row.api_restricted && (row.api_restricted_until === 0 || row.api_restricted_until > Date.now());
}

const enforced = computed(() => usage.value?.windows.filter((window) => window.enforced) ?? []);
const unlimited = computed(() => !!usage.value && (usage.value.unlimited || !enforced.value.length));

const summaryStats = computed<Stat[]>(() => {
  const totals = lifetime.value;
  const row = account.value;
  if (!totals || !row) return [];
  return [
    { label: t('statRequests'), value: maskBilling(compactNumber(totals.requests)), note: t('lifetimeAllTime') },
    { label: t('statTokens'), value: maskBilling(compactNumber(totals.total_tokens)) },
    { label: t('statCredits'), value: maskBilling(compactNumber(totals.credits)) },
    {
      label: t('joined'),
      value: new Date(row.created_at).toLocaleDateString(),
      note: (row.last_active_at || row.last_login_at)
        ? t('lastSeenAt', { when: relativeTime(row.last_active_at || row.last_login_at) })
        : t('neverSignedIn'),
    },
  ];
});

/**
 * The facts about an account that are read rather than edited.
 *
 * The id first, because it is the one an operator has to paste somewhere: a
 * log line, a support thread, a URL. Monospace and selectable — a ULID that
 * has to be transcribed by eye is a ULID that gets transcribed wrong.
 */
const identity = computed<Array<[string, string, boolean]>>(() => {
  const row = account.value;
  if (!row) return [];
  const rows: Array<[string, string, boolean]> = [
    [t('colUID'), maskUser(row.id), true],
    [t('colRegistered'), absoluteTime(row.created_at), false],
    [t('colLastSeen'), (row.last_active_at || row.last_login_at) ? absoluteTime(row.last_active_at || row.last_login_at) : t('neverSignedIn'), false],
  ];
  // Only where it was recorded: accounts predating the column have none, and
  // an empty row reads as a missing value rather than an absent one.
  if (row.signup_ip) rows.push([t('colSignupIP'), maskLog(row.signup_ip), true]);
  if (row.signup_user_agent) rows.push([t('registrationUserAgent'), maskLog(row.signup_user_agent), true]);
  return rows;
});

const conversationColumns = computed<Array<Column<Conversation>>>(() => [
  { key: 'title', header: t('colTitle'), text: (row) => row.title || t('untitled') },
  { key: 'messages', header: t('colMessages'), text: (row) => String(row.message_count), numeric: true },
  { key: 'updated', header: t('colUpdated'), text: (row) => relativeTime(row.updated_at) },
]);

async function open(id: string): Promise<void> {
  panelError.value = '';
  twoFactorFlash.value = '';
  mode.value = 'account';
  conversationPage.value.page = 1;
  keys.value = null;
  sessions.value = null;
  sessionsError.value = '';
  grantLabel.value = '';
  rescheduleLabel.value = '';
  rescheduling.value = '';
  grantName.value = '';
  grantScopePreset.value = 'full';
  grantCustomWindows.value = ['5h'];
  grantCount.value = 1;
  grantExpiresAt.value = defaultGrantExpiry();

  // The panel opens on the click, with the row the list already has, and
  // fills in when the detail arrives. It used to wait for the detail first:
  // an account with hundreds of thousands of ledger rows takes seconds to
  // total, nothing moved in the meantime, and a failure was written above
  // the list — out of sight of a reader scrolled down to the row — so the
  // click looked like it had done nothing at all.
  const ticket = ++opening;
  const listed = users.value.find((candidate) => candidate.id === id);
  account.value = listed ?? null;
  usage.value = null;
  lifetime.value = null;
  models.value = [];
  cards.value = null;
  loadingDetail.value = true;
  detailReady.value = false;
  panelOpen.value = true;

  let detail;
  try {
    detail = await adminApi.user(id);
  } catch (failure) {
    if (ticket !== opening) return;
    loadingDetail.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
    // Without a row to show there is no panel to put the error in.
    if (!account.value) { listError.value = panelError.value; panelOpen.value = false; }
    return;
  }
  // A second click while the first was loading wins; the late answer about
  // the first account must not overwrite the panel now showing another.
  if (ticket !== opening) return;
  loadingDetail.value = false;

  const row = detail.user;
  const policy = detail.policy.id ? detail.policy : emptyPolicy('user', id);

  const windows = {} as typeof form.value.windows;
  for (const kind of WINDOWS) {
    const limits = policy.windows[kind];
    windows[kind] = {
      override: limits?.enabled !== null && limits?.enabled !== undefined,
      enabled: limits?.enabled === true,
      requests: limits?.requests ?? null,
      tokens: limits?.tokens ?? null,
      credits: limits?.credits ?? null,
    };
  }

  account.value = row;
  usage.value = detail.usage;
  lifetime.value = detail.lifetime;
  models.value = detail.models ?? [];
  cards.value = detail.cards;
  apiRestrictionTouched.value = false;
  const restrictionActive = apiRestrictionActive(row);
  const restrictionHours = restrictionActive && row.api_restricted_until > 0
    ? Math.max(1, Math.ceil((row.api_restricted_until - Date.now()) / 3_600_000))
    : 0;
  form.value = {
    nickname: row.nickname,
    email: row.email,
    qq: row.qq || '',
    bio: row.bio,
    avatar: row.avatar,
    role: row.role,
    permissions: [...(row.admin_permissions ?? [])],
    status: row.status,
    group: row.group_id,
    groupExpiresAt: row.group_expires_at ? dateTimeLocal(row.group_expires_at) : '',
    apiRestricted: restrictionActive,
    apiRestrictionHours: restrictionActive ? restrictionHours : 24,
    newPassword: '',
    rpm: policy.rpm,
    tpm: policy.tpm,
    windows,
  };
  detailReady.value = true;

  // The list and nothing else: the server keeps a digest, so there is no token
  // to show and no endpoint that could produce one. What an operator needs
  // here is to see that a key exists and to be able to revoke it.
  void adminApi.userKeys(id)
    .then(({ keys: list }) => { keys.value = list; })
    .catch(() => { keys.value = []; });
  void adminApi.userSessions(id)
    .then(({ sessions: list }) => { sessions.value = list; })
    .catch((failure: unknown) => {
      sessionsError.value = failure instanceof ApiError ? failure.message : String(failure);
    });
}

async function save(): Promise<void> {
  const row = account.value;
  if (!row) return;
  busy.value = true;
  panelError.value = '';
  try {
    const patch: Record<string, unknown> = {
      nickname: form.value.nickname.trim(),
      email: form.value.email.trim(),
      qq: form.value.qq.trim(),
      bio: form.value.bio.trim(),
      avatar: form.value.avatar.trim(),

      status: form.value.status,
    };
    if (canAdmin('administrators') && form.value.role !== row.role) patch.role = form.value.role;
    if (canAdmin('administrators') && (form.value.role === 'admin') &&
      (form.value.role !== row.role || JSON.stringify(form.value.permissions) !== JSON.stringify(row.admin_permissions ?? []))) {
      patch.admin_permissions = form.value.permissions;
    }
    // Only send a membership change when the operator edited it. A profile
    // panel left open across expiry must not silently renew the old group.
    const originalExpiry = row.group_expires_at ? dateTimeLocal(row.group_expires_at) : '';
    if (form.value.group !== row.group_id || form.value.groupExpiresAt !== originalExpiry) {
      const expiry = form.value.groupExpiresAt ? new Date(form.value.groupExpiresAt).getTime() : 0;
      if (form.value.groupExpiresAt && (!Number.isFinite(expiry) || expiry <= Date.now())) {
        throw new Error(t('membershipExpiryInvalid'));
      }
      patch.group_id = form.value.group;
      patch.group_expires_at = expiry;
    }
    if (apiRestrictionTouched.value) {
      patch.api_restricted = form.value.apiRestricted;
      patch.api_restriction_hours = form.value.apiRestrictionHours ?? 0;
    }
    const result = await adminApi.updateUser(row.id, patch);
    if (currentUser.value?.id === row.id) currentUser.value = { ...currentUser.value, ...result.user };

    if (form.value.newPassword) {
      await adminApi.resetPassword(row.id, form.value.newPassword);
    }

    if (canAdmin('users') || canAdmin('usage')) await adminApi.savePolicy({
      scope: 'user',
      scope_id: row.id,
      rpm: form.value.rpm,
      tpm: form.value.tpm,
      windows: Object.fromEntries(WINDOWS.map((kind) => [kind, form.value.windows[kind].override
        ? {
            enabled: form.value.windows[kind].enabled,
            requests: form.value.windows[kind].requests,
            tokens: form.value.windows[kind].tokens,
            credits: form.value.windows[kind].credits,
          }
        : { enabled: null, requests: null, tokens: null, credits: null }])),
    });

    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

/**
 * Turns somebody's second sign-in step off, for a lost phone and a lost
 * sheet of recovery codes. It commits on its own button rather than with the
 * form's Save: it is not a field, and it is not undone by closing the panel.
 */
async function resetTwoFactor(): Promise<void> {
  const row = account.value;
  if (!row) return;
  twoFactorFlash.value = '';
  panelError.value = '';
  try {
    const { user } = await adminApi.resetTwoFactor(row.id);
    account.value = { ...row, ...user };
    users.value = users.value.map((entry) => (entry.id === row.id ? { ...entry, ...user } : entry));
    twoFactorFlash.value = t('twoFactorResetDone');
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function remove(): Promise<void> {
  const row = account.value;
  if (!row) return;
  busy.value = true;
  try {
    await adminApi.deleteUser(row.id);
    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

/**
 * Straight to this account, without a code in between. Beside the figures it
 * changes, because "why does this person have no allowance left" and "give
 * them another" are one thought.
 */
function grant(): void {
  const row = account.value;
  if (!row) return;
  if (grantScopePreset.value === 'custom' && grantCustomWindows.value.length === 0) {
    panelError.value = t('cardResetScopeRequired');
    return;
  }
  const count = grantCount.value ?? 1;
  const expiresAt = new Date(grantExpiresAt.value).getTime();
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    panelError.value = t('grantCardExpiryInvalid');
    return;
  }
  grantLabel.value = '…';
  void adminApi.grantCards(row.id, {
    cards: count,
    expires_at: expiresAt,
    name: grantName.value.trim(),
    windows: grantSelectedWindows(),
  })
    .then(async () => {
      grantLabel.value = t('granted', { count });
      const detail = await adminApi.user(row.id);
      cards.value = detail.cards;
    })
    .catch((failure: unknown) => {
      grantLabel.value = '';
      panelError.value = failure instanceof ApiError ? failure.message : String(failure);
    });
}

/**
 * Moves cards this account already holds onto the date in the field above.
 *
 * The same picker the grant uses, because an operator asked for a date once
 * and should not have to type it twice to decide which of the two things they
 * meant. Naming one card moves that one; naming none moves every unused card,
 * which is the request that actually arrives.
 */
async function reschedule(cardIDs?: string[]): Promise<void> {
  const row = account.value;
  if (!row) return;
  const expiresAt = new Date(grantExpiresAt.value).getTime();
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    panelError.value = t('grantCardExpiryInvalid');
    return;
  }

  rescheduling.value = cardIDs?.length ? cardIDs[0]! : 'all';
  rescheduleLabel.value = '…';
  try {
    const result = await adminApi.rescheduleCards(row.id, {
      expires_at: expiresAt,
      ...(cardIDs?.length ? { card_ids: cardIDs } : {}),
    });
    rescheduleLabel.value = t('cardsMoved', { count: result.moved });
    // Re-read rather than patch the list in place: a lapsed card that was
    // moved forward is spendable again, and the counts above it move with it.
    const detail = await adminApi.user(row.id);
    cards.value = detail.cards;
  } catch (failure) {
    rescheduleLabel.value = '';
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    rescheduling.value = '';
  }
}

/**
 * Takes one card off this account.
 *
 * Silent, like every other thing that happens to cards here: they are granted
 * without a message and an operator undoing a mis-typed grant should not be
 * announcing it. The row goes when the server says it went, not optimistically
 * — a card being spent in another tab at that moment stays.
 */
async function revoke(cardID: string): Promise<void> {
  const row = account.value;
  if (!row) return;
  rescheduling.value = cardID;
  try {
    await adminApi.revokeCard(row.id, cardID);
    rescheduleLabel.value = t('cardRevoked');
    const detail = await adminApi.user(row.id);
    cards.value = detail.cards;
  } catch (failure) {
    rescheduleLabel.value = '';
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    rescheduling.value = '';
  }
}

function revokeKey(key: ApiKey): void {
  const row = account.value;
  if (!row) return;
  void adminApi.revokeUserKey(row.id, key.id)
    .then(() => { keys.value = (keys.value ?? []).filter((entry) => entry.id !== key.id); })
    .catch((failure: unknown) => {
      panelError.value = failure instanceof ApiError ? failure.message : String(failure);
    });
}

function revokeSession(session: AdminSession): void {
  const row = account.value;
  if (!row) return;
  revokingSession.value = session.id;
  adminApi.revokeUserSession(row.id, session.id)
    .then(() => { sessions.value = (sessions.value ?? []).filter((entry) => entry.id !== session.id); })
    .catch((failure: unknown) => {
      panelError.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { revokingSession.value = ''; });
}

function signOutEverywhere(): void {
  const row = account.value;
  if (!row) return;
  signOutAllBusy.value = true;
  adminApi.revokeAllUserSessions(row.id)
    .then(() => { sessions.value = []; })
    .catch((failure: unknown) => {
      panelError.value = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { signOutAllBusy.value = false; });
}

// Stepping in rather than stacking: the panel replaces itself and offers a
// way back, so the layout never grows a fourth column.
async function openConversations(): Promise<void> {
  const row = account.value;
  if (!row) return;
  mode.value = 'conversations';
  conversations.value = null;
  panelError.value = '';
  try {
    const result = await adminApi.userConversations(row.id, `?limit=${conversationPage.value.pageSize}&offset=${(conversationPage.value.page - 1) * conversationPage.value.pageSize}`);
    conversations.value = result.conversations;
    conversationTotal.value = result.total;
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function openTranscript(conversation: Conversation): Promise<void> {
  const row = account.value;
  if (!row) return;
  mode.value = 'transcript';
  transcript.value = null;
  transcriptTitle.value = conversation.title || t('conversationFallback');
  panelError.value = '';
  try {
    const { messages } = await adminApi.userTranscript(row.id, conversation.id);
    transcript.value = messages;
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

function turnLabel(message: Message): string {
  const model = message.model_name ? ` · ${message.model_name}` : '';
  const when = message.created_at ? ` · ${absoluteTime(message.created_at)}` : '';
  return `${message.role}${model}${when}`;
}

const panelTitle = computed(() => {
  if (mode.value === 'transcript') return transcriptTitle.value;
  const row = account.value;
  if (!row) return '';
  const name = maskUser(row.nickname || row.username);
  if (mode.value === 'conversations') {
    return t('someonesConversations', { name });
  }
  return name;
});

async function load(): Promise<void> {
  error.value = '';
  try {
    ({ groups: groups.value } = await adminApi.groupOptions());
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
    loaded.value = true;
    return;
  }
  loaded.value = true;
  await list();
}

onMounted(load);
</script>

<script lang="ts">
const state = { q: '', role: '', status: '', group: '' };
</script>

<template>
  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <div id="usersList" class="oa-filters">
      <input v-model="filters.q" type="search" :placeholder="t('searchUsers')">
      <OaSelect
        v-model="filters.role"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyRole') },
          { value: 'user', label: t('filterUsers') },
          { value: 'admin', label: t('filterAdmins') },
          { value: 'super_admin', label: t('superAdmin') },
        ]"
        @update:model-value="filterList"
      />
      <OaSelect
        v-model="filters.status"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyStatus') },
          { value: 'active', label: t('filterActive') },
          { value: 'disabled', label: t('filterDisabled') },
        ]"
        @update:model-value="filterList"
      />
      <OaSelect
        v-model="filters.group"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyGroup') },
          ...groups.map((group) => ({ value: group.id, label: group.name })),
        ]"
        @update:model-value="filterList"
      />
    </div>

    <p v-if="listing" class="oa-table-empty">{{ t('loading') }}</p>
    <p v-if="listError" class="oa-table-empty">{{ listError }}</p>
    <OaTable
      :pagination="{ ...paging, total }"
      :busy="listing"
      @page="changePage"
      :columns="columns"
      :rows="users"
      :empty="t('noAccountsMatch')"
      :muted="(row) => row.status === 'disabled'"
      selectable
      @select="open($event.id)"
    >
      <template #cell-account="{ row }">
        <OaCellStack
          :title="maskUser(row.nickname || row.username)"
          :sub="row.qq ? `@${maskUser(row.username)} · QQ ${maskUser(row.qq)}` : `@${maskUser(row.username)}`"
        />
      </template>
      <template #cell-role="{ row }">
        <OaBadgeRow>
          <OaBadge v-if="row.role === 'super_admin'">{{ t('superAdmin') }}</OaBadge>
          <OaBadge v-if="row.role === 'admin'">{{ t('admin') }}</OaBadge>
          <OaBadge v-if="row.status === 'disabled'" tone="danger">{{ t('disabled') }}</OaBadge>
          <OaBadge v-if="apiRestrictionActive(row)" tone="warning">{{ t('apiRestrictedBadge') }}</OaBadge>
          <OaBadge v-if="row.two_factor_at" tone="muted">{{ t('twoFactorBadge') }}</OaBadge>
        </OaBadgeRow>
      </template>
    </OaTable>
  </template>

  <OaPanel
    v-if="panelOpen && account"
    :title="panelTitle"
    :width="mode === 'transcript' ? 480 : 440"
    :footer="mode === 'account'"
    :cancel-label="mode === 'account' ? undefined : t('close')"
    :confirm-label="t('save')"
    :destructive-label="mode === 'account' && !self ? t('deleteLabel') : undefined"
    :destructive-confirm="mode === 'account' && !self
      ? t('confirmDeleteUser', { name: maskUser(account.username) })
      : undefined"
    :back="mode !== 'account'"
    :busy="busy || loadingDetail"
    :error="panelError"
    @close="panelOpen = false"
    @confirm="save"
    @destructive="remove"
    @back="mode === 'transcript' ? openConversations() : (mode = 'account')"
  >
    <p v-if="mode === 'account' && loadingDetail" class="oa-table-empty">{{ t('loading') }}</p>
    <template v-else-if="mode === 'account' && detailReady">
      <OaStatGrid :stats="summaryStats" />

      <div class="oa-facts">
        <div v-for="[label, value, mono] in identity" :key="label" class="oa-fact">
          <span class="oa-fact-label">{{ label }}</span>
          <span class="oa-fact-value" :class="{ mono }">{{ value }}</span>
        </div>
      </div>

      <!-- What the lifetime figures above were spent on. The question after
           "how much" is "on what", and it is cheaper to answer here than to
           send the operator to the usage page to narrow it by hand. -->
      <template v-if="models.length">
        <OaFormSection :title="t('boardTheirModels')" :hint="t('boardTheirModelsHint')" />
        <UsageBoard
          :rows="models" kind="model" metric="tokens" :limit="4" :reach="false"
          :selectable="canAdmin('usage') && !!view.open" :empty-text="t('nothingYet')"
          @select="view.open?.('/admin/usage', { user: account.id, model: $event })"
        />
        <button
          v-if="canAdmin('usage') && view.open" type="button" class="oa-btn small oa-panel-link"
          @click="view.open?.('/admin/usage', { user: account.id })"
        >{{ t('viewInUsage') }}</button>
      </template>

      <!-- The same bars the account sees in its own composer, from the same
           summary: an administrator answering "why can this person not send
           anything" should be reading the figure the person is up against. -->
      <template v-if="usage">
        <OaFormSection :title="t('secAllowance')" />
        <div class="oa-usage-list">
          <span v-if="unlimited" class="oa-usage-reset">{{ t('quotaUnlimited') }}</span>
          <OaUsageWindow
            v-for="window in enforced"
            :key="window.kind"
            :window="window"
            :display="usage.display ?? 'absolute'"
            :masked="isMasked('billing')"
          />
        </div>
      </template>

      <!-- What they are holding, before the control that adds more: an
           operator is usually here because somebody asked, and "you already
           have two" is the answer more often than a third card is. -->
      <OaFormSection :title="t('secHeldCards')" />
      <div v-if="cards">
        <p v-if="cards.total === 0" class="oa-field-hint">{{ t('cardsNone') }}</p>
        <template v-else>
          <p class="oa-card-count">{{ t('cardsAvailable', { count: cards.available }) }}</p>
          <!-- The three together, because "none left" and "never had any" are
               different answers and the first number cannot tell them apart. -->
          <p class="oa-field-hint">
            {{ t('cardsBreakdown', { used: cards.used, expired: cards.expired, total: cards.total }) }}
          </p>
          <div v-if="cards.cards.length" class="oa-card-list">
            <div v-for="card in cards.cards" :key="card.id" class="oa-card-row">
              <div>
                <span class="oa-card-title">{{ cardTitle(card) }}</span>
                <span class="oa-card-sub">{{ cardSubtitle(card) }}</span>
              </div>
              <span class="oa-header-spacer" />
              <!-- Moves this one onto the date below. One card at a time is
                   the "this one runs out tomorrow" case; the button under the
                   picker is the one that catches the expired ones too. -->
              <button
                type="button"
                class="oa-btn oa-card-move"
                :disabled="!!rescheduling"
                @click="reschedule([card.id])"
              >{{ t('rescheduleOne') }}</button>
              <!-- Never window.confirm: it answers false on its own in some
                   browsers, which would turn this into a button that silently
                   does nothing. -->
              <OaConfirmButton
                class="oa-btn oa-card-move oa-card-drop"
                :label="t('revokeCard')"
                :armed-label="t('revokeCardConfirm')"
                :armed-title="t('revokeCardConfirm')"
                :resting-title="t('revokeCard')"
                :disabled="!!rescheduling"
                @confirm="revoke(card.id)"
              />
            </div>
          </div>
        </template>
      </div>

      <OaTextField
        v-model="grantName"
        :label="t('cardName')"
        :placeholder="t('cardNamePlaceholder')"
        :hint="t('cardNameHint')"
        :max-length="64"
      />
      <OaSelectField
        v-model="grantScopePreset"
        :label="t('cardResetScope')"
        :hint="t('cardResetScopeHint')"
        :options="[
          { value: 'full', label: t('cardResetFull') },
          { value: '5h', label: t('cardReset5H') },
          { value: '1w', label: t('cardReset1W') },
          { value: '1m', label: t('cardReset1M') },
          { value: 'custom', label: t('cardResetCustom') },
        ]"
      />
      <OaCheckList
        v-if="grantScopePreset === 'custom'"
        v-model="grantCustomWindows"
        :label="t('cardResetScope')"
        :empty-text="t('nothingYet')"
        :items="[
          { value: '5h', label: t('cardScopeLabel5H') },
          { value: '1w', label: t('cardScopeLabel1W') },
          { value: '1m', label: t('cardScopeLabel1M') },
        ]"
      />
      <OaNumberField
        v-model="grantCount"
        :label="t('grantCards')"
        :min="1"
        :hint="t('grantCardsHint')"
      />
      <OaTextField
        v-model="grantExpiresAt"
        :label="t('grantCardExpiry')"
        :hint="t('grantCardExpiryHint')"
        type="datetime-local"
        required
      />
      <ExpiryPresets @pick="grantExpiresAt = $event" />
      <div class="oa-card-actions">
        <button type="button" class="oa-btn" :disabled="!!grantLabel" @click="grant">
          {{ grantLabel || t('grantCards') }}
        </button>
        <button
          type="button"
          class="oa-btn"
          :disabled="!movableCards || !!rescheduling"
          @click="reschedule()"
        >{{ rescheduleLabel || t('rescheduleCards', { count: movableCards }) }}</button>
      </div>
      <p class="oa-field-hint">{{ t('rescheduleCardsHint') }}</p>

      <OaFormSection :title="t('secProfile')" />
      <!-- Masking these would break editing them, so the value stays real and
           only its focus state decides whether it can be read: blurred until
           the operator actually clicks in, same as a screen share would need. -->
      <OaTextField v-model="form.nickname" :class="{ 'oa-safe-blur': isMasked('users') }" :label="t('nickname')" :max-length="32" />
      <OaTextField v-model="form.email" :class="{ 'oa-safe-blur': isMasked('users') }" :label="t('email')" type="email" />
      <OaTextField
        v-model="form.qq"
        :class="{ 'oa-safe-blur': isMasked('users') }"
        :label="t('qq')"
        :placeholder="t('qqPlaceholder')"
        :max-length="15"
      />
      <OaTextArea v-model="form.bio" :label="t('bio')" :rows="2" />
      <OaTextField
        v-model="form.avatar"
        :class="{ 'oa-safe-blur': isMasked('users') }"
        :label="t('avatar')"
        :placeholder="t('avatarPlaceholder')"
        :hint="t('avatarHint')"
      />

      <OaFormSection :title="t('secAccess')" />
      <OaSelectField
        v-if="canAdmin('administrators')"
        v-model="form.role"
        :label="t('role')"
        :hint="self ? t('cannotDemoteSelf') : undefined"
        :options="[
          { value: 'user', label: t('roleUser') },
          { value: 'admin', label: t('roleAdmin') },
          ...(isSuperAdmin ? [{ value: 'super_admin' as const, label: t('superAdmin') }] : []),
        ]"
      />
      <OaCheckList
        v-if="canAdmin('administrators') && form.role === 'admin'"
        v-model="form.permissions"
        :label="t('adminPermissions')"
        :hint="t('adminPermissionsHint')"
        :items="ADMIN_PERMISSIONS.filter((entry) => canAdmin(entry.value)).map((entry) => ({ value: entry.value, label: t(entry.label) }))"
        :empty-text="t('permissionDeniedTitle')"
      />
      <OaSelectField
        v-model="form.status"
        :label="t('status')"
        :hint="t('disableHint')"
        :options="[
          { value: 'active', label: t('statusActive') },
          { value: 'disabled', label: t('statusDisabled') },
        ]"
      />
      <OaSelectField
        v-model="form.group"
        :label="t('group')"
        :options="groups.map((entry) => ({ value: entry.id, label: entry.name }))"
        @update:model-value="form.groupExpiresAt = ''"
      />
      <OaTextField
        v-model="form.groupExpiresAt"
        type="datetime-local"
        :label="t('membershipExpiry')"
        :hint="t('membershipExpiryHint')"
      />
      <ExpiryPresets permanent @pick="form.groupExpiresAt = $event" />
      <OaSwitchField
        v-model="form.apiRestricted"
        :label="t('apiRestricted')"
        :hint="t('apiRestrictedHint')"
        @update:model-value="apiRestrictionTouched = true"
      />
      <OaNumberField
        v-if="form.apiRestricted"
        v-model="form.apiRestrictionHours"
        :label="t('apiRestrictionHours')"
        :hint="t('apiRestrictionHoursHint')"
        :min="0"
        :max="8760"
        @update:model-value="apiRestrictionTouched = true"
      />
      <OaTextField
        v-model="form.newPassword"
        :label="t('setNewPassword')"
        type="password"
        :placeholder="t('keepPassword')"
        :hint="t('resetPasswordHint')"
      />
      <div class="oa-field">
        <span class="oa-field-label">{{ t('colTwoFactor') }}</span>
        <div class="oa-2fa-admin-row">
          <OaBadge :tone="account.two_factor_at ? 'default' : 'muted'">
            {{ account.two_factor_at ? t('twoFactorOn') : t('twoFactorOff') }}
          </OaBadge>
          <span v-if="account.two_factor_at" class="oa-field-hint">
            {{ t('twoFactorOnSince', { date: absoluteTime(account.two_factor_at) }) }}
          </span>
          <OaConfirmButton
            v-if="account.two_factor_at && !self"
            class="oa-btn"
            :label="t('twoFactorResetLabel')"
            :armed-label="t('confirmWord')"
            :armed-title="t('twoFactorResetConfirm', { name: maskUser(account.username) })"
            :resting-title="t('twoFactorResetLabel')"
            :disabled="busy"
            @confirm="resetTwoFactor"
          />
        </div>
        <span v-if="account.two_factor_at && !self" class="oa-field-hint">{{ t('twoFactorResetHint') }}</span>
        <span v-if="twoFactorFlash" class="oa-field-hint" role="status">{{ twoFactorFlash }}</span>
      </div>

      <OaFormSection :title="t('secAllowanceOverride')" :hint="t('allowanceOverrideHint')" />
      <OaSwitchField
        v-model="unlimitedQuota"
        :label="t('unlimitedQuota')"
        :hint="t('unlimitedQuotaHint')"
      />
      <OaNumberField
        v-model="form.rpm"
        :label="t('requestsPerMinute')"
        :placeholder="t('inherit')"
        :min="0"
      />
      <OaNumberField
        v-model="form.tpm"
        :label="t('tokensPerMinute')"
        :placeholder="t('inherit')"
        :min="0"
      />
      <template v-for="kind in WINDOWS" :key="kind">
        <OaFormSection :title="kind" />
        <OaSwitchField
          v-model="form.windows[kind].override"
          :label="t('overrideWindow', { window: kind })"
        />
        <OaSwitchField v-model="form.windows[kind].enabled" :label="t('enforceIt')" />
        <OaNumberField
          v-model="form.windows[kind].requests"
          :label="t('limitRequests')"
          :placeholder="t('noLimit')"
          :min="0"
        />
        <OaNumberField
          v-model="form.windows[kind].tokens"
          :label="t('limitTokens')"
          :placeholder="t('noLimit')"
          :min="0"
        />
        <CreditsField v-model="form.windows[kind].credits" />
      </template>

      <OaFormSection :title="t('apiKeys')" :hint="t('adminKeysHint')" />
      <div class="oa-keys-list">
        <p v-if="keys === null" class="oa-menu-empty">{{ t('loading') }}</p>
        <p v-else-if="!keys.length" class="oa-menu-empty">{{ t('keysEmpty') }}</p>
        <div v-for="key in keys ?? []" v-else :key="key.id" class="oa-key-row">
          <div class="oa-key-info">
            <div class="oa-key-title">
              <span class="oa-key-name">{{ key.name }}</span>
              <OaBadge
                v-if="key.expires_at > 0 && key.expires_at <= Date.now()"
                tone="danger"
              >{{ t('keyExpired') }}</OaBadge>
            </div>
            <div class="oa-key-meta">
              <code class="oa-key-prefix">{{ maskCredential(key.prefix) }}…</code>
              <span>
                {{ key.expires_at
                  ? t('keyExpiresAt', { when: absoluteTime(key.expires_at) })
                  : t('keyNoExpiry') }}
              </span>
              <span>
                {{ key.last_used_at
                  ? t('keyLastUsed', { when: relativeTime(key.last_used_at) })
                  : t('keyNeverUsed') }}
              </span>
            </div>
          </div>
          <!-- Revoking somebody else's credential asks first, in place, the
               way every other irreversible action in this interface does. -->
          <div class="oa-key-actions">
            <OaConfirmButton
              class="oa-icon-btn danger"
              :armed-label="t('keyRevokeConfirm')"
              :armed-title="t('keyRevoke')"
              :resting-title="t('keyRevoke')"
              @confirm="revokeKey(key)"
            >
              <IconTrash :size="15" />
            </OaConfirmButton>
          </div>
        </div>
      </div>

      <OaFormSection :title="t('secDevices')" :hint="t('adminSessionsHint')" />
      <div class="oa-keys-list">
        <p v-if="sessionsError" class="oa-menu-empty">{{ sessionsError }}</p>
        <p v-else-if="sessions === null" class="oa-menu-empty">{{ t('loading') }}</p>
        <p v-else-if="!sessions.length" class="oa-menu-empty">{{ t('devicesEmpty') }}</p>
        <div v-for="deviceSession in sessions ?? []" v-else :key="deviceSession.id" class="oa-key-row">
          <div class="oa-key-info">
            <div class="oa-key-title">
              <span class="oa-key-name">{{ describeUserAgent(deviceSession.user_agent) }}</span>
            </div>
            <div class="oa-key-meta">
              <span>{{ maskLog(deviceSession.ip) }}</span>
              <span>{{ t('deviceLastActive', { when: relativeTime(deviceSession.last_seen_at) }) }}</span>
            </div>
          </div>
          <!-- Signing somebody else out asks first, in place, the way every
               other irreversible action in this interface does. -->
          <div class="oa-key-actions">
            <OaConfirmButton
              class="oa-icon-btn danger"
              :armed-label="t('confirmWord')"
              :armed-title="t('deviceSignOutConfirm')"
              :resting-title="t('deviceSignOut')"
              :disabled="revokingSession === deviceSession.id"
              @confirm="revokeSession(deviceSession)"
            >
              <IconTrash :size="15" />
            </OaConfirmButton>
          </div>
        </div>
      </div>
      <OaConfirmButton
        v-if="sessions && sessions.length > 1"
        class="oa-btn"
        :label="t('signOutEverywhere')"
        :armed-label="t('confirmWord')"
        :armed-title="t('signOutEverywhereConfirm', { name: maskUser(account.username) })"
        :resting-title="t('signOutEverywhere')"
        :disabled="signOutAllBusy"
        @confirm="signOutEverywhere"
      />

      <OaFormSection :title="t('secConversations')" :hint="t('conversationsHint')" />
      <button type="button" class="oa-btn" @click="openConversations">
        {{ t('viewConversations') }}
      </button>
    </template>

    <template v-else-if="mode === 'conversations'">
      <p v-if="conversations === null" class="oa-field-hint">{{ t('loading') }}</p>
      <p v-else-if="!conversations.length" class="oa-field-hint">{{ t('noConversations') }}</p>
      <OaTable
        v-else
        :pagination="{ ...conversationPage, total: conversationTotal }"
        @page="changeConversationPage"
        :columns="conversationColumns"
        :rows="conversations"
        :empty="t('noConversations')"
        selectable
        @select="openTranscript($event)"
      />
    </template>

    <template v-else-if="mode === 'transcript'">
      <p v-if="transcript === null" class="oa-field-hint">{{ t('loading') }}</p>
      <div v-else class="oa-transcript">
        <div v-for="message in transcript" :key="message.id" class="oa-transcript-turn">
          <span class="oa-transcript-role">{{ turnLabel(message) }}</span>
          <!-- Plain text, not markdown: this is an audit view of what was
               stored, and a renderer would be interpreting it. -->
          {{ message.error || message.content || t('emptyMessage') }}
        </div>
      </div>
    </template>
  </OaPanel>
</template>
