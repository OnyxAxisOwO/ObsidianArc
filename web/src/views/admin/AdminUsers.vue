<script setup lang="ts">
// Users: search, edit, ban, reset, and read their conversations.
//
// That last one is an intrusion even when it is justified, so it sits behind
// its own click, says whose transcript it is, and leaves a line in the server
// log. Nothing about it is incidental to opening the account panel.

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useDebounceFn } from '@vueuse/core';
import {
  adminApi, emptyPolicy,
  type Account, type AdminSession, type ApiKey, type CardHolding, type Conversation,
  type Group, type Message, type QuotaWindowKind, type Role, type UsageBreakdown,
} from '@/admin/api';
import type { UsageSummary } from '@/api/usage';
import { ADMIN_PERMISSIONS } from '@/admin/permissions';
import OaCheckList from '@/components/OaCheckList.vue';
import type { PageState } from '@/components/table-types';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaBulkBar from '@/components/OaBulkBar.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaRow from '@/components/OaRow.vue';
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
import { cardTitle, formatCardScope, windowChoices } from '@/lib/card-scope';
import { DAY, HOUR } from './usage/scale';
import { toDateTimeLocal, failureText } from './shared';
import { useBulk } from '@/composables/useBulk';
import { usePanelSlot } from '@/composables/usePanelSlot';
import { IconTrash } from '@/icons';
import { absoluteTime, compactNumber, localeTag, relativeTime } from '@/lib/format';
import { rememberedPageSize } from '@/lib/page-size';
import { describeUserAgent } from '@/lib/ua';
import { currentUser, canAdmin, isSuperAdmin, siteInfo } from '@/stores/session';
import OaAccountFields from '@/components/OaAccountFields.vue';
import { allFields, fieldValues } from '@/lib/account-fields';
import { fieldSpec, plugins } from '@/plugins/registry';
import AdminControlCard from './AdminControlCard.vue';
import PluginUserActions from './PluginUserActions.vue';
import { isMasked, maskUser, maskLog, maskBilling, maskCredential } from '@/admin/safeMode';
import { QUOTA_WINDOWS as WINDOWS, windowFigure } from '@/admin/quota';
import CreditsField from './CreditsField.vue';
import ExpiryPresets from './ExpiryPresets.vue';
import UsageBoard from './usage/UsageBoard.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
// Null until the first list lands, so the heading shows no count before then.
const listedTotal = ref<number | null>(null);
view.setTitle(
  () => t('usersTitle'),
  () => (listedTotal.value === null ? '' : tn(listedTotal.value, 'accountsCountOne', 'accountsCountOther')),
);

const groups = ref<Pick<Group, 'id' | 'name'>[]>([]);
const users = ref<Account[]>([]);
const total = ref(0);
const paging = ref<PageState>({ page: 1, pageSize: rememberedPageSize() });
let request = 0;
function changePage(next: PageState): void { paging.value = next; void list(); }
function filterList(): void { paging.value.page = 1; void list(); }
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

// Once the page is left, a late list must not set the title: the heading is
// the shell's, and the page that replaced this one has already set its own.
let disposed = false;
onBeforeUnmount(() => { disposed = true; });

// Typing filters after a pause rather than on each keystroke: one request per
// word, not one per letter.
const debouncedList = useDebounceFn(() => { if (!disposed) void list(); }, 250);
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
    if (ticket !== request || disposed) return;
    if (result.total > 0 && (paging.value.page - 1) * paging.value.pageSize >= result.total) {
      paging.value.page = Math.ceil(result.total / paging.value.pageSize);
      await list(); return;
    }
    users.value = result.users ?? [];
    total.value = result.total;
    listedTotal.value = result.total;
  } catch (failure) {
    if (ticket !== request) return;
    listError.value = failureText(failure);
  } finally {
    if (ticket === request) listing.value = false;
  }
}

/**
 * A change that failed for an account the panel has since left. The panel now
 * belongs to another account, so the failure goes to the list, named, where the
 * operator will read it. list() clears listError as it starts, which is why the
 * message is written after it.
 */
function failedForAccount(row: Account, failure: unknown): void {
  void list();
  const message = failureText(failure);
  listError.value = t('accountChangeFailed', { name: maskUser(row.nickname || row.username), error: message });
}

/**
 * A change made to one account that failed: into the panel while it still
 * shows that account, otherwise to the list as failedForAccount does. Reads
 * keep their own handling, since a failed read has nothing to route to the list.
 */
function panelFailure(row: Account, failure: unknown): void {
  if (panelShows(row.id)) panelError.value = failureText(failure);
  else failedForAccount(row, failure);
}

// --- the account panel ------------------------------------------------------------

type PanelMode = 'account' | 'conversations' | 'transcript';

const { open: panelOpen, panel, key: panelKey, show: showPanel, hide: hidePanel, closed: panelClosed } = usePanelSlot();
const loadingDetail = ref(false);
// Whether the form below holds this account. Until it does the panel shows
// nothing editable: an empty form saved by mistake would clear real fields.
const detailReady = ref(false);
let opening = 0;
const mode = ref<PanelMode>('account');
const busy = ref(false);
const panelError = ref('');
// The reason typed for a ban, which belongs to the account on screen: it is
// cleared when another account is opened, so it cannot land on the wrong one.
const banDraft = ref('');
const banBusy = ref(false);

const account = ref<Account | null>(null);
// The account as the form was loaded, or as the last save left it. Save diffs
// the form against this and not against `account`, which refreshFacts re-reads
// for display: diffing against the fresher copy would send this form's older
// values back over a change made since the panel opened.
const baseline = ref<Account | null>(null);
const usage = ref<UsageSummary | null>(null);
const lifetime = ref<{ requests: number; total_tokens: number; credits: number } | null>(null);
const cards = ref<CardHolding | null>(null);
/** Every model the account has used, by tokens: what its spending goes on. */
const models = ref<UsageBreakdown[]>([]);
const keys = ref<ApiKey[] | null>(null);
const sessions = ref<AdminSession[] | null>(null);
// Kept apart from `keys` and `sessions` staying null: null-forever would read as
// "still loading" and an empty array would read as "none", and a failed read is
// neither. The same holds for keys, which used to be cleared to [] on failure.
const keysError = ref('');
// Kept apart from `sessions` staying null: null-forever would read as "still
// loading" and an empty array would read as "signed in nowhere", and a 403 on
// a restricted operator is neither.
const sessionsError = ref('');
const revokingSession = ref('');
const signOutAllBusy = ref(false);
const conversations = ref<Conversation[] | null>(null);
const conversationPage = ref<PageState>({ page: 1, pageSize: rememberedPageSize() });
const conversationTotal = ref(0);
async function changeConversationPage(next: PageState): Promise<void> { conversationPage.value = next; await openConversations(); }
const transcript = ref<Message[] | null>(null);
const transcriptTitle = ref('');
const apiRestrictionTouched = ref(false);

// What the grant form starts at, for each account the panel opens on. A
// function, not a constant: the expiry is thirty days from the moment it is
// asked, and a panel opened tomorrow should not offer yesterday's date.
function grantDefaults() {
  return {
    count: 1 as number | null,
    name: '',
    preset: 'full' as 'full' | '5h' | '1w' | '1m' | 'custom',
    custom: ['5h'],
    expiresAt: defaultGrantExpiry(),
  };
}
const initialGrant = grantDefaults();
const grantCount = ref(initialGrant.count);
const grantName = ref(initialGrant.name);
const grantScopePreset = ref(initialGrant.preset);
const grantCustomWindows = ref<string[]>(initialGrant.custom);
const grantExpiresAt = ref(initialGrant.expiresAt);
// The result line under the card buttons. Kept apart from the buttons' own
// state: a button that reads "granted" and stays disabled leaves nothing to
// click until the panel is reopened.
const grantLabel = ref('');
const granting = ref(false);
const rescheduleLabel = ref('');
// Whether a reschedule or a revocation is out. One at a time for the whole
// card list: either one re-reads the list when it lands.
const rescheduling = ref(false);

/**
 * Whether the panel still shows this account. An answer to a request sent for
 * one account, arriving after another was opened, must not be written into
 * the panel of the other.
 */
function panelShows(id: string): boolean {
  return account.value?.id === id;
}

function grantSelectedWindows(): string[] {
  if (grantScopePreset.value === 'custom') {
    return grantCustomWindows.value;
  }
  if (grantScopePreset.value === 'full') {
    return [];
  }
  return [grantScopePreset.value];
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

function defaultGrantExpiry(): string {
  return toDateTimeLocal(Date.now() + 30 * DAY);
}


const form = ref({
  nickname: '', email: '', fields: {} as Record<string, string>, bio: '', avatar: '',
  role: 'user' as Role,
  permissions: [] as string[],
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
// Whether an operator has edited a limit since the form was loaded. The policy
// is one row that a save replaces whole, so a Save that touched only the
// profile must not write the limits it was loaded with over another
// administrator's change to them. A switch flipped on and off again still
// counts: what the operator left on the form is what gets written.
let limitsEdited = false;
// Counts the edits, so a save can tell whether one came after it took its copy.
let limitEdits = 0;
watch(() => JSON.stringify(policyBody(form.value)), () => {
  if (!detailReady.value) return;
  limitsEdited = true;
  limitEdits += 1;
}, { flush: 'sync' });

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
      value: new Date(row.created_at).toLocaleDateString(localeTag()),
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
  banDraft.value = '';
  mode.value = 'account';
  conversationPage.value.page = 1;
  keys.value = null;
  keysError.value = '';
  sessions.value = null;
  sessionsError.value = '';
  grantLabel.value = '';
  granting.value = false;
  rescheduleLabel.value = '';
  rescheduling.value = false;
  const grantForm = grantDefaults();
  grantName.value = grantForm.name;
  grantScopePreset.value = grantForm.preset;
  grantCustomWindows.value = grantForm.custom;
  grantCount.value = grantForm.count;
  grantExpiresAt.value = grantForm.expiresAt;

  // The panel opens on the click, with the row the list already has, and
  // fills in when the detail arrives. It used to wait for the detail first:
  // an account with hundreds of thousands of ledger rows takes seconds to
  // total, nothing moved in the meantime, and a failure was written above
  // the list — out of sight of a reader scrolled down to the row — so the
  // click looked like it had done nothing at all.
  const ticket = ++opening;
  const listed = users.value.find((candidate) => candidate.id === id);
  account.value = listed ?? null;
  baseline.value = null;
  usage.value = null;
  lifetime.value = null;
  models.value = [];
  cards.value = null;
  loadingDetail.value = true;
  detailReady.value = false;
  // Saving leaves this set while the panel slides out.
  busy.value = false;
  showPanel();
  // The keys and devices need only the id, so they start with the detail
  // instead of after it. That is safe only when the list already has the row:
  // then the panel shows this account and can match their answers to it. With
  // no listed row they wait for the detail, which sets the account.
  if (listed) loadAccess(id);

  let detail;
  try {
    detail = await adminApi.user(id);
  } catch (failure) {
    if (ticket !== opening) return;
    loadingDetail.value = false;
    panelError.value = failureText(failure);
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
  baseline.value = row;
  usage.value = detail.usage;
  lifetime.value = detail.lifetime;
  models.value = detail.models ?? [];
  cards.value = detail.cards;
  apiRestrictionTouched.value = false;
  const restrictionActive = apiRestrictionActive(row);
  const restrictionHours = restrictionActive && row.api_restricted_until > 0
    ? Math.max(1, Math.ceil((row.api_restricted_until - Date.now()) / HOUR))
    : 0;
  form.value = {
    nickname: row.nickname,
    email: row.email,
    fields: { ...(row.fields ?? {}) },
    bio: row.bio,
    avatar: row.avatar,
    role: row.role,
    permissions: [...(row.admin_permissions ?? [])],
    group: row.group_id,
    groupExpiresAt: row.group_expires_at ? toDateTimeLocal(row.group_expires_at) : '',
    apiRestricted: restrictionActive,
    apiRestrictionHours: restrictionActive ? restrictionHours : 24,
    newPassword: '',
    rpm: policy.rpm,
    tpm: policy.tpm,
    windows,
  };
  limitsEdited = false;
  detailReady.value = true;

  if (!listed) loadAccess(id);
}

/**
 * The keys and devices are a second round trip after the detail, so each
 * answer is checked against the panel when it lands: opening another account
 * in the meantime must not show this one's keys under the other's name.
 */
function loadAccess(id: string): void {
  // The list and nothing else: the server keeps a digest, so there is no token
  // to show and no endpoint that could produce one. What an operator needs
  // here is to see that a key exists and to be able to revoke it.
  void adminApi.userKeys(id)
    .then(({ keys: list }) => {
      if (!panelShows(id)) return;
      keys.value = list;
      keysError.value = '';
    })
    .catch((failure: unknown) => {
      if (panelShows(id)) keysError.value = failureText(failure);
    });
  void adminApi.userSessions(id)
    .then(({ sessions: list }) => {
      if (!panelShows(id)) return;
      sessions.value = list;
      sessionsError.value = '';
    })
    .catch((failure: unknown) => {
      if (panelShows(id)) sessionsError.value = failureText(failure);
    });
}

/**
 * The panel slides away and the list is fetched again where it stands, rather
 * than the page being mounted again — which put the reader back at the top and
 * on the first page of accounts.
 */
function finish(): void {
  hidePanel();
  void list();
}

// --- several at once ----------------------------------------------------------------

const bulk = useBulk(() => users.value, (row) => row.id);
// Typed once for the whole selection. It survives a batch that partly failed,
// so the retry is one click rather than a second round of typing.
const bulkBanReason = ref('');

// The server refuses what must not be done — one's own account, the last
// administrator — and the bar shows its reason; nothing is pre-filtered here.
// A row already banned for this same reason is left as it is; every other row
// takes the reason, whatever it had before.
function bulkBan(): Promise<void> {
  const reason = bulkBanReason.value.trim();
  return bulk.run(
    (row) => (row.status === 'disabled' && (row.ban_reason ?? '') === reason
      ? Promise.resolve()
      : adminApi.updateUser(row.id, { status: 'disabled', ban_reason: reason })),
    list,
  ).then(() => { if (!bulk.error.value) bulkBanReason.value = ''; });
}

function bulkUnban(): Promise<void> {
  return bulk.run((row) => (row.status === 'active' ? Promise.resolve() : adminApi.updateUser(row.id, { status: 'active' })), list);
}

function bulkRemove(): Promise<void> {
  return bulk.run((row) => adminApi.deleteUser(row.id), list);
}

async function save(): Promise<void> {
  // The baseline, not the displayed account: the form is diffed against the
  // copy it was loaded from. The displayed account is set before the detail
  // lands, so a failed detail leaves it on a row the form does not hold.
  const row = baseline.value;
  if (!row || !detailReady.value) return;
  // The form belongs to whichever account was opened last, and that can be
  // another account while this save is in flight: the requests below await
  // in between, and opening an account replaces the form. Every value is read
  // from this copy, taken before the first await, so the save describes the
  // account it was sent for.
  const draft = JSON.parse(JSON.stringify(form.value)) as typeof form.value;
  const sendLimits = limitsEdited;
  const editsAtStart = limitEdits;
  busy.value = true;
  panelError.value = '';
  // Whether anything was written before the failure, which decides what the
  // error has to say: a save that half went through is not a failed save.
  let saved = false;
  try {
    // Only what the operator changed is sent. The panel can stay open while
    // another administrator edits this account, and resending every field would
    // write this form's older copy of their edit back over it.
    const patch: Record<string, unknown> = {};
    if (draft.nickname.trim() !== row.nickname) patch.nickname = draft.nickname.trim();
    if (draft.email.trim() !== row.email) patch.email = draft.email.trim();
    if (draft.bio.trim() !== row.bio) patch.bio = draft.bio.trim();
    if (draft.avatar.trim() !== row.avatar) patch.avatar = draft.avatar.trim();
    if (fieldPlan.value.keys.length) {
      const next = fieldValues(draft.fields, fieldPlan.value);
      const was = fieldValues(row.fields ?? {}, fieldPlan.value);
      const changed = Object.fromEntries(
        fieldPlan.value.keys.filter((key) => next[key] !== was[key]).map((key) => [key, next[key]!]),
      );
      if (Object.keys(changed).length) patch.fields = changed;
    }
    if (canAdmin('administrators') && draft.role !== row.role) patch.role = draft.role;
    if (canAdmin('administrators') && (draft.role === 'admin') &&
      (draft.role !== row.role || JSON.stringify(draft.permissions) !== JSON.stringify(row.admin_permissions ?? []))) {
      patch.admin_permissions = draft.permissions;
    }
    // Only send a membership change when the operator edited it. A profile
    // panel left open across expiry must not silently renew the old group.
    const originalExpiry = row.group_expires_at ? toDateTimeLocal(row.group_expires_at) : '';
    if (draft.group !== row.group_id || draft.groupExpiresAt !== originalExpiry) {
      const expiry = draft.groupExpiresAt ? new Date(draft.groupExpiresAt).getTime() : 0;
      if (draft.groupExpiresAt && (!Number.isFinite(expiry) || expiry <= Date.now())) {
        throw new Error(t('membershipExpiryInvalid'));
      }
      patch.group_id = draft.group;
      patch.group_expires_at = expiry;
    }
    if (apiRestrictionTouched.value) {
      patch.api_restricted = draft.apiRestricted;
      patch.api_restriction_hours = draft.apiRestrictionHours ?? 0;
    }
    // Sent even when empty: the server answers that as a no-op, and one path for
    // every Save keeps the errors it can raise the same whatever was edited.
    const result = await adminApi.updateUser(row.id, patch);
    // An empty patch writes nothing, so a failure in a later step must not be
    // reported as one that came after a save.
    if (Object.keys(patch).length) saved = true;
    if (currentUser.value?.id === row.id) currentUser.value = { ...currentUser.value, ...result.user };
    // The saved profile is the baseline for the next Save: a retry after a
    // later step failed sends only what is still different from it. The save
    // can outlive the panel's account, so the baseline moves only if it is
    // still this one.
    if (baseline.value?.id === row.id) baseline.value = { ...baseline.value, ...result.user };
    applyAccount(row.id, result.user);

    if (draft.newPassword) {
      await adminApi.resetPassword(row.id, draft.newPassword);
      saved = true;
    }

    if ((canAdmin('users') || canAdmin('usage')) && sendLimits) {
      await adminApi.savePolicy({ scope: 'user', scope_id: row.id, ...policyBody(draft) });
      saved = true;
      // An edit made after this save took its copy is not in what was written,
      // so the limits stay marked as edited for the next Save.
      if (panelShows(row.id)) limitsEdited = limitEdits !== editsAtStart;
    }

    // Closing the panel is for the account it showed when the save began. If
    // another one is shown now, its panel stays and the list is refreshed.
    if (panelShows(row.id)) finish(); else void list();
  } catch (failure) {
    // The failure belongs to the account that was saved; a panel now showing
    // another one keeps its own busy state and error, and the failure is
    // written to the list instead.
    if (!panelShows(row.id)) { failedForAccount(row, failure); return; }
    busy.value = false;
    const message = failureText(failure);
    panelError.value = saved ? t('savedBeforeError', { error: message }) : message;
  }
}

/** The limits in the shape the policy endpoint takes them. */
function policyBody(source: typeof form.value) {
  return {
    rpm: source.rpm,
    tpm: source.tpm,
    // The form has no windows until an account is opened, and the change
    // watcher reads this before then.
    windows: Object.fromEntries(WINDOWS.map((kind) => {
      const limits = source.windows[kind];
      return [kind, limits?.override
        ? {
            enabled: limits.enabled,
            requests: windowFigure(limits.requests),
            tokens: windowFigure(limits.tokens),
            credits: windowFigure(limits.credits),
          }
        : { enabled: null, requests: null, tokens: null, credits: null }];
    })),
  };
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
    // The row is the account the reset was sent for, but the panel may show
    // another one by now: applyAccount only touches the panel for this id.
    applyAccount(row.id, user);
    if (panelShows(row.id)) twoFactorFlash.value = t('twoFactorResetDone');
  } catch (failure) {
    panelFailure(row, failure);
  }
}

/**
 * Banning and unbanning commit on their own buttons, as two-step reset does.
 * Neither is a field the form saves, so Save can neither ban an account nor
 * quietly undo a ban made since the panel was opened.
 */
async function ban(): Promise<void> {
  const row = account.value;
  if (!row) return;
  banBusy.value = true;
  panelError.value = '';
  try {
    const { user } = await adminApi.updateUser(row.id, { status: 'disabled', ban_reason: banDraft.value.trim() });
    applyAccount(row.id, user);
    if (!panelShows(row.id)) return;
    banDraft.value = '';
    // The ban signed every device out in the same transaction, so the devices
    // the panel shows are none.
    sessions.value = [];
  } catch (failure) {
    panelFailure(row, failure);
  } finally {
    banBusy.value = false;
  }
}

async function unban(): Promise<void> {
  const row = account.value;
  if (!row) return;
  banBusy.value = true;
  panelError.value = '';
  try {
    const { user } = await adminApi.updateUser(row.id, { status: 'active' });
    applyAccount(row.id, user);
  } catch (failure) {
    panelFailure(row, failure);
  } finally {
    banBusy.value = false;
  }
}

/** The list row and the open panel both show the account, so both take the change. */
function applyAccount(id: string, fresh: Account): void {
  users.value = users.value.map((entry) => (entry.id === id ? { ...entry, ...fresh } : entry));
  if (account.value?.id === id) account.value = { ...account.value, ...fresh };
}

/** What the status row says about the reason, or nothing while the account is active. */
const banSummary = computed<string | undefined>(() => {
  const row = account.value;
  if (!row || row.status !== 'disabled') return undefined;
  return row.ban_reason ? t('banReasonLine', { reason: row.ban_reason }) : t('banNoReason');
});

async function remove(): Promise<void> {
  const row = account.value;
  if (!row) return;
  busy.value = true;
  try {
    await adminApi.deleteUser(row.id);
    // As in save(): the panel closes only if it still shows the deleted account.
    if (panelShows(row.id)) finish(); else void list();
  } catch (failure) {
    if (!panelShows(row.id)) { failedForAccount(row, failure); return; }
    busy.value = false;
    panelError.value = failureText(failure);
  }
}

// --- what plugins add ---------------------------------------------------------

// The account fields plugins added. Every one of them, whatever sign-up asks
// for: an operator is correcting the value, not registering it.
const fieldPlan = computed(() => allFields(siteInfo.value));

/** "QQ 12345"-style parts for the list's second line, masked like the name. */
function fieldSummary(values: Record<string, string> | undefined): string[] {
  return fieldPlan.value.keys
    .filter((key) => values?.[key])
    .map((key) => `${fieldSpec(key)!.label()} ${maskUser(values![key])}`);
}

const userActions = computed(() => plugins().flatMap((plugin) => plugin.userActions ?? []));

function actionDone(closePanel: boolean): void {
  if (closePanel) {
    finish();
    return;
  }
  // The panel stays, so what it shows is fetched again: the action changed
  // some of it. The form is not rebuilt from the server, which would throw
  // away edits nobody has saved yet.
  void list();
  if (account.value) void refreshFacts(account.value.id);
}

/**
 * Re-reads what a plugin action can change without the operator's form
 * changing with it: the account's status, role and group, the figures, the
 * cards and the keys and devices. The account is replaced for display only; Save
 * compares the form against the baseline, which this does not move, so a field
 * the operator has not touched is never sent back.
 */
async function refreshFacts(id: string): Promise<void> {
  loadAccess(id);
  try {
    const detail = await adminApi.user(id);
    if (!panelShows(id)) return;
    account.value = detail.user;
    usage.value = detail.usage;
    lifetime.value = detail.lifetime;
    models.value = detail.models ?? [];
    cards.value = detail.cards;
  } catch (failure) {
    if (panelShows(id)) panelError.value = failureText(failure);
  }
}

function actionFailed(message: string): void {
  panelError.value = message;
}

/**
 * Straight to this account, without a code in between. Beside the figures it
 * changes, because "why does this person have no allowance left" and "give
 * them another" are one thought.
 */
/**
 * The date in the field both a grant and a reschedule use, or null with the
 * panel saying why: a date already past would hand out cards that are spent
 * before anyone could use them.
 */
function chosenExpiry(): number | null {
  const expiresAt = new Date(grantExpiresAt.value).getTime();
  if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
    panelError.value = t('grantCardExpiryInvalid');
    return null;
  }
  return expiresAt;
}

async function grant(): Promise<void> {
  const row = account.value;
  if (!row) return;
  if (grantScopePreset.value === 'custom' && grantCustomWindows.value.length === 0) {
    panelError.value = t('cardResetScopeRequired');
    return;
  }
  const count = grantCount.value ?? 1;
  const expiresAt = chosenExpiry();
  if (expiresAt === null) return;
  granting.value = true;
  grantLabel.value = '';
  try {
    await adminApi.grantCards(row.id, {
      cards: count,
      expires_at: expiresAt,
      name: grantName.value.trim(),
      windows: grantSelectedWindows(),
    });
    if (panelShows(row.id)) grantLabel.value = t('granted', { count });
    await refreshCards(row.id);
  } catch (failure) {
    panelFailure(row, failure);
  } finally {
    if (panelShows(row.id)) granting.value = false;
  }
}

/**
 * Re-reads the cards after one of them changed. The change has already been
 * made when this runs, so a failed re-read is not reported as the change
 * failing: the cards on screen are the last ones read, and the next open reads
 * them again.
 */
async function refreshCards(id: string): Promise<void> {
  // Once the panel has moved on, no card list on screen can show the answer,
  // and the detail behind it is the expensive read on the server.
  if (!panelShows(id)) return;
  try {
    const detail = await adminApi.user(id);
    if (panelShows(id)) cards.value = detail.cards;
  } catch {
    // See above: there is nothing for the operator to act on.
  }
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
  const expiresAt = chosenExpiry();
  if (expiresAt === null) return;

  rescheduling.value = true;
  rescheduleLabel.value = '';
  try {
    const result = await adminApi.rescheduleCards(row.id, {
      expires_at: expiresAt,
      ...(cardIDs?.length ? { card_ids: cardIDs } : {}),
    });
    if (panelShows(row.id)) rescheduleLabel.value = t('cardsMoved', { count: result.moved });
    // Re-read rather than patch the list in place: a lapsed card that was
    // moved forward is spendable again, and the counts above it move with it.
    await refreshCards(row.id);
  } catch (failure) {
    panelFailure(row, failure);
  } finally {
    // A request for an account the panel has left must not clear the flag of
    // the one running for the account it shows now.
    if (panelShows(row.id)) rescheduling.value = false;
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
  rescheduling.value = true;
  rescheduleLabel.value = '';
  try {
    // Silent on purpose, like the grant of a card: an operator undoing a
    // mistake should not be told about it. The list shows what is left.
    await adminApi.revokeCard(row.id, cardID);
    await refreshCards(row.id);
  } catch (failure) {
    panelFailure(row, failure);
  } finally {
    if (panelShows(row.id)) rescheduling.value = false;
  }
}

function revokeKey(key: ApiKey): void {
  const row = account.value;
  if (!row) return;
  void adminApi.revokeUserKey(row.id, key.id)
    .then(() => { if (panelShows(row.id)) keys.value = (keys.value ?? []).filter((entry) => entry.id !== key.id); })
    .catch((failure: unknown) => {
      panelFailure(row, failure);
    });
}

function revokeSession(session: AdminSession): void {
  const row = account.value;
  if (!row) return;
  revokingSession.value = session.id;
  adminApi.revokeUserSession(row.id, session.id)
    .then(() => { if (panelShows(row.id)) sessions.value = (sessions.value ?? []).filter((entry) => entry.id !== session.id); })
    .catch((failure: unknown) => {
      panelFailure(row, failure);
    })
    .finally(() => { revokingSession.value = ''; });
}

function signOutEverywhere(): void {
  const row = account.value;
  if (!row) return;
  signOutAllBusy.value = true;
  adminApi.revokeAllUserSessions(row.id)
    .then(() => { if (panelShows(row.id)) sessions.value = []; })
    .catch((failure: unknown) => {
      panelFailure(row, failure);
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
    if (!panelShows(row.id)) return;
    conversations.value = result.conversations;
    conversationTotal.value = result.total;
  } catch (failure) {
    if (panelShows(row.id)) panelError.value = failureText(failure);
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
    // An audit view of one person's messages must not sit under another
    // account's title: the answer is shown only while its account is.
    if (panelShows(row.id)) transcript.value = messages;
  } catch (failure) {
    if (panelShows(row.id)) panelError.value = failureText(failure);
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
  // The group names only label rows, so their failure must not stand in for the
  // whole table. It is reported above the rows, where a list failure goes, and
  // the rows still load. list() clears listError as it starts, hence the order.
  let namesFailure = '';
  try {
    ({ groups: groups.value } = await adminApi.groupOptions());
  } catch (failure) {
    namesFailure = failureText(failure);
  }
  loaded.value = true;
  await list();
  if (namesFailure && !listError.value) listError.value = namesFailure;
}

onMounted(load);
</script>

<script lang="ts">
const state = { q: '', role: '', status: '', group: '' };
</script>

<template>
  <p v-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

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

    <p v-if="listing && !users.length" class="oa-table-empty">{{ t('loading') }}</p>
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
      multi
      v-model:selected="bulk.selected.value"
      :row-key="(row) => row.id"
      @select="open($event.id)"
    >
      <template #cell-account="{ row }">
        <OaCellStack
          :title="maskUser(row.nickname || row.username)"
          :sub="[`@${maskUser(row.username)}`, ...fieldSummary(row.fields)].join(' · ')"
        />
      </template>
      <template #cell-role="{ row }">
        <OaBadgeRow>
          <OaBadge v-if="row.role === 'super_admin'">{{ t('superAdmin') }}</OaBadge>
          <OaBadge v-if="row.role === 'admin'">{{ t('admin') }}</OaBadge>
          <OaBadge v-if="row.status === 'disabled'" tone="danger" :title="row.ban_reason || undefined">{{ t('statusDisabled') }}</OaBadge>
          <OaBadge v-if="apiRestrictionActive(row)" tone="warning">{{ t('apiRestrictedBadge') }}</OaBadge>
          <OaBadge v-if="row.two_factor_at" tone="muted">{{ t('twoFactorBadge') }}</OaBadge>
        </OaBadgeRow>
      </template>
    </OaTable>
    <OaBulkBar
      :count="bulk.selected.value.length"
      :total="users.length"
      :busy="bulk.busy.value"
      :error="bulk.error.value"
      deletable
      @all="bulk.selectAll"
      @clear="bulk.clear"
      @delete="bulkRemove"
    >
      <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkUnban">{{ t('unbanAccount') }}</button>
      <input
        v-model="bulkBanReason"
        class="oa-bulk-reason"
        type="text"
        maxlength="500"
        :placeholder="t('banReasonPlaceholder')"
        :aria-label="t('banReason')"
        :disabled="bulk.busy.value"
      >
      <!-- A ban signs every ticked account out, so it asks twice: the first
           click arms it, and the armed title names how many it will reach. -->
      <OaConfirmButton
        class="oa-btn small oa-btn-danger"
        :label="t('bulkBan')"
        :armed-title="t('bulkBanConfirm', { count: bulk.selected.value.length })"
        :disabled="bulk.busy.value"
        @confirm="bulkBan"
      />
    </OaBulkBar>
  </template>

  <OaPanel
    v-if="panelOpen && account"
    ref="panel"
    :key="panelKey"
    :title="panelTitle"
    :width="mode === 'transcript' ? 480 : 440"
    :footer="mode === 'account'"
    :cancel-label="mode === 'account' ? undefined : t('close')"
    :confirm-label="t('save')"
    :confirmable="mode !== 'account' || detailReady || loadingDetail"
    :destructive-label="mode === 'account' && !self ? t('deleteLabel') : undefined"
    :destructive-confirm="mode === 'account' && !self
      ? t('confirmDeleteUser', { name: maskUser(account.username) })
      : undefined"
    :back="mode !== 'account'"
    :busy="busy || loadingDetail"
    :error="panelError"
    @close="panelClosed"
    @confirm="save"
    @destructive="remove"
    @back="mode === 'transcript' ? openConversations() : (mode = 'account')"
  >
    <p v-if="mode === 'account' && loadingDetail" class="oa-table-empty">{{ t('loading') }}</p>
    <template v-else-if="mode === 'account' && detailReady">
      <OaStatGrid :stats="summaryStats" />

      <!-- Every section is a card of rows, the shape the settings screens
           use: a label and its answer side by side, lists as rows with their
           action at the end, fields stacked with their own explanation. -->
      <AdminControlCard :title="t('account')">
        <OaRow v-for="[label, value, mono] in identity" :key="label" class="oa-fact-row" :title="label">
          <span class="oa-row-value" :class="{ mono }" :title="String(value)">{{ value }}</span>
        </OaRow>
      </AdminControlCard>

      <!-- Banning is an action with a reason, not a field of the form. It sits
           near the top so it is found without scrolling past the grants, and it
           commits on its own button. The reason is what the account sees when
           it tries to sign in. -->
      <AdminControlCard id="secBanStatus" :title="t('secBanStatus')" :hint="self ? t('cannotBanSelf') : t('banHint')">
        <OaRow
          :title="account.status === 'disabled' ? t('statusDisabled') : t('statusActive')"
          :meta="banSummary"
        >
          <button
            v-if="account.status === 'disabled' && !self"
            type="button"
            class="oa-btn small"
            :disabled="banBusy"
            @click="unban"
          >{{ t('unbanAccount') }}</button>
        </OaRow>
        <template v-if="account.status !== 'disabled' && !self">
          <OaTextField
            v-model="banDraft"
            :label="t('banReason')"
            :placeholder="t('banReasonPlaceholder')"
            :hint="t('banReasonHint')"
            :max-length="500"
          />
          <OaRow>
            <!-- Never window.confirm: the first click arms it and the second acts,
                 the same two steps every other destructive button here takes. -->
            <OaConfirmButton
              class="oa-btn small oa-btn-danger oa-ban-confirm"
              :label="t('banAccount')"
              :armed-label="t('confirmWord')"
              :armed-title="t('banAccountConfirm', { name: maskUser(account.username) })"
              :resting-title="t('banAccount')"
              :disabled="banBusy"
              @confirm="ban"
            />
          </OaRow>
        </template>
      </AdminControlCard>

      <!-- What the lifetime figures above were spent on. The question after
           "how much" is "on what", and it is cheaper to answer here than to
           send the operator to the usage page to narrow it by hand. -->
      <AdminControlCard v-if="models.length" :title="t('boardTheirModels')" :hint="t('boardTheirModelsHint')">
        <template v-if="canAdmin('usage') && view.open" #actions>
          <button type="button" class="oa-btn small" @click="view.open?.('/admin/usage', { user: account.id })">{{ t('viewInUsage') }}</button>
        </template>
        <UsageBoard
          :rows="models" kind="model" metric="tokens" :limit="4" :reach="false"
          :selectable="canAdmin('usage') && !!view.open" :empty-text="t('nothingYet')"
          @select="view.open?.('/admin/usage', { user: account.id, model: $event })"
        />
      </AdminControlCard>

      <!-- The same bars the account sees in its own composer, from the same
           summary: an administrator answering "why can this person not send
           anything" should be reading the figure the person is up against. -->
      <AdminControlCard v-if="usage" :title="t('secAllowance')">
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
      </AdminControlCard>

      <!-- What they are holding, before the control that adds more: an
           operator is usually here because somebody asked, and "you already
           have two" is the answer more often than a third card is. -->
      <AdminControlCard id="secHeldCards" v-if="cards" :title="t('secHeldCards')">
        <OaRow v-if="cards.total === 0" :meta="t('cardsNone')" />
        <template v-else>
          <!-- The three together, because "none left" and "never had any" are
               different answers and the first number cannot tell them apart. -->
          <OaRow
            :title="t('cardsAvailable', { count: cards.available })"
            :meta="t('cardsBreakdown', { used: cards.used, expired: cards.expired, total: cards.total })"
          />
          <OaRow v-for="card in cards.cards" :key="card.id" class="oa-held-card" :title="cardTitle(card)" :meta="cardSubtitle(card)">
            <!-- Moves this one onto the date in the card below. One card at a
                 time is the "this one runs out tomorrow" case; the button
                 under the picker is the one that catches the expired ones too. -->
            <button
              type="button"
              class="oa-btn small oa-card-reschedule"
              :disabled="rescheduling"
              @click="reschedule([card.id])"
            >{{ t('rescheduleOne') }}</button>
            <!-- Never window.confirm: it answers false on its own in some
                 browsers, which would turn this into a button that silently
                 does nothing. -->
            <OaConfirmButton
              class="oa-btn small oa-card-drop"
              :label="t('revokeCard')"
              :armed-label="t('revokeCardConfirm')"
              :armed-title="t('revokeCardConfirm')"
              :resting-title="t('revokeCard')"
              :disabled="rescheduling"
              @confirm="revoke(card.id)"
            />
          </OaRow>
        </template>
      </AdminControlCard>

      <AdminControlCard :title="t('grantCards')">
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
          :items="windowChoices()"
        />
        <OaNumberField
          v-model="grantCount"
          :label="t('grantCards')"
          :min="1"
          :hint="t('grantCardsHint')"
        />
        <!-- The date and its shortcuts are one control, so one row. -->
        <div class="oa-field-with-presets">
          <OaTextField
            v-model="grantExpiresAt"
            :label="t('grantCardExpiry')"
            :hint="t('grantCardExpiryHint')"
            type="datetime-local"
            required
          />
          <ExpiryPresets @pick="grantExpiresAt = $event" />
        </div>
        <OaRow>
          <button
            type="button"
            class="oa-btn small"
            :disabled="!movableCards || rescheduling"
            @click="reschedule()"
          >{{ t('rescheduleCards', { count: movableCards }) }}</button>
          <button type="button" class="oa-btn small primary" :disabled="granting" @click="grant">
            {{ t('grantCards') }}
          </button>
        </OaRow>
        <p v-if="grantLabel" class="oa-group-flash ok" role="status">{{ grantLabel }}</p>
        <p v-if="rescheduleLabel" class="oa-group-flash ok" role="status">{{ rescheduleLabel }}</p>
        <p class="oa-field-hint">{{ t('rescheduleCardsHint') }}</p>
      </AdminControlCard>

      <AdminControlCard id="secProfile" :title="t('secProfile')">
        <!-- Masking these would break editing them, so the value stays real
             and only its focus state decides whether it can be read: blurred
             until the operator actually clicks in, same as a screen share
             would need. -->
        <OaTextField v-model="form.nickname" :class="{ 'oa-safe-blur': isMasked('users') }" :label="t('nickname')" :max-length="32" />
        <OaTextField v-model="form.email" :class="{ 'oa-safe-blur': isMasked('users') }" :label="t('email')" type="email" />
        <OaAccountFields v-model="form.fields" :class="{ 'oa-safe-blur': isMasked('users') }" :plan="fieldPlan" />
        <OaTextArea v-model="form.bio" :label="t('bio')" :rows="2" />
        <OaTextField
          v-model="form.avatar"
          :class="{ 'oa-safe-blur': isMasked('users') }"
          :label="t('avatar')"
          :placeholder="t('avatarPlaceholder')"
          :hint="t('avatarHint')"
        />
      </AdminControlCard>

      <AdminControlCard id="secAccess" :title="t('secAccess')">
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
          v-model="form.group"
          :label="t('group')"
          :options="groups.map((entry) => ({ value: entry.id, label: entry.name }))"
          @update:model-value="form.groupExpiresAt = ''"
        />
        <div class="oa-field-with-presets">
          <OaTextField
            v-model="form.groupExpiresAt"
            type="datetime-local"
            :label="t('membershipExpiry')"
            :hint="t('membershipExpiryHint')"
          />
          <ExpiryPresets permanent @pick="form.groupExpiresAt = $event" />
        </div>
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
      </AdminControlCard>

      <AdminControlCard :title="t('secSecurity')">
        <OaTextField
          v-model="form.newPassword"
          :label="t('setNewPassword')"
          type="password"
          :placeholder="t('keepPassword')"
          :hint="t('resetPasswordHint')"
        />
        <OaRow
          :title="t('colTwoFactor')"
          :meta="account.two_factor_at
            ? t('twoFactorOnSince', { date: absoluteTime(account.two_factor_at) })
            : t('twoFactorOff')"
        >
          <OaConfirmButton
            v-if="account.two_factor_at && !self"
            class="oa-btn small"
            :label="t('twoFactorResetLabel')"
            :armed-label="t('confirmWord')"
            :armed-title="t('twoFactorResetConfirm', { name: maskUser(account.username) })"
            :resting-title="t('twoFactorResetLabel')"
            :disabled="busy"
            @confirm="resetTwoFactor"
          />
          <OaBadge v-else :tone="account.two_factor_at ? 'default' : 'muted'">
            {{ account.two_factor_at ? t('twoFactorOn') : t('twoFactorOff') }}
          </OaBadge>
        </OaRow>
        <p v-if="account.two_factor_at && !self" class="oa-field-hint">{{ t('twoFactorResetHint') }}</p>
        <p v-if="twoFactorFlash" class="oa-group-flash ok" role="status">{{ twoFactorFlash }}</p>
      </AdminControlCard>

      <AdminControlCard id="secAllowanceOverride" :title="t('secAllowanceOverride')" :hint="t('allowanceOverrideHint')">
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
        <!-- Each window's numbers only once it is overridden: three windows
             of four fields each, all showing, buried the switch that decides
             whether any of them count. -->
        <template v-for="kind in WINDOWS" :key="kind">
          <OaSwitchField
            v-model="form.windows[kind].override"
            :label="t('overrideWindow', { window: kind })"
          />
          <template v-if="form.windows[kind].override">
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
        </template>
      </AdminControlCard>

      <AdminControlCard :title="t('apiKeys')" :hint="t('adminKeysHint')">
        <OaRow v-if="keysError" :meta="keysError" />
        <OaRow v-else-if="keys === null" :meta="t('loading')" />
        <OaRow v-else-if="!keys.length" :meta="t('keysEmpty')" />
        <OaRow v-for="key in keys ?? []" v-else :key="key.id">
          <template #text>
            <span class="oa-group-row-title">
              {{ key.name }}
              <OaBadge v-if="key.expires_at > 0 && key.expires_at <= Date.now()" tone="danger">{{ t('keyExpired') }}</OaBadge>
            </span>
            <span class="oa-group-row-meta">
              <code class="oa-key-prefix">{{ maskCredential(key.prefix) }}…</code>
              · {{ key.expires_at ? t('keyExpiresAt', { when: absoluteTime(key.expires_at) }) : t('keyNoExpiry') }}
              · {{ key.last_used_at ? t('keyLastUsed', { when: relativeTime(key.last_used_at) }) : t('keyNeverUsed') }}
            </span>
          </template>
          <!-- Revoking somebody else's credential asks first, in place, the
               way every other irreversible action in this interface does. -->
          <OaConfirmButton
            class="oa-icon-btn danger"
            :armed-label="t('keyRevokeConfirm')"
            :armed-title="t('keyRevoke')"
            :resting-title="t('keyRevoke')"
            @confirm="revokeKey(key)"
          >
            <IconTrash :size="15" />
          </OaConfirmButton>
        </OaRow>
      </AdminControlCard>

      <AdminControlCard :title="t('secDevices')" :hint="t('adminSessionsHint')">
        <OaRow v-if="sessionsError" :meta="sessionsError" />
        <OaRow v-else-if="sessions === null" :meta="t('loading')" />
        <OaRow v-else-if="!sessions.length" :meta="t('devicesEmpty')" />
        <OaRow
          v-for="deviceSession in sessions ?? []"
          v-else
          :key="deviceSession.id"
          :title="describeUserAgent(deviceSession.user_agent)"
          :meta="`${maskLog(deviceSession.ip)} · ${t('deviceLastActive', { when: relativeTime(deviceSession.last_seen_at) })}`"
        >
          <!-- Signing somebody else out asks first, in place, the way every
               other irreversible action in this interface does. -->
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
        </OaRow>
        <OaRow v-if="sessions && sessions.length > 1">
          <OaConfirmButton
            class="oa-btn small"
            :label="t('signOutEverywhere')"
            :armed-label="t('confirmWord')"
            :armed-title="t('signOutEverywhereConfirm', { name: maskUser(account.username) })"
            :resting-title="t('signOutEverywhere')"
            :disabled="signOutAllBusy"
            @confirm="signOutEverywhere"
          />
        </OaRow>
      </AdminControlCard>

      <!-- A plugin's account-ending actions, never offered on oneself: the
           same rule every other one here keeps. -->
      <template v-if="!self">
        <PluginUserActions
          v-for="spec in userActions"
          :key="spec.id"
          :spec="spec"
          :user-id="account.id"
          :username="account.username"
          @done="actionDone"
          @failed="actionFailed"
        />
      </template>

      <AdminControlCard id="secConversations" :title="t('secConversations')">
        <OaRow :meta="t('conversationsHint')">
          <button type="button" class="oa-btn small" @click="openConversations">
            {{ t('viewConversations') }}
          </button>
        </OaRow>
      </AdminControlCard>
    </template>

    <template v-else-if="mode === 'conversations'">
      <!-- A failed read leaves the list unloaded: neither the loading line nor an
           empty list is true then, and the error above says what happened. -->
      <p v-if="conversations === null && !panelError" class="oa-field-hint">{{ t('loading') }}</p>
      <p v-else-if="conversations && !conversations.length" class="oa-field-hint">{{ t('noConversations') }}</p>
      <OaTable
        v-else-if="conversations"
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
      <p v-if="transcript === null && !panelError" class="oa-field-hint">{{ t('loading') }}</p>
      <div v-else-if="transcript" class="oa-transcript">
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
