<script setup lang="ts">
// Invite codes: who may join without an invitation, and who let everyone
// else in.
//
// Four things share this screen because they are one subject read from four
// angles: the registration policy (open, invite-only or closed) and what a
// qualifying invite is worth to the person who sent it; the partner links an
// operator hands to an outside partner, each carrying its own group and day
// range; the stats a code scheme's health is read off; and the codes
// themselves — an operator's batch, a partner's named link, or the code
// every account may carry for its own referrals — searchable and filterable
// in one list.

import { computed, onMounted, ref, watch } from 'vue';
import { useDebounceFn } from '@vueuse/core';
import { adminApi, type Group, type InviteCode, type InviteStats, type InviteUse } from '@/admin/api';
import { fetchSite } from '@/api/auth';
import { ApiError } from '@/api/client';
import { copyToClipboard } from '@/chat/markdown';
import AdminControlCard from './AdminControlCard.vue';
import PluginList from './PluginList.vue';
import { plugins } from '@/plugins/registry';
import OaBadge from '@/components/OaBadge.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column, PageState } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { t } from '@/composables/useI18n';
import { IconChart, IconSend, IconUsers } from '@/icons';
import { compactNumber, relativeTime } from '@/lib/format';
import { maskCredential, maskUser } from '@/admin/safeMode';
import { site } from '@/stores/session';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navInvites'), t('invitesSubtitle'));

/** A generated code reads as XXXX-XXXX. A partner's code is a name
 *  somebody chose — PARTNERX is eight letters too, and splitting it into
 *  PART-NERX would print a word nobody typed — so it is shown as stored. */
function formatCode(code: string, kind?: string): string {
  if (kind === 'partner') return code;
  return code.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
}

function inviteLink(code: string): string {
  return `${window.location.origin}/register?invite=${code}`;
}

// --- settings: the registration policy and what an invite is worth --------------

const settingsError = ref('');
const settingsLoaded = ref(false);
const savingBusy = ref(false);
const saveLabel = ref('');
const saveFlash = ref('');
const groups = ref<Pick<Group, 'id' | 'name'>[]>([]);

const form = ref({
  mode: 'open',
  userEnabled: false,
  userLimit: 10 as number | null,
  rewardEvery: 1 as number | null,
  rewardCards: 0 as number | null,
  rewardCardDays: 30 as number | null,
});

/** Only this page's keys — the settings endpoint leaves every other screen's
 *  fields alone, which is what lets this card and Security's own registration
 *  switch both write `registration.enabled` without either reverting it. */
function collect(): Record<string, string> {
  return {
    'registration.enabled': String(form.value.mode !== 'closed'),
    'invites.required': String(form.value.mode === 'invite'),
    'invites.user_enabled': String(form.value.userEnabled),
    'invites.user_limit': String(form.value.userLimit ?? 10),
    'invites.reward_every': String(form.value.rewardEvery ?? 1),
    'invites.reward_cards': String(form.value.rewardCards ?? 0),
    'invites.reward_card_days': String(form.value.rewardCardDays ?? 30),
  };
}

const dirty = computed(() => saved.value !== null && saved.value !== JSON.stringify(collect()));
const saved = ref<string | null>(null);
function accept(values = collect()): void { saved.value = JSON.stringify(values); }

/** The reward read as one sentence, live under the three fields that make
 *  it up — the only way an operator sees the same rule the settings page
 *  spells out in three separate numbers. Zero cards still counts invites
 *  (rewarded_at / reward_skipped are set regardless), so that case gets its
 *  own wording rather than reading as "every 0 people get 0 cards". */
const rewardSummary = computed(() => {
  const every = form.value.rewardEvery ?? 1;
  const cards = form.value.rewardCards ?? 0;
  const days = form.value.rewardCardDays ?? 30;
  return cards > 0
    ? t('inviteRewardRuleSummary', { every, cards, days })
    : t('inviteRewardRuleNone', { every });
});

async function loadSettings(): Promise<void> {
  settingsError.value = '';
  try {
    const [data, groupOptions] = await Promise.all([adminApi.settings(), adminApi.groupOptions()]);
    const values = data.settings;
    groups.value = groupOptions.groups ?? [];
    const enabled = values['registration.enabled'] === 'true';
    const required = values['invites.required'] === 'true';
    form.value = {
      mode: !enabled ? 'closed' : required ? 'invite' : 'open',
      userEnabled: values['invites.user_enabled'] === 'true',
      userLimit: Number(values['invites.user_limit'] ?? 10),
      rewardEvery: Number(values['invites.reward_every'] ?? 1),
      rewardCards: Number(values['invites.reward_cards'] ?? 0),
      rewardCardDays: Number(values['invites.reward_card_days'] ?? 30),
    };
    accept();
  } catch (failure) {
    settingsError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    settingsLoaded.value = true;
  }
}

async function saveSettings(): Promise<void> {
  if (!settingsLoaded.value || settingsError.value || savingBusy.value) return;
  const values = collect();
  savingBusy.value = true;
  saveLabel.value = t('saving');
  saveFlash.value = '';
  try {
    await adminApi.saveSettings(values);
    accept(values);
    try {
      // Registration mode is the one setting here a signed-out visitor reads
      // straight off this response, so the change should be visible without
      // a reload the moment it is saved.
      site.value = await fetchSite();
    } catch {
      // The setting itself is already saved; a failed public refresh is not
      // worth reporting as one.
    }
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
  } catch (failure) {
    saveFlash.value = failure instanceof ApiError ? failure.message : String(failure);
    saveLabel.value = '';
  } finally {
    savingBusy.value = false;
  }
}

// --- stats -----------------------------------------------------------------------

const stats = ref<InviteStats | null>(null);
const statsError = ref('');

async function loadStats(): Promise<void> {
  statsError.value = '';
  try {
    stats.value = await adminApi.inviteStats();
  } catch (failure) {
    statsError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

const statGridItems = computed<Stat[]>(() => (stats.value ? [
  { label: t('statActiveCodes'), value: compactNumber(stats.value.active) },
  { label: t('statInvitesTotal'), value: compactNumber(stats.value.uses_total) },
  { label: t('statInvites7d'), value: compactNumber(stats.value.uses_7d) },
] : []));

// partners is a field the server may not have shipped yet on an instance
// mid-rollout — read defensively rather than assume the contract's shape.
const partnerStats = computed(() => stats.value?.partners ?? []);

// --- the code list: filtered, searched, paged -------------------------------------

const codes = ref<InviteCode[]>([]);
const total = ref(0);
const paging = ref<PageState>({ page: 1, pageSize: 20 });
const listing = ref(false);
const listError = ref('');
let listRequest = 0;

const filters = ref({ kind: 'all', status: 'all', q: '' });

function changePage(next: PageState): void { paging.value = next; void list(); }
function filterList(): void { paging.value.page = 1; void list(); }
const debouncedList = useDebounceFn(() => void list(), 250);
watch(() => filters.value.q, () => { paging.value.page = 1; void debouncedList(); });

async function list(): Promise<void> {
  const ticket = ++listRequest;
  listing.value = true;
  listError.value = '';
  const query = new URLSearchParams({
    limit: String(paging.value.pageSize),
    offset: String((paging.value.page - 1) * paging.value.pageSize),
  });
  if (filters.value.kind !== 'all') query.set('kind', filters.value.kind);
  if (filters.value.status !== 'all') query.set('status', filters.value.status);
  if (filters.value.q.trim()) query.set('q', filters.value.q.trim());
  try {
    const result = await adminApi.invites(`?${query}`);
    if (ticket !== listRequest) return;
    if (result.total > 0 && (paging.value.page - 1) * paging.value.pageSize >= result.total) {
      paging.value.page = Math.ceil(result.total / paging.value.pageSize);
      await list(); return;
    }
    codes.value = result.codes ?? [];
    total.value = result.total;
  } catch (failure) {
    if (ticket !== listRequest) return;
    listError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    if (ticket === listRequest) listing.value = false;
  }
}

/** The partner-code list embedded in its own card is a separate, unpaged
 *  fetch — always every partner code, not whatever page and filter the
 *  operator left the big list on — so creating or revoking one refreshes it
 *  on its own rather than through `list()`. */
const partnerCodes = ref<InviteCode[]>([]);
const partnerLoading = ref(false);
const partnerListError = ref('');

async function loadPartners(): Promise<void> {
  partnerLoading.value = true;
  partnerListError.value = '';
  try {
    const result = await adminApi.invites('?kind=partner&limit=50&offset=0');
    partnerCodes.value = result.codes ?? [];
  } catch (failure) {
    partnerListError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    partnerLoading.value = false;
  }
}

/** registrations is not a field of its own — `uses` counts both a
 *  registration and a claim spending the same use, so it is `uses` minus
 *  whatever `claims` already accounts for. Guards against an instance whose
 *  server has not shipped `claims` yet. */
function registrationsOf(row: InviteCode): number {
  return Math.max(0, row.uses - (row.claims ?? 0));
}

function ownerTitle(row: InviteCode): string {
  if (row.kind === 'partner') return row.name || t('inviteKindPartner');
  return row.owner_id ? maskUser(row.owner_nickname || row.owner_username) : t('inviteOwnerAdmin');
}
function ownerSub(row: InviteCode): string | undefined {
  if (row.kind === 'partner') return t('inviteKindPartner');
  return row.owner_id ? `@${maskUser(row.owner_username)}` : undefined;
}

/** "3–7 days", a fixed "30 days", or "Permanent" for a fixed length of zero.
 *  A code with no group carries none of this. */
function daysLabel(row: InviteCode): string {
  if (!row.group_id) return '—';
  if (row.group_days_max > 0) return t('invitesDaysRange', { from: row.group_days, to: row.group_days_max });
  return row.group_days === 0 ? t('invitesPermanent') : t('nDays', { count: row.group_days });
}

function statusLabel(status: InviteCode['status']): string {
  if (status === 'active') return t('inviteStatusActive');
  if (status === 'used_up') return t('inviteStatusUsedUp');
  if (status === 'expired') return t('inviteStatusExpired');
  return t('inviteStatusRevoked');
}
function statusTone(status: InviteCode['status']): 'default' | 'muted' | 'danger' {
  if (status === 'active') return 'default';
  if (status === 'used_up') return 'muted';
  return 'danger';
}

const columns = computed<Array<Column<InviteCode>>>(() => [
  { key: 'code', header: t('colInviteCode') },
  { key: 'owner', header: t('colOwner'), secondary: true, width: '150px' },
  { key: 'group', header: t('colGroup'), secondary: true, width: '150px' },
  {
    key: 'uses', header: t('colUses'), numeric: true, width: '80px',
    text: (row) => `${row.uses} / ${row.max_uses || '∞'}`,
  },
  {
    key: 'expires', header: t('colExpires'), secondary: true, width: '110px',
    text: (row) => (row.expires_at ? relativeTime(row.expires_at) : '—'),
  },
  { key: 'status', header: t('colState'), width: '90px' },
]);

/** The partner card's own short list — name and code stand in for "owner",
 *  and registrations/claims are the two figures nothing else on this screen
 *  breaks out, everything else reuses the big list's own columns. */
const partnerColumns = computed<Array<Column<InviteCode>>>(() => [
  { key: 'name', header: t('colName') },
  { key: 'group', header: t('colGroup'), secondary: true, width: '140px' },
  { key: 'registrations', header: t('colRegistrations'), numeric: true, width: '90px', text: (row) => String(registrationsOf(row)) },
  { key: 'claims', header: t('colClaims'), numeric: true, width: '80px', text: (row) => String(row.claims ?? 0) },
  { key: 'uses', header: t('colUses'), numeric: true, width: '90px', text: (row) => `${row.uses} / ${row.max_uses || '∞'}` },
  { key: 'expires', header: t('colExpires'), secondary: true, width: '100px', text: (row) => (row.expires_at ? relativeTime(row.expires_at) : '—') },
  { key: 'status', header: t('colState'), width: '90px' },
]);

// --- the generate / detail panel ---------------------------------------------------
//
// One panel, three faces — the same shape AdminCodes uses for redemption
// codes: a form that creates a code, the batch it just made (copy it now,
// nowhere else lists it together), or an existing code with who has used it.
// A partner code shares this panel rather than getting one of its own: it is
// not a different kind of thing to create, only a batch of one with a
// partner's name and a claimable switch attached — the same reasoning
// invite.CreateInput itself is built on. `footer` and `confirmable` are set
// separately rather than tied to `creating` alone, because a code being
// viewed still needs its one action — revoking it — in that footer.

const panelOpen = ref(false);
const existing = ref<InviteCode | null>(null);
const minted = ref<InviteCode[] | null>(null);
const busy = ref(false);
const panelError = ref('');
const copyFlash = ref('');

const uses = ref<InviteUse[]>([]);
const usesLoading = ref(false);
const usesError = ref('');

const creating = computed(() => existing.value === null && minted.value === null);

const genForm = ref({
  kind: 'batch' as 'batch' | 'partner',
  count: 1 as number | null,
  code: '',
  name: '',
  maxUses: 1 as number | null,
  expiresDays: null as number | null,
  groupId: '',
  daysFrom: 0 as number | null,
  daysTo: null as number | null,
  note: '',
  allowExisting: true,
});

/** A partner code is always exactly one, named, custom-worded and requires a
 *  group — the same fields Create rejects a batch for skipping are the ones
 *  it requires here instead. */
const partnerValid = computed(() => genForm.value.kind !== 'partner' || (
  genForm.value.name.trim().length > 0 && genForm.value.name.trim().length <= 60
  && genForm.value.code.trim().length > 0 && !!genForm.value.groupId
));

function openGenerate(kind: 'batch' | 'partner' = 'batch'): void {
  existing.value = null;
  minted.value = null;
  panelError.value = '';
  copyFlash.value = '';
  uses.value = [];
  usesError.value = '';
  genForm.value = {
    kind, count: 1, code: '', name: '',
    // Unlimited by default: a partner link is meant to be handed out widely,
    // where a batch's default of one each is meant to be counted out.
    maxUses: kind === 'partner' ? 0 : 1,
    expiresDays: null, groupId: '', daysFrom: 0, daysTo: null, note: '',
    allowExisting: true,
  };
  panelOpen.value = true;
}

function openExisting(row: InviteCode): void {
  minted.value = null;
  existing.value = row;
  panelError.value = '';
  copyFlash.value = '';
  uses.value = [];
  usesError.value = '';
  usesLoading.value = true;
  panelOpen.value = true;
  void loadUses(row.id);
}

async function loadUses(id: string): Promise<void> {
  try {
    const result = await adminApi.inviteUses(id);
    if (existing.value?.id === id) uses.value = result.uses ?? [];
  } catch (failure) {
    if (existing.value?.id === id) usesError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    if (existing.value?.id === id) usesLoading.value = false;
  }
}

/** "@nickname · Claimed · Rewarded", or the reason it was not — folded into
 *  the subtitle rather than a badge, so this list stays the same two-column
 *  row every other card list on this screen already is. Registering is the
 *  default and common case and earns no badge of its own; only the notable
 *  states do. */
function useSub(use: InviteUse): string {
  const base = `@${maskUser(use.username)}`;
  const parts: string[] = [];
  if (use.via === 'claim') parts.push(t('inviteViaClaim'));
  if (use.rewarded_at) parts.push(t('inviteRewardedBadge'));
  else if (use.reward_skipped === 'same_ip') parts.push(t('inviteSkippedSameIP'));
  else if (use.reward_skipped === 'limit') parts.push(t('inviteSkippedLimit'));
  else if (use.reward_skipped === 'disabled') parts.push(t('inviteSkippedDisabled'));
  return parts.length ? `${base} · ${parts.join(' · ')}` : base;
}

async function create(): Promise<void> {
  const partner = genForm.value.kind === 'partner';
  const count = genForm.value.count ?? 1;
  const days = genForm.value.expiresDays;
  const groupId = genForm.value.groupId;
  const from = genForm.value.daysFrom ?? 0;
  const to = genForm.value.daysTo ?? 0;
  busy.value = true;
  panelError.value = '';
  try {
    const { codes: created } = await adminApi.createInvites({
      count,
      // A batch is generated; naming one is only offered for a single code —
      // which a partner code always is, since it forces count to 1.
      code: count > 1 ? '' : genForm.value.code.trim(),
      max_uses: genForm.value.maxUses ?? 0,
      // Zero is "never", which is what an empty field means here.
      expires_at: days && days > 0 ? Date.now() + days * 24 * 3600 * 1000 : 0,
      group_id: groupId,
      group_days: groupId ? from : 0,
      // A "to" no higher than "from" is a fixed length, not a range.
      group_days_max: groupId && to > from ? to : 0,
      note: partner ? '' : genForm.value.note.trim(),
      ...(partner ? { kind: 'partner', name: genForm.value.name.trim(), allow_existing: genForm.value.allowExisting } : {}),
    });
    minted.value = created;
    void list();
    void loadStats();
    if (partner) void loadPartners();
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

async function revoke(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  busy.value = true;
  try {
    const { code: updated } = await adminApi.revokeInvite(row.id);
    existing.value = updated;
    void list();
    void loadStats();
    if (row.kind === 'partner') void loadPartners();
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

function copyCode(code: string, kind?: string): void {
  void copyToClipboard(formatCode(code, kind)).then((ok) => { copyFlash.value = ok ? t('copied') : t('copyFailed'); });
}
function copyLink(code: string): void {
  void copyToClipboard(inviteLink(code)).then((ok) => { copyFlash.value = ok ? t('copied') : t('copyFailed'); });
}
function copyAllCodes(): void {
  const lines = (minted.value ?? []).map((entry) => formatCode(entry.code, entry.kind)).join('\n');
  void copyToClipboard(lines).then((ok) => { copyFlash.value = ok ? t('copied') : t('copyFailed'); });
}

// Tables a plugin keeps about invitations — who left, say, and what was
// taken back.
const pluginLists = computed(() =>
  plugins().flatMap((plugin) => plugin.lists ?? []).filter((list) => list.page === 'invites'));

onMounted(() => {
  void loadSettings();
  void loadStats();
  void list();
  void loadPartners();
});
</script>

<template>
  <Teleport :to="view.actionsHost">
    <span v-if="settingsLoaded && !settingsError" class="oa-control-save-state" :class="{ dirty }" role="status">
      <span class="oa-dashboard-dot" />{{ dirty ? t('controlUnsaved') : t('controlSaved') }}
    </span>
    <button type="button" class="oa-btn" :disabled="savingBusy || !settingsLoaded || !!settingsError" @click="saveSettings">
      {{ saveLabel || t('save') }}
    </button>
    <button id="generateInvites" type="button" class="oa-btn primary" @click="openGenerate('batch')">{{ t('generateInvites') }}</button>
  </Teleport>

  <AdminFailure v-if="settingsError" :message="settingsError" @retry="loadSettings" />
  <p v-else-if="!settingsLoaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <div class="oa-workbench">
      <div class="oa-workbench-grid">
        <AdminControlCard id="secInviteSettings" :title="t('secInviteSettings')" :hint="t('secInviteSettingsHint')" :icon="IconUsers">
          <OaSelectField
            v-model="form.mode"
            :label="t('registrationMode')"
            :hint="t('registrationModeHint')"
            :options="[
              { value: 'open', label: t('registrationModeOpen') },
              { value: 'invite', label: t('registrationModeInvite') },
              { value: 'closed', label: t('registrationModeClosed') },
            ]"
          />
          <OaSwitchField v-model="form.userEnabled" :label="t('userInvitesEnabled')" :hint="t('userInvitesEnabledHint')" />
          <template v-if="form.userEnabled">
            <OaNumberField v-model="form.userLimit" :label="t('userInviteLimit')" :min="0" :max="10000" :hint="t('userInviteLimitHint')" />
            <OaNumberField v-model="form.rewardEvery" :label="t('inviteRewardEvery')" :min="1" :max="1000" :hint="t('inviteRewardEveryHint')" />
            <OaNumberField v-model="form.rewardCards" :label="t('inviteRewardCards')" :min="0" :max="100" :hint="t('inviteRewardCardsHint')" />
            <OaNumberField
              v-if="(form.rewardCards ?? 0) > 0"
              v-model="form.rewardCardDays"
              :label="t('inviteRewardCardDays')"
              :min="1"
              :max="3650"
            />
            <p class="oa-field-hint" role="status">{{ rewardSummary }}</p>
          </template>
        </AdminControlCard>

        <AdminControlCard id="secInviteStats" :title="t('secInviteStats')" :icon="IconChart">
          <p v-if="statsError" class="oa-field-hint">{{ statsError }}</p>
          <p v-else-if="!stats" class="oa-field-hint">{{ t('loading') }}</p>
          <template v-else>
            <OaStatGrid :stats="statGridItems" />
            <OaFormSection :title="t('topInviters')" />
            <p v-if="!stats.top_inviters.length" class="oa-field-hint">{{ t('noTopInviters') }}</p>
            <div v-else class="oa-card-list">
              <div v-for="row in stats.top_inviters" :key="row.user_id" class="oa-card-row">
                <OaCellStack :title="maskUser(row.nickname || row.username)" :sub="`@${maskUser(row.username)}`" />
                <span class="oa-card-expiry">{{ t('inviteInviterSummary', { invites: row.invites, rewarded: row.rewarded }) }}</span>
              </div>
            </div>
            <OaFormSection :title="t('topPartnerCodes')" />
            <p v-if="!partnerStats.length" class="oa-field-hint">{{ t('noPartnerCodes') }}</p>
            <div v-else class="oa-card-list">
              <div v-for="row in partnerStats" :key="row.id" class="oa-card-row">
                <OaCellStack :title="row.name || '—'" :sub="maskCredential(formatCode(row.code, 'partner'))" />
                <span class="oa-card-expiry">{{ t('invitePartnerSummary', { registrations: row.registrations, claims: row.claims }) }}</span>
              </div>
            </div>
          </template>
        </AdminControlCard>

        <PluginList v-for="spec in pluginLists" :key="spec.id" :spec="spec" />

        <AdminControlCard
          id="secInvitePartners"
          class="oa-control-card-wide"
          :title="t('secInvitePartners')"
          :hint="t('secInvitePartnersHint')"
          :icon="IconSend"
        >
          <template #actions>
            <button id="createPartnerCode" type="button" class="oa-btn" @click="openGenerate('partner')">{{ t('createPartnerCode') }}</button>
          </template>
          <p v-if="partnerListError" class="oa-field-hint">{{ partnerListError }}</p>
          <p v-else-if="partnerLoading" class="oa-field-hint">{{ t('loading') }}</p>
          <OaTable
            v-else
            :columns="partnerColumns"
            :rows="partnerCodes"
            :empty="t('noPartnerCodes')"
            :muted="(row) => row.status !== 'active'"
            selectable
            @select="openExisting($event)"
          >
            <template #cell-name="{ row }">
              <OaCellStack :title="row.name || '—'" :sub="maskCredential(formatCode(row.code, row.kind))" />
            </template>
            <template #cell-group="{ row }">
              <OaCellStack :title="row.group_id ? (row.group_name || '—') : t('inviteGroupNone')" :sub="row.group_id ? daysLabel(row) : undefined" />
            </template>
            <template #cell-status="{ row }">
              <OaBadge :tone="statusTone(row.status)">{{ statusLabel(row.status) }}</OaBadge>
            </template>
          </OaTable>
        </AdminControlCard>
      </div>
    </div>

    <!-- The list of codes is a card like the ones above it, in the same column:
         filters and table on the bare panel were wider than the column and
         drew their corners against nothing. -->
    <div class="oa-workbench oa-invite-codes">
      <AdminControlCard id="secInviteCodes" :title="t('secInviteCodes')" :icon="IconSend">
        <div class="oa-filters">
          <input v-model="filters.q" type="search" :placeholder="t('searchInvites')">
          <OaSelect
            v-model="filters.kind"
            class="oa-filter-select"
            :choices="[
              { value: 'all', label: t('inviteKindAll') },
              { value: 'batch', label: t('inviteKindBatch') },
              { value: 'partner', label: t('inviteKindPartner') },
              { value: 'personal', label: t('inviteKindUser') },
            ]"
            @update:model-value="filterList"
          />
          <OaSelect
            v-model="filters.status"
            class="oa-filter-select"
            :choices="[
              { value: 'all', label: t('inviteStatusAll') },
              { value: 'active', label: t('inviteStatusActive') },
              { value: 'used_up', label: t('inviteStatusUsedUp') },
              { value: 'expired', label: t('inviteStatusExpired') },
              { value: 'revoked', label: t('inviteStatusRevoked') },
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
          :rows="codes"
          :empty="t('noInvites')"
          :muted="(row) => row.status !== 'active'"
          selectable
          @select="openExisting($event)"
        >
          <template #cell-code="{ row }">
            <OaCellStack :title="maskCredential(formatCode(row.code, row.kind))" :sub="row.note || undefined" monospace />
          </template>
          <template #cell-owner="{ row }">
            <OaCellStack :title="ownerTitle(row)" :sub="ownerSub(row)" />
          </template>
          <template #cell-group="{ row }">
            <OaCellStack :title="row.group_id ? (row.group_name || '—') : t('inviteGroupNone')" :sub="row.group_id ? daysLabel(row) : undefined" />
          </template>
          <template #cell-status="{ row }">
            <OaBadge :tone="statusTone(row.status)">{{ statusLabel(row.status) }}</OaBadge>
          </template>
        </OaTable>

        <p v-if="saveFlash" class="oa-drawer-flash visible oa-control-flash" role="alert">{{ saveFlash }}</p>
      </AdminControlCard>
    </div>
  </template>

  <OaPanel
    v-if="panelOpen"
    :title="minted ? t('codesMinted', { count: minted.length }) : creating ? (genForm.kind === 'partner' ? t('secInvitePartners') : t('secGenerateInvites')) : (existing!.name || maskCredential(formatCode(existing!.code, existing!.kind)))"
    :footer="!minted"
    :confirmable="creating && partnerValid"
    :confirm-label="creating && genForm.kind === 'partner' ? t('createPartnerCode') : t('generateInvites')"
    :destructive-label="existing && existing.status !== 'revoked' ? t('revokeLabel') : undefined"
    :destructive-confirm="existing ? t('confirmRevokeInvite', { code: maskCredential(formatCode(existing.code, existing.kind)) }) : undefined"
    :busy="busy"
    :error="panelError"
    @close="panelOpen = false"
    @confirm="create"
    @destructive="revoke"
  >
    <!-- The batch, once, with a way to take it away in one piece. -->
    <template v-if="minted">
      <p class="oa-field-hint">{{ t('codesMintedHint') }}</p>
      <button type="button" class="oa-btn primary" @click="copyAllCodes">{{ copyFlash || t('copyAll') }}</button>
      <div class="oa-card-list">
        <div v-for="entry in minted" :key="entry.id" class="oa-card-row">
          <OaCellStack
            :title="entry.name || maskCredential(formatCode(entry.code, entry.kind))"
            :sub="entry.name ? maskCredential(formatCode(entry.code, entry.kind)) : undefined"
            monospace
          />
          <div class="oa-invite-actions">
            <button type="button" class="oa-btn" @click="copyCode(entry.code, entry.kind)">{{ t('copyCode') }}</button>
            <button type="button" class="oa-btn" @click="copyLink(entry.code)">{{ t('copyLink') }}</button>
          </div>
        </div>
      </div>
    </template>

    <template v-else-if="!creating">
      <p class="oa-field-hint">
        {{ t('colUses') }}: {{ existing!.uses }} / {{ existing!.max_uses || '∞' }}
        <template v-if="existing!.kind === 'partner'"> · {{ t('inviteClaimsCount', { count: existing!.claims ?? 0 }) }}</template>
        <template v-if="existing!.group_id"> · {{ existing!.group_name || t('inviteGroupNone') }}, {{ daysLabel(existing!) }}</template>
      </p>
      <div class="oa-invite-actions">
        <button type="button" class="oa-btn" @click="copyCode(existing!.code, existing!.kind)">{{ copyFlash || t('copyCode') }}</button>
        <button type="button" class="oa-btn" @click="copyLink(existing!.code)">{{ t('copyLink') }}</button>
      </div>
      <OaFormSection :title="t('inviteUsedBy')" />
      <p v-if="usesLoading" class="oa-field-hint">{{ t('loading') }}</p>
      <p v-else-if="usesError" class="oa-field-hint">{{ usesError }}</p>
      <p v-else-if="!uses.length" class="oa-field-hint">{{ t('inviteNoUses') }}</p>
      <div v-else class="oa-card-list">
        <div v-for="use in uses" :key="use.user_id" class="oa-card-row">
          <OaCellStack :title="maskUser(use.nickname || use.username)" :sub="useSub(use)" />
          <span class="oa-card-expiry">{{ relativeTime(use.created_at) }}</span>
        </div>
      </div>
    </template>

    <template v-else-if="genForm.kind === 'partner'">
      <OaTextField v-model="genForm.name" :label="t('invitePartnerName')" :hint="t('invitePartnerNameHint')" />
      <OaTextField
        v-model="genForm.code"
        :label="t('inviteCustomCode')"
        placeholder="PARTNERX"
        :hint="t('inviteCustomCodeHint')"
        monospace
      />
      <OaSelectField
        v-model="genForm.groupId"
        :label="t('inviteGroup')"
        :options="[
          { value: '', label: t('inviteGroupNone') },
          ...groups.map((group) => ({ value: group.id, label: group.name })),
        ]"
      />
      <template v-if="genForm.groupId">
        <OaNumberField v-model="genForm.daysFrom" :label="t('inviteGroupDaysFrom')" :min="0" :max="3650" :hint="t('inviteGroupDaysHint')" />
        <OaNumberField v-model="genForm.daysTo" :label="t('inviteGroupDaysTo')" :min="0" :max="3650" :placeholder="t('inviteGroupDaysFixed')" />
      </template>
      <OaNumberField v-model="genForm.maxUses" :label="t('inviteMaxUses')" :min="0" :max="100000" :hint="t('inviteMaxUsesHint')" />
      <OaNumberField
        v-model="genForm.expiresDays"
        :label="t('inviteExpiresDays')"
        :min="0"
        :placeholder="t('noLimit')"
        :hint="t('inviteExpiresDaysHint')"
      />
      <OaSwitchField v-model="genForm.allowExisting" :label="t('inviteAllowExisting')" :hint="t('inviteAllowExistingHint')" />
    </template>

    <template v-else>
      <OaNumberField v-model="genForm.count" :label="t('inviteCount')" :min="1" :max="500" :hint="t('inviteCountHint')" />
      <!-- A batch is generated, so there is nothing to name — the field is
           for the one case that is naming a single, memorable link. -->
      <OaTextField
        v-if="(genForm.count ?? 1) <= 1"
        v-model="genForm.code"
        :label="t('inviteCustomCode')"
        placeholder="PARTNERX"
        :hint="t('inviteCustomCodeHint')"
        monospace
      />
      <OaNumberField v-model="genForm.maxUses" :label="t('inviteMaxUses')" :min="0" :max="100000" :hint="t('inviteMaxUsesHint')" />
      <OaNumberField
        v-model="genForm.expiresDays"
        :label="t('inviteExpiresDays')"
        :min="0"
        :placeholder="t('noLimit')"
        :hint="t('inviteExpiresDaysHint')"
      />
      <OaSelectField
        v-model="genForm.groupId"
        :label="t('inviteGroup')"
        :options="[
          { value: '', label: t('inviteGroupNone') },
          ...groups.map((group) => ({ value: group.id, label: group.name })),
        ]"
      />
      <template v-if="genForm.groupId">
        <OaNumberField v-model="genForm.daysFrom" :label="t('inviteGroupDaysFrom')" :min="0" :max="3650" :hint="t('inviteGroupDaysHint')" />
        <OaNumberField v-model="genForm.daysTo" :label="t('inviteGroupDaysTo')" :min="0" :max="3650" :placeholder="t('inviteGroupDaysFixed')" />
      </template>
      <OaTextField v-model="genForm.note" :label="t('inviteNote')" :hint="t('inviteNoteHint')" />
    </template>
  </OaPanel>
</template>
