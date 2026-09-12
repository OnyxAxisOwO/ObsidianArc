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
  type Account, type AccountStatus, type ApiKey, type CardHolding, type Conversation,
  type Group, type Message, type QuotaWindowKind, type Role,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import type { UsageSummary } from '@/api/usage';
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
import { currentUser } from '@/stores/session';
import AdminFailure from './AdminFailure.vue';
import CreditsField from './CreditsField.vue';
import { useAdminView } from './adminView';

const WINDOWS: QuotaWindowKind[] = ['5h', '1w', '1m'];

const view = useAdminView();
view.setTitle(t('usersTitle'));

const groups = ref<Group[]>([]);
const users = ref<Account[]>([]);
const total = ref(0);
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
  { key: 'account', header: t('colAccount') },
  { key: 'email', header: t('colEmail'), text: (row) => row.email || '—', secondary: true, width: '160px' },
  { key: 'group', header: t('colGroup'), text: (row) => groupName(row.group_id), width: '120px' },
  { key: 'role', header: t('colRole'), width: '120px' },
  { key: 'seen', header: t('colLastSeen'), text: (row) => relativeTime(row.last_login_at), secondary: true, width: '110px' },
]);

function groupName(id: string): string {
  return groups.value.find((group) => group.id === id)?.name ?? '—';
}

// Typing filters after a pause rather than on each keystroke: one request per
// word, not one per letter.
const debouncedList = useDebounceFn(() => void list(), 250);
watch(() => filters.value.q, () => void debouncedList());

async function list(): Promise<void> {
  Object.assign(state, filters.value);
  listing.value = true;
  listError.value = '';

  const query = new URLSearchParams();
  if (filters.value.q) query.set('q', filters.value.q.trim());
  if (filters.value.role) query.set('role', filters.value.role);
  if (filters.value.status) query.set('status', filters.value.status);
  if (filters.value.group) query.set('group_id', filters.value.group);

  try {
    const result = await adminApi.users(query.toString() ? `?${query}` : '');
    users.value = result.users ?? [];
    total.value = result.total;
    view.setTitle(t('usersTitle'), tn(result.total, 'accountsCountOne', 'accountsCountOther'));
  } catch (failure) {
    listError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    listing.value = false;
  }
}

// --- the account panel ------------------------------------------------------------

type PanelMode = 'account' | 'conversations' | 'transcript';

const panelOpen = ref(false);
const mode = ref<PanelMode>('account');
const busy = ref(false);
const panelError = ref('');

const account = ref<Account | null>(null);
const usage = ref<UsageSummary | null>(null);
const lifetime = ref<{ requests: number; total_tokens: number; credits: number } | null>(null);
const cards = ref<CardHolding | null>(null);
const keys = ref<ApiKey[] | null>(null);
const conversations = ref<Conversation[] | null>(null);
const transcript = ref<Message[] | null>(null);
const transcriptTitle = ref('');
const apiRestrictionTouched = ref(false);

const grantCount = ref<number | null>(1);
const grantLabel = ref('');

const form = ref({
  nickname: '', email: '', qq: '', bio: '', avatar: '',
  role: 'user' as Role,
  status: 'active' as AccountStatus,
  group: '',
  apiRestricted: false,
  apiRestrictionHours: 24 as number | null,
  newPassword: '',
  rpm: null as number | null,
  windows: {} as Record<QuotaWindowKind, {
    override: boolean; enabled: boolean;
    requests: number | null; tokens: number | null; credits: number | null;
  }>,
});

const self = computed(() => currentUser.value?.id === account.value?.id);

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
    { label: t('statRequests'), value: compactNumber(totals.requests), note: t('lifetimeAllTime') },
    { label: t('statTokens'), value: compactNumber(totals.total_tokens) },
    { label: t('statCredits'), value: compactNumber(totals.credits) },
    {
      label: t('joined'),
      value: new Date(row.created_at).toLocaleDateString(),
      note: row.last_login_at
        ? t('lastSeenAt', { when: relativeTime(row.last_login_at) })
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
    [t('colUID'), row.id, true],
    [t('colRegistered'), absoluteTime(row.created_at), false],
    [t('colLastSeen'), row.last_login_at ? absoluteTime(row.last_login_at) : t('neverSignedIn'), false],
  ];
  // Only where it was recorded: accounts predating the column have none, and
  // an empty row reads as a missing value rather than an absent one.
  if (row.signup_ip) rows.push([t('colSignupIP'), row.signup_ip, true]);
  if (row.signup_user_agent) rows.push([t('registrationUserAgent'), row.signup_user_agent, true]);
  return rows;
});

const conversationColumns = computed<Array<Column<Conversation>>>(() => [
  { key: 'title', header: t('colTitle'), text: (row) => row.title || t('untitled') },
  { key: 'messages', header: t('colMessages'), text: (row) => String(row.message_count), numeric: true },
  { key: 'updated', header: t('colUpdated'), text: (row) => relativeTime(row.updated_at) },
]);

async function open(id: string): Promise<void> {
  panelError.value = '';
  mode.value = 'account';
  keys.value = null;
  grantLabel.value = '';
  grantCount.value = 1;

  let detail;
  try {
    detail = await adminApi.user(id);
  } catch (failure) {
    listError.value = failure instanceof ApiError ? failure.message : String(failure);
    return;
  }

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
    status: row.status,
    group: row.group_id,
    apiRestricted: restrictionActive,
    apiRestrictionHours: restrictionActive ? restrictionHours : 24,
    newPassword: '',
    rpm: policy.rpm,
    windows,
  };
  panelOpen.value = true;

  // The list and nothing else: the server keeps a digest, so there is no token
  // to show and no endpoint that could produce one. What an operator needs
  // here is to see that a key exists and to be able to revoke it.
  void adminApi.userKeys(id)
    .then(({ keys: list }) => { keys.value = list; })
    .catch(() => { keys.value = []; });
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
      role: form.value.role,
      status: form.value.status,
      group_id: form.value.group,
    };
    if (apiRestrictionTouched.value) {
      patch.api_restricted = form.value.apiRestricted;
      patch.api_restriction_hours = form.value.apiRestrictionHours ?? 0;
    }
    await adminApi.updateUser(row.id, patch);

    if (form.value.newPassword) {
      await adminApi.resetPassword(row.id, form.value.newPassword);
    }

    await adminApi.savePolicy({
      scope: 'user',
      scope_id: row.id,
      rpm: form.value.rpm,
      tpm: null,
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
  const count = grantCount.value ?? 1;
  grantLabel.value = '…';
  void adminApi.grantCards(row.id, { cards: count, card_days: 30 })
    .then(() => { grantLabel.value = t('granted', { count }); })
    .catch((failure: unknown) => {
      grantLabel.value = '';
      panelError.value = failure instanceof ApiError ? failure.message : String(failure);
    });
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

// Stepping in rather than stacking: the panel replaces itself and offers a
// way back, so the layout never grows a fourth column.
async function openConversations(): Promise<void> {
  const row = account.value;
  if (!row) return;
  mode.value = 'conversations';
  conversations.value = null;
  panelError.value = '';
  try {
    ({ conversations: conversations.value } = await adminApi.userConversations(row.id));
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
  if (mode.value === 'conversations') {
    return t('someonesConversations', { name: row.nickname || row.username });
  }
  return row.nickname || row.username;
});

async function load(): Promise<void> {
  error.value = '';
  try {
    ({ groups: groups.value } = await adminApi.groups());
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
        ]"
        @update:model-value="list"
      />
      <OaSelect
        v-model="filters.status"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyStatus') },
          { value: 'active', label: t('filterActive') },
          { value: 'disabled', label: t('filterDisabled') },
        ]"
        @update:model-value="list"
      />
      <OaSelect
        v-model="filters.group"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyGroup') },
          ...groups.map((group) => ({ value: group.id, label: group.name })),
        ]"
        @update:model-value="list"
      />
    </div>

    <p v-if="listing" class="oa-table-empty">{{ t('loading') }}</p>
    <p v-else-if="listError" class="oa-table-empty">{{ listError }}</p>
    <OaTable
      v-else
      :columns="columns"
      :rows="users"
      :empty="t('noAccountsMatch')"
      :muted="(row) => row.status === 'disabled'"
      selectable
      @select="open($event.id)"
    >
      <template #cell-account="{ row }">
        <OaCellStack
          :title="row.nickname || row.username"
          :sub="row.qq ? `@${row.username} · QQ ${row.qq}` : `@${row.username}`"
        />
      </template>
      <template #cell-role="{ row }">
        <OaBadgeRow>
          <OaBadge v-if="row.role === 'admin'">{{ t('admin') }}</OaBadge>
          <OaBadge v-if="row.status === 'disabled'" tone="danger">{{ t('disabled') }}</OaBadge>
          <OaBadge v-if="apiRestrictionActive(row)" tone="warning">{{ t('apiRestrictedBadge') }}</OaBadge>
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
      ? t('confirmDeleteUser', { name: account.username })
      : undefined"
    :back="mode !== 'account'"
    :busy="busy"
    :error="panelError"
    @close="panelOpen = false"
    @confirm="save"
    @destructive="remove"
    @back="mode === 'transcript' ? openConversations() : (mode = 'account')"
  >
    <template v-if="mode === 'account'">
      <OaStatGrid :stats="summaryStats" />

      <div class="oa-facts">
        <div v-for="[label, value, mono] in identity" :key="label" class="oa-fact">
          <span class="oa-fact-label">{{ label }}</span>
          <span class="oa-fact-value" :class="{ mono }">{{ value }}</span>
        </div>
      </div>

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
              <span class="oa-card-source">
                {{ card.source === 'grant' ? t('cardFromAdmin') : t('cardFromCode') }}
              </span>
              <span class="oa-card-expiry">
                {{ card.expires_at > 0 ? t('cardExpires', { when: relativeTime(card.expires_at) }) : t('noLimit') }}
              </span>
            </div>
          </div>
        </template>
      </div>

      <OaNumberField
        v-model="grantCount"
        :label="t('grantCards')"
        :min="1"
        :hint="t('grantCardsHint')"
      />
      <button type="button" class="oa-btn" :disabled="!!grantLabel" @click="grant">
        {{ grantLabel || t('grantCards') }}
      </button>

      <OaFormSection :title="t('secProfile')" />
      <OaTextField v-model="form.nickname" :label="t('nickname')" :max-length="32" />
      <OaTextField v-model="form.email" :label="t('email')" type="email" />
      <OaTextField
        v-model="form.qq"
        :label="t('qq')"
        :placeholder="t('qqPlaceholder')"
        :max-length="15"
      />
      <OaTextArea v-model="form.bio" :label="t('bio')" :rows="2" />
      <OaTextField
        v-model="form.avatar"
        :label="t('avatar')"
        :placeholder="t('avatarPlaceholder')"
        :hint="t('avatarHint')"
      />

      <OaFormSection :title="t('secAccess')" />
      <OaSelectField
        v-model="form.role"
        :label="t('role')"
        :hint="self ? t('cannotDemoteSelf') : undefined"
        :options="[
          { value: 'user', label: t('roleUser') },
          { value: 'admin', label: t('roleAdmin') },
        ]"
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
      />
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

      <OaFormSection :title="t('secAllowanceOverride')" :hint="t('allowanceOverrideHint')" />
      <OaNumberField
        v-model="form.rpm"
        :label="t('requestsPerMinute')"
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
              <code class="oa-key-prefix">{{ key.prefix }}…</code>
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
        :columns="conversationColumns"
        :rows="conversations"
        :empty="t('noConversations')"
        selectable
        @select="openTranscript($event)"
      />
    </template>

    <template v-else>
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
