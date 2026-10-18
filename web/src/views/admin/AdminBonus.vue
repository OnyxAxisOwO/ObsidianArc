<script setup lang="ts">
// Bonus bars and check-in, the two ways an account gets more than its window.
//
// A bar is a set of rules (who may switch it, whether the total is shown, which
// models it covers) and a place to grant into. Check-in is what pressing a
// button once a day earns. The rules for both are in
// docs/architecture/bonus-and-checkin.md; this screen edits them.
//
// A shared pool is a bar too, with one balance for everybody in place of
// grants: the same panel edits its rules, and where a bar lists its grants a
// pool shows its balance and the box that changes it.

import { computed, onMounted, ref, watch } from 'vue';
import {
  adminApi,
  type AdminModel, type BonusBarRow, type BonusGrantRow, type CheckinRuleBody, type CheckinSettings, type Group,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaCheckList from '@/components/OaCheckList.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column, SortState } from '@/components/table-types';
import { t, tn } from '@/composables/useI18n';
import { maskProvider, maskUser } from '@/admin/safeMode';
import { apiErrorText } from '@/lib/api-error';
import { absoluteTime, relativeTime } from '@/lib/format';
import { IconCheck, IconGift } from '@/icons';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';
import CheckinRewardEditor from './CheckinRewardEditor.vue';
import ExpiryPresets from './ExpiryPresets.vue';
import { poolRefusal, poolText as pt } from './pool-text';
import { useAdminView } from './adminView';
import { toDateTimeLocal } from './shared';
import { adminCodeText, errorLine, refusalLine } from './failure-codes';

const view = useAdminView();
view.setTitle(() => t('bonusTitle'), () => t('bonusSubtitle'));

// The server clamps a grant to this many days (internal/admin/bonus.go), so a
// larger figure would be shortened without a word. The field stops at the same
// figure for that reason.
const MAX_GRANT_DAYS = 3650;

const bars = ref<BonusBarRow[]>([]);
const models = ref<AdminModel[]>([]);
const groups = ref<Group[]>([]);
const error = ref('');
const loaded = ref(false);

async function load(): Promise<void> {
  error.value = '';
  try {
    bars.value = (await adminApi.bonusBars()).bars;
    // The pickers below are only needed once a panel is open, and a failure to
    // fetch them should not hide the bars.
    void adminApi.models().then((r) => { models.value = r.models; }).catch(() => undefined);
    void adminApi.groups().then((r) => { groups.value = r.groups; }).catch(() => undefined);
    await loadCheckin();
  } catch (failure) {
    error.value = errorLine(failure);
  } finally {
    loaded.value = true;
  }
}

// --- the bars ----------------------------------------------------------------

interface BarForm {
  name: string;
  description: string;
  kind: BonusBarRow['kind'];
  toggle_mode: BonusBarRow['toggle_mode'];
  default_on: boolean;
  show_total: boolean;
  model_ids: string[];
  expiry: string;
  active: boolean;
  reset_period: '' | 'day' | 'week' | 'month';
  reset_amount: number | null;
  per_user_daily: number | null;
  /** What a new pool starts with; not sent for one that exists. */
  initial: number | null;
}

const panel = ref<'closed' | 'bar' | 'grant'>('closed');
const existing = ref<BonusBarRow | null>(null);
const form = ref<BarForm>(blankForm());
const busy = ref(false);
const panelError = ref('');
/**
 * A refresh that fails after an action has already closed its panel. The
 * panel's own error would be written where nothing is drawn, so this one is
 * shown on the page.
 */
const pageError = ref('');
const flash = ref('');

function blankForm(): BarForm {
  return {
    name: '', description: '', kind: 'bonus', toggle_mode: 'user', default_on: true, show_total: true,
    model_ids: [], expiry: '', active: true, reset_period: '', reset_amount: null, per_user_daily: null, initial: null,
  };
}

function toLocalInput(ms: number): string {
  if (!ms) return '';
  return toDateTimeLocal(ms);
}

function fromLocalInput(value: string): number {
  if (!value) return 0;
  const ms = new Date(value).getTime();
  return Number.isNaN(ms) ? 0 : ms;
}

function openBar(row: BonusBarRow | null): void {
  existing.value = row;
  panelError.value = '';
  flash.value = '';
  grants.value = [];
  form.value = row
    ? {
      name: row.name, description: row.description, kind: row.kind, toggle_mode: row.toggle_mode,
      default_on: row.default_on, show_total: row.show_total, model_ids: [...row.model_ids],
      expiry: toLocalInput(row.default_expires_at), active: row.active,
      reset_period: row.pool?.reset_period ?? '', reset_amount: row.pool?.reset_amount || null,
      per_user_daily: row.pool?.per_user_daily || null, initial: null,
    }
    : blankForm();
  adjust.value = { op: 'add', amount: null };
  panel.value = 'bar';
  if (row && row.kind !== 'shared') void loadGrants(row.id, 0);
}

// A pool's switch has 'only' and no 'off'; every other bar has the reverse.
// Changing the type moves a switch the new type does not have to one it does.
watch(() => form.value.kind, (kind) => {
  if (kind === 'shared' && form.value.toggle_mode === 'off') form.value.toggle_mode = 'user';
  if (kind !== 'shared' && form.value.toggle_mode === 'only') form.value.toggle_mode = 'user';
});

function poolBody(): Record<string, unknown> | undefined {
  const f = form.value;
  if (f.kind !== 'shared') return undefined;
  return {
    reset_period: f.reset_period, reset_amount: f.reset_period ? f.reset_amount ?? 0 : 0,
    per_user_daily: f.per_user_daily ?? 0,
    ...(existing.value ? {} : { amount: f.initial ?? 0 }),
  };
}

async function saveBar(): Promise<void> {
  busy.value = true;
  panelError.value = '';
  try {
    await adminApi.saveBonusBar(existing.value?.id ?? null, {
      name: form.value.name, description: form.value.description, kind: form.value.kind,
      toggle_mode: form.value.toggle_mode, default_on: form.value.default_on, show_total: form.value.show_total,
      model_ids: form.value.model_ids, default_expires_at: fromLocalInput(form.value.expiry), active: form.value.active,
      pool: poolBody(),
    });
    panel.value = 'closed';
    await refreshBars();
  } catch (failure) {
    // Only a code the interface has a sentence for is translated; any other
    // keeps the server's sentence, which an administrator may need verbatim.
    panelError.value = failure instanceof ApiError
      ? poolRefusal(failure.code) ?? adminCodeText(failure.code, failure.details) ?? failure.message
      : String(failure);
  } finally {
    busy.value = false;
  }
}

// The save or delete is committed before this runs, so a failed read-back is
// reported where the page is, not as a failed save in a panel that is closed.
async function refreshBars(): Promise<void> {
  pageError.value = '';
  try {
    bars.value = (await adminApi.bonusBars()).bars;
  } catch (failure) {
    pageError.value = refusalLine(failure);
  }
}

async function removeBar(): Promise<void> {
  if (!existing.value) return;
  busy.value = true;
  try {
    await adminApi.deleteBonusBar(existing.value.id);
    panel.value = 'closed';
    await refreshBars();
  } catch (failure) {
    panelError.value = refusalLine(failure);
  } finally {
    busy.value = false;
  }
}

// A pool cannot become a bar nor a bar a pool (the server refuses it), so an
// existing one is offered only the types it may change between.
const kindChoices = computed(() => [
  { value: 'bonus', label: t('bonusKindBonus') },
  { value: 'reserve', label: t('bonusKindReserve') },
  { value: 'shared', label: t('bonusKindShared') },
].filter((c) => !existing.value || (c.value === 'shared') === (existing.value.kind === 'shared')));
const modeChoices = computed(() => form.value.kind === 'shared'
  ? [
    { value: 'user', label: t('bonusModeUser') },
    { value: 'on', label: t('bonusModeOn') },
    { value: 'only', label: pt('modeOnly') },
  ]
  : [
    { value: 'user', label: t('bonusModeUser') },
    { value: 'on', label: t('bonusModeOn') },
    { value: 'off', label: t('bonusModeOff') },
  ]);
const allModes = computed<Record<string, string>>(() => ({
  user: t('bonusModeUser'), on: t('bonusModeOn'), off: t('bonusModeOff'), only: pt('modeOnly'),
}));
const resetChoices = computed(() => [
  { value: '', label: pt('resetNever') },
  { value: 'day', label: pt('resetDay') },
  { value: 'week', label: pt('resetWeek') },
  { value: 'month', label: pt('resetMonth') },
]);
/** Check-in pays into an account's own grants, which a pool has none of. */
const grantable = computed(() => bars.value.filter((bar) => bar.kind !== 'shared'));
const modelItems = computed(() => models.value.map((m) => ({ value: m.id, label: m.display_name || m.model_id, sub: maskProvider(m.provider_name) })));

function modeLabel(row: BonusBarRow): string {
  if (row.kind === 'reserve') return t('bonusKindReserve');
  return allModes.value[row.toggle_mode] ?? row.toggle_mode;
}

// --- a pool's balance --------------------------------------------------------

const adjust = ref<{ op: 'add' | 'set'; amount: number | null }>({ op: 'add', amount: null });
const adjustChoices = computed(() => [
  { value: 'add', label: pt('add') },
  { value: 'set', label: pt('set') },
]);

function poolLine(row: BonusBarRow): string {
  const pool = row.pool;
  if (!pool) return '';
  return pt('balance', { left: credits(Math.max(pool.amount - pool.used, 0)), total: credits(pool.amount), count: row.holders });
}

async function adjustPool(): Promise<void> {
  const bar = existing.value;
  if (!bar || adjust.value.amount === null) return;
  busy.value = true;
  panelError.value = '';
  flash.value = '';
  let left: number;
  try {
    const { pool } = await adminApi.adjustBonusPool(bar.id, adjust.value.op, adjust.value.amount);
    left = Math.max(pool.amount - pool.used, 0);
  } catch (failure) {
    panelError.value = (failure instanceof ApiError ? poolRefusal(failure.code) : undefined) ?? apiErrorText(failure);
    return;
  } finally {
    busy.value = false;
  }
  // Committed by here: a failed read-back is reported on the page, as after a
  // grant, so the operator is not sent to top up a second time.
  flash.value = pt('adjusted', { left: credits(left) });
  adjust.value = { op: 'add', amount: null };
  await refreshBars();
  existing.value = bars.value.find((row) => row.id === bar.id) ?? existing.value;
}

function credits(amount: number): string {
  return String(Math.round(amount * 100) / 100);
}

const sort = ref<SortState | null>(null);

const columns = computed<Array<Column<BonusBarRow>>>(() => [
  { key: 'name', header: t('bonusName'), text: (row) => row.name },
  { key: 'rules', header: t('colBonusRules'), width: '190px' },
  { key: 'granted', header: t('colBonusGranted'), text: (row) => credits(row.granted), numeric: true, width: '90px', sort: (row) => row.granted },
  { key: 'used', header: t('colBonusUsed'), text: (row) => credits(row.used), numeric: true, width: '90px', sort: (row) => row.used },
  { key: 'holders', header: t('colBonusHolders'), text: (row) => String(row.holders), numeric: true, width: '80px', sort: (row) => row.holders },
]);

// --- grants ------------------------------------------------------------------

const grants = ref<BonusGrantRow[]>([]);
const grantsTotal = ref(0);
const grantsError = ref('');
const grantsLoading = ref(false);
// Each request supersedes the ones before it: a page asked for before the
// operator reopened the bar, or before a second press, must not land on the
// list that is shown now.
let grantsTicket = 0;

async function loadGrants(barID: string, offset: number): Promise<void> {
  // A page already on its way asks for the same offset; a second press would
  // fetch it again and append it twice.
  if (offset && grantsLoading.value) return;
  const mine = ++grantsTicket;
  grantsLoading.value = true;
  grantsError.value = '';
  try {
    const result = await adminApi.bonusGrants(barID, offset);
    if (mine !== grantsTicket || existing.value?.id !== barID) return;
    const known = new Set(grants.value.map((row) => row.id));
    grants.value = offset
      ? [...grants.value, ...result.grants.filter((row) => !known.has(row.id))]
      : result.grants;
    grantsTotal.value = result.total;
  } catch (failure) {
    if (mine !== grantsTicket) return;
    grantsError.value = refusalLine(failure);
  } finally {
    if (mine === grantsTicket) grantsLoading.value = false;
  }
}

function grantLine(row: BonusGrantRow): string {
  const parts = [
    `${credits(row.used)} / ${credits(row.amount)}`,
    row.expires_at ? t('bonusExpires', { when: absoluteTime(row.expires_at) }) : t('bonusNoExpiry'),
    relativeTime(row.created_at),
  ];
  if (row.source === 'checkin') parts.push(t('bonusSourceCheckin'));
  // What a plugin package granted is marked with its name (rewards.bonus).
  else if (row.source.startsWith('plugin:')) parts.push(t('bonusSourcePlugin', { name: row.source.slice(7) }));
  if (row.note) parts.push(row.note);
  return parts.join(' · ');
}

// As with grant(): the revoke is committed before the reads run, so a failed
// read-back is reported at the page, not as a failed revoke.
async function revoke(row: BonusGrantRow): Promise<void> {
  let revoked: number;
  try {
    revoked = (await adminApi.revokeBonusGrant(row.id)).revoked;
  } catch (failure) {
    grantsError.value = refusalLine(failure);
    return;
  }
  flash.value = t('bonusRevoked', { amount: credits(revoked) });
  if (existing.value) await loadGrants(existing.value.id, 0);
  await refreshBars();
}

// Starts on named accounts: granting to everyone is one press of a button and
// cannot be taken back in bulk, so it is a choice made rather than a default.
const grantForm = ref({ to: 'named', group: '', names: '', amount: null as number | null, days: null as number | null, note: '' });
const toChoices = computed(() => [
  { value: 'all', label: t('bonusGrantEveryone') },
  { value: 'group', label: t('bonusGrantGroup') },
  { value: 'named', label: t('bonusGrantNamed') },
]);
const groupChoices = computed(() => groups.value.map((g) => ({ value: g.id, label: g.name })));

function openGrant(): void {
  grantForm.value = { to: 'named', group: groups.value[0]?.id ?? '', names: '', amount: null, days: null, note: '' };
  panelError.value = '';
  panel.value = 'grant';
}

async function grant(): Promise<void> {
  const bar = existing.value;
  if (!bar) return;
  const f = grantForm.value;
  busy.value = true;
  panelError.value = '';
  try {
    const result = await adminApi.grantBonus(bar.id, {
      amount: f.amount ?? 0,
      ...(f.to === 'all' ? { all: true } : {}),
      ...(f.to === 'group' ? { group_id: f.group } : {}),
      ...(f.to === 'named' ? { usernames: f.names.split(',').map((s) => s.trim()).filter(Boolean) } : {}),
      ...(f.days ? { days: f.days } : {}),
      note: f.note,
    });
    flash.value = tn(result.granted, 'bonusGrantedOne', 'bonusGrantedOther');
    panel.value = 'bar';
  } catch (failure) {
    // A grant's refusals carry codes (no such name, bar switched off), which
    // are worded in the reader's language here rather than the server's.
    panelError.value = apiErrorText(failure);
    return;
  } finally {
    busy.value = false;
  }
  // The grant is committed by here. A failed read-back is not a failed grant:
  // reporting it as one sends the operator back to grant again, doubling it.
  await refreshBars();
  void loadGrants(bar.id, 0);
}

// --- check-in ----------------------------------------------------------------

const checkin = ref<CheckinSettings>({ enabled: false, timezone: 'Asia/Shanghai', daily: { kind: '' }, rules: [] });
const checkinError = ref('');
const checkinFlash = ref('');
const checkinBusy = ref(false);
/**
 * A failed read leaves the form on its empty defaults, and saving those would
 * overwrite the stored rules. So the save is shut until a read succeeds; the
 * bars beside it are a separate read and stay up either way.
 */
const checkinLoadError = ref('');

async function loadCheckin(): Promise<void> {
  checkinLoadError.value = '';
  try {
    const current = await adminApi.checkinSettings();
    checkin.value = { ...current, rules: current.rules ?? [], daily: current.daily?.kind ? current.daily : { kind: '' } };
  } catch (failure) {
    checkinLoadError.value = refusalLine(failure);
  }
}

function addRule(): void {
  const n = checkin.value.rules.length + 1;
  const rule: CheckinRuleBody = {
    id: `m${Date.now().toString(36)}${n}`, title: '', basis: 'streak', days: 7, reward: { kind: 'bonus', bar_id: bars.value[0]?.id ?? '', amount: 1, valid_days: 7 },
  };
  checkin.value.rules.push(rule);
}

async function saveCheckin(): Promise<void> {
  checkinBusy.value = true;
  checkinError.value = '';
  checkinFlash.value = '';
  try {
    checkin.value = await adminApi.saveCheckinSettings(checkin.value);
    checkinFlash.value = t('checkinSaved');
  } catch (failure) {
    checkinError.value = refusalLine(failure);
  } finally {
    checkinBusy.value = false;
  }
}

const basisChoices = computed(() => [
  { value: 'streak', label: t('checkinBasisStreak') },
  { value: 'month', label: t('checkinBasisMonth') },
]);

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button id="addBonusBar" type="button" class="oa-btn primary" @click="openBar(null)">{{ t('addBonusBar') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <div v-else class="oa-workbench">
    <p v-if="pageError" class="oa-field-hint" role="alert">{{ pageError }}</p>
    <div class="oa-workbench-grid">
      <AdminControlCard id="secBonusBars" class="oa-control-card-wide" :title="t('secBonusBars')" :hint="t('secBonusBarsHint')" :icon="IconGift">
        <OaTable
          :columns="columns"
          :rows="bars"
          :empty="t('noBonusBars')"
          :muted="(row) => !row.active"
          :sort="sort"
          selectable
          @sort="sort = $event"
          @select="openBar($event)"
        >
          <template #cell-name="{ row }">
            <OaCellStack :title="row.name" :sub="row.description" />
          </template>
          <template #cell-rules="{ row }">
            <OaBadgeRow>
              <OaBadge v-if="row.kind === 'shared'">{{ t('bonusKindShared') }}</OaBadge>
              <OaBadge>{{ modeLabel(row) }}</OaBadge>
              <OaBadge v-if="!row.show_total" tone="muted">{{ t('bonusTotalHidden') }}</OaBadge>
              <OaBadge v-if="!row.active" tone="warning">{{ t('bonusInactive') }}</OaBadge>
            </OaBadgeRow>
          </template>
        </OaTable>
      </AdminControlCard>

      <AdminControlCard id="secCheckinAdmin" class="oa-control-card-wide" :title="t('secCheckinAdmin')" :hint="t('secCheckinAdminHint')" :icon="IconCheck">
        <template #actions>
          <button type="button" class="oa-btn primary" :disabled="checkinBusy || !!checkinLoadError" @click="saveCheckin">{{ t('checkinSave') }}</button>
        </template>
        <OaSwitchField v-model="checkin.enabled" :label="t('checkinEnabled')" :hint="t('checkinEnabledHint')" />
        <OaTextField v-model="checkin.timezone" :label="t('checkinTimezone')" :hint="t('checkinTimezoneHint')" placeholder="Asia/Shanghai" monospace />
        <CheckinRewardEditor v-model="checkin.daily" :bars="grantable" :label="t('checkinDailyHeading')" allow-none />

        <OaFormSection :title="t('checkinRulesHeading')" :hint="t('checkinRulesHint')" />
        <div v-for="(rule, index) in checkin.rules" :key="rule.id" class="oa-checkin-rule">
          <div class="oa-checkin-rule-basis">
            <OaTextField v-model="rule.title" :label="t('checkinRuleTitleField')" :placeholder="t('checkinRuleTitlePlaceholder')" :max-length="60" />
            <OaSelectField v-model="rule.basis" :label="t('checkinRuleBasis')" :options="basisChoices" :searchable="false" />
            <OaNumberField v-model="rule.days" :label="t('checkinRuleDays')" :min="1" :max="rule.basis === 'month' ? 31 : 366" />
          </div>
          <CheckinRewardEditor v-model="rule.reward" :bars="grantable" :label="t('checkinRewardKindField')" />
          <div class="oa-checkin-rule-foot">
            <button type="button" class="oa-btn" @click="checkin.rules.splice(index, 1)">{{ t('checkinRemoveRule') }}</button>
          </div>
        </div>
        <div>
          <button type="button" class="oa-btn" @click="addRule">{{ t('checkinAddRule') }}</button>
        </div>
        <p v-if="checkinFlash" class="oa-field-hint" role="status">{{ checkinFlash }}</p>
        <p v-if="checkinLoadError" class="oa-field-hint" role="alert">{{ checkinLoadError }}</p>
        <p v-if="checkinError" class="oa-field-hint" role="alert">{{ checkinError }}</p>
      </AdminControlCard>
    </div>
  </div>

  <OaPanel
    v-if="panel === 'bar'"
    :title="existing ? existing.name : t('addBonusBar')"
    :confirm-label="t('save')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing ? t('confirmDeleteBonus', { name: existing.name }) : undefined"
    :busy="busy"
    :error="panelError"
    @close="panel = 'closed'"
    @confirm="saveBar"
    @destructive="removeBar"
  >
    <OaTextField v-model="form.name" :label="t('bonusName')" :max-length="60" />
    <OaTextField v-model="form.description" :label="t('bonusDescription')" :hint="t('bonusDescriptionHint')" :max-length="500" />
    <OaSelectField v-model="form.kind" :label="t('bonusKind')" :hint="pt('kindHint')" :options="kindChoices" :searchable="false" />
    <OaSelectField
      v-if="form.kind !== 'reserve'"
      v-model="form.toggle_mode"
      :label="t('bonusMode')"
      :hint="form.kind === 'shared' ? pt('modeHint') : t('bonusModeHint')"
      :options="modeChoices"
      :searchable="false"
    />
    <OaSwitchField
      v-if="form.kind !== 'reserve' && form.toggle_mode === 'user'"
      v-model="form.default_on"
      :label="t('bonusDefaultOn')"
      :hint="t('bonusDefaultOnHint')"
    />
    <OaSwitchField v-model="form.show_total" :label="t('bonusShowTotal')" :hint="t('bonusShowTotalHint')" />
    <OaCheckList v-model="form.model_ids" :label="t('bonusModelsLabel')" :hint="t('bonusModelsHint')" :items="modelItems" :empty-text="t('nothingYet')" />
    <template v-if="form.kind === 'shared'">
      <OaFormSection :title="pt('heading')" />
      <OaSelectField v-model="form.reset_period" :label="pt('reset')" :hint="pt('resetHint')" :options="resetChoices" :searchable="false" />
      <OaNumberField v-if="form.reset_period" v-model="form.reset_amount" :label="pt('resetAmount')" :min="0" :step="1" />
      <OaNumberField v-if="!existing" v-model="form.initial" :label="pt('initial')" :hint="form.reset_period ? pt('initialHint') : undefined" :min="0" :step="1" />
      <OaNumberField v-model="form.per_user_daily" :label="pt('dailyLimit')" :hint="pt('dailyLimitHint')" :min="0" :step="0.1" />
    </template>
    <template v-else>
      <OaTextField v-model="form.expiry" type="datetime-local" :label="t('bonusDefaultExpiry')" :hint="t('bonusDefaultExpiryHint')" />
      <ExpiryPresets permanent @pick="form.expiry = $event" />
    </template>
    <OaSwitchField v-if="existing" v-model="form.active" :label="t('bonusActive')" :hint="t('bonusActiveHint')" />

    <template v-if="existing && existing.kind === 'shared'">
      <OaFormSection :title="pt('adjust')" />
      <p class="oa-field-hint">{{ poolLine(existing) }}</p>
      <OaSelectField v-model="adjust.op" :label="pt('adjustOp')" :options="adjustChoices" :searchable="false" />
      <OaNumberField v-model="adjust.amount" :label="pt('adjustAmount')" :hint="pt('adjustHint')" :step="1" />
      <div class="oa-card-actions">
        <button type="button" class="oa-btn" :disabled="busy || adjust.amount === null" @click="adjustPool">{{ pt('adjust') }}</button>
      </div>
      <p v-if="flash" class="oa-field-hint" role="status">{{ flash }}</p>
    </template>
    <template v-else-if="existing">
      <OaFormSection :title="t('bonusGrantsHeading')" />
      <div class="oa-card-actions">
        <button type="button" class="oa-btn" @click="openGrant">{{ t('bonusGrantAction') }}</button>
      </div>
      <p v-if="flash" class="oa-field-hint" role="status">{{ flash }}</p>
      <p v-if="grantsError" class="oa-field-hint">{{ grantsError }}</p>
      <p v-else-if="!grants.length" class="oa-field-hint">{{ t('bonusNoGrants') }}</p>
      <div v-else class="oa-card-list">
        <div v-for="row in grants" :key="row.id" class="oa-card-row">
          <OaCellStack :title="maskUser(row.username)" :sub="grantLine(row)" />
          <span class="oa-header-spacer" />
          <!-- Taking back removes credits from a balance, so it is armed first,
               and the armed label says how much is going. -->
          <OaConfirmButton
            class="oa-btn"
            :label="t('bonusRevoke')"
            :armed-label="t('bonusRevokeConfirm', { amount: credits(row.amount - row.used) })"
            :armed-title="t('bonusRevokeConfirm', { amount: credits(row.amount - row.used) })"
            :resting-title="t('bonusRevoke')"
            :disabled="row.amount <= row.used"
            @confirm="revoke(row)"
          />
        </div>
        <button v-if="grants.length < grantsTotal" type="button" class="oa-btn" :disabled="grantsLoading" @click="loadGrants(existing!.id, grants.length)">{{ t('bonusLoadMore') }}</button>
      </div>
    </template>
  </OaPanel>

  <OaPanel
    v-if="panel === 'grant' && existing"
    :title="t('bonusGrantTitle', { name: existing.name })"
    :confirm-label="t('bonusGrantAction')"
    :busy="busy"
    :error="panelError"
    back
    @close="panel = 'closed'"
    @back="panel = 'bar'"
    @confirm="grant"
  >
    <OaSelectField v-model="grantForm.to" :label="t('bonusGrantTo')" :options="toChoices" :searchable="false" />
    <OaSelectField v-if="grantForm.to === 'group'" v-model="grantForm.group" :label="t('bonusGrantGroupField')" :options="groupChoices" />
    <OaTextField v-if="grantForm.to === 'named'" v-model="grantForm.names" :label="t('bonusGrantNamesField')" :hint="t('bonusGrantNamesHint')" />
    <OaNumberField v-model="grantForm.amount" :label="t('bonusGrantAmount')" :min="0" :step="0.1" />
    <OaNumberField v-model="grantForm.days" :label="t('bonusGrantDays')" :hint="t('bonusGrantDaysHint')" :min="1" :max="MAX_GRANT_DAYS" />
    <OaTextField v-model="grantForm.note" :label="t('bonusGrantNote')" :max-length="200" />
  </OaPanel>
</template>
