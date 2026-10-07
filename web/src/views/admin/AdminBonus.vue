<script setup lang="ts">
// Bonus bars and check-in, the two ways an account gets more than its window.
//
// A bar is a set of rules (who may switch it, whether the total is shown, which
// models it covers) and a place to grant into. Check-in is what pressing a
// button once a day earns. The rules for both are in
// docs/architecture/bonus-and-checkin.md; this screen edits them.

import { computed, onMounted, ref } from 'vue';
import {
  adminApi,
  type AdminModel, type BonusBarRow, type BonusGrantRow, type CheckinRuleBody, type CheckinSettings, type Group,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaCheckList from '@/components/OaCheckList.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column, SortState } from '@/components/table-types';
import { t, tn } from '@/composables/useI18n';
import { absoluteTime, relativeTime } from '@/lib/format';
import { IconCheck, IconGift } from '@/icons';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';
import CheckinRewardEditor from './CheckinRewardEditor.vue';
import ExpiryPresets from './ExpiryPresets.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('bonusTitle'), t('bonusSubtitle'));

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
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

// --- the bars ----------------------------------------------------------------

interface BarForm {
  name: string;
  description: string;
  kind: 'bonus' | 'reserve';
  toggle_mode: 'user' | 'on' | 'off';
  default_on: boolean;
  show_total: boolean;
  model_ids: string[];
  expiry: string;
  active: boolean;
}

const panel = ref<'closed' | 'bar' | 'grant'>('closed');
const existing = ref<BonusBarRow | null>(null);
const form = ref<BarForm>(blankForm());
const busy = ref(false);
const panelError = ref('');
const flash = ref('');

function blankForm(): BarForm {
  return {
    name: '', description: '', kind: 'bonus', toggle_mode: 'user', default_on: true, show_total: true,
    model_ids: [], expiry: '', active: true,
  };
}

function toLocalInput(ms: number): string {
  if (!ms) return '';
  const d = new Date(ms);
  const pad = (n: number): string => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
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
    }
    : blankForm();
  panel.value = 'bar';
  if (row) void loadGrants(row.id, 0);
}

async function saveBar(): Promise<void> {
  busy.value = true;
  panelError.value = '';
  try {
    await adminApi.saveBonusBar(existing.value?.id ?? null, {
      name: form.value.name, description: form.value.description, kind: form.value.kind,
      toggle_mode: form.value.toggle_mode, default_on: form.value.default_on, show_total: form.value.show_total,
      model_ids: form.value.model_ids, default_expires_at: fromLocalInput(form.value.expiry), active: form.value.active,
    });
    panel.value = 'closed';
    bars.value = (await adminApi.bonusBars()).bars;
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

async function removeBar(): Promise<void> {
  if (!existing.value) return;
  busy.value = true;
  try {
    await adminApi.deleteBonusBar(existing.value.id);
    panel.value = 'closed';
    bars.value = (await adminApi.bonusBars()).bars;
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

const kindChoices = computed(() => [
  { value: 'bonus', label: t('bonusKindBonus') },
  { value: 'reserve', label: t('bonusKindReserve') },
]);
const modeChoices = computed(() => [
  { value: 'user', label: t('bonusModeUser') },
  { value: 'on', label: t('bonusModeOn') },
  { value: 'off', label: t('bonusModeOff') },
]);
const modelItems = computed(() => models.value.map((m) => ({ value: m.id, label: m.display_name || m.model_id, sub: m.provider_name })));

function modeLabel(row: BonusBarRow): string {
  if (row.kind === 'reserve') return t('bonusKindReserve');
  return modeChoices.value.find((c) => c.value === row.toggle_mode)?.label ?? row.toggle_mode;
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

async function loadGrants(barID: string, offset: number): Promise<void> {
  grantsError.value = '';
  try {
    const result = await adminApi.bonusGrants(barID, offset);
    if (existing.value?.id !== barID) return;
    grants.value = offset ? [...grants.value, ...result.grants] : result.grants;
    grantsTotal.value = result.total;
  } catch (failure) {
    grantsError.value = failure instanceof ApiError ? failure.message : String(failure);
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

async function revoke(row: BonusGrantRow): Promise<void> {
  try {
    const { revoked } = await adminApi.revokeBonusGrant(row.id);
    flash.value = t('bonusRevoked', { amount: credits(revoked) });
    if (existing.value) await loadGrants(existing.value.id, 0);
    bars.value = (await adminApi.bonusBars()).bars;
  } catch (failure) {
    grantsError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
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
    bars.value = (await adminApi.bonusBars()).bars;
    await loadGrants(bar.id, 0);
  } catch (failure) {
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

// --- check-in ----------------------------------------------------------------

const checkin = ref<CheckinSettings>({ enabled: false, timezone: 'Asia/Shanghai', daily: { kind: '' }, rules: [] });
const checkinError = ref('');
const checkinFlash = ref('');
const checkinBusy = ref(false);

async function loadCheckin(): Promise<void> {
  const current = await adminApi.checkinSettings();
  checkin.value = { ...current, rules: current.rules ?? [], daily: current.daily?.kind ? current.daily : { kind: '' } };
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
    checkinError.value = failure instanceof ApiError ? failure.message : String(failure);
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
              <OaBadge>{{ modeLabel(row) }}</OaBadge>
              <OaBadge v-if="!row.show_total" tone="muted">{{ t('bonusTotalHidden') }}</OaBadge>
              <OaBadge v-if="!row.active" tone="warning">{{ t('bonusInactive') }}</OaBadge>
            </OaBadgeRow>
          </template>
        </OaTable>
      </AdminControlCard>

      <AdminControlCard id="secCheckinAdmin" class="oa-control-card-wide" :title="t('secCheckinAdmin')" :hint="t('secCheckinAdminHint')" :icon="IconCheck">
        <template #actions>
          <button type="button" class="oa-btn primary" :disabled="checkinBusy" @click="saveCheckin">{{ t('checkinSave') }}</button>
        </template>
        <OaSwitchField v-model="checkin.enabled" :label="t('checkinEnabled')" :hint="t('checkinEnabledHint')" />
        <OaTextField v-model="checkin.timezone" :label="t('checkinTimezone')" :hint="t('checkinTimezoneHint')" placeholder="Asia/Shanghai" monospace />
        <CheckinRewardEditor v-model="checkin.daily" :bars="bars" :label="t('checkinDailyHeading')" allow-none />

        <OaFormSection :title="t('checkinRulesHeading')" :hint="t('checkinRulesHint')" />
        <div v-for="(rule, index) in checkin.rules" :key="rule.id" class="oa-checkin-rule">
          <div class="oa-checkin-rule-basis">
            <OaTextField v-model="rule.title" :label="t('checkinRuleTitleField')" :placeholder="t('checkinRuleTitlePlaceholder')" :max-length="60" />
            <OaSelectField v-model="rule.basis" :label="t('checkinRuleBasis')" :options="basisChoices" :searchable="false" />
            <OaNumberField v-model="rule.days" :label="t('checkinRuleDays')" :min="1" :max="rule.basis === 'month' ? 31 : 366" />
          </div>
          <CheckinRewardEditor v-model="rule.reward" :bars="bars" :label="t('checkinRewardKindField')" />
          <div class="oa-checkin-rule-foot">
            <button type="button" class="oa-btn" @click="checkin.rules.splice(index, 1)">{{ t('checkinRemoveRule') }}</button>
          </div>
        </div>
        <div>
          <button type="button" class="oa-btn" @click="addRule">{{ t('checkinAddRule') }}</button>
        </div>
        <p v-if="checkinFlash" class="oa-field-hint" role="status">{{ checkinFlash }}</p>
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
    <OaSelectField v-model="form.kind" :label="t('bonusKind')" :hint="t('bonusKindHint')" :options="kindChoices" :searchable="false" />
    <OaSelectField
      v-if="form.kind === 'bonus'"
      v-model="form.toggle_mode"
      :label="t('bonusMode')"
      :hint="t('bonusModeHint')"
      :options="modeChoices"
      :searchable="false"
    />
    <OaSwitchField
      v-if="form.kind === 'bonus' && form.toggle_mode === 'user'"
      v-model="form.default_on"
      :label="t('bonusDefaultOn')"
      :hint="t('bonusDefaultOnHint')"
    />
    <OaSwitchField v-model="form.show_total" :label="t('bonusShowTotal')" :hint="t('bonusShowTotalHint')" />
    <OaCheckList v-model="form.model_ids" :label="t('bonusModelsLabel')" :hint="t('bonusModelsHint')" :items="modelItems" :empty-text="t('nothingYet')" />
    <OaTextField v-model="form.expiry" type="datetime-local" :label="t('bonusDefaultExpiry')" :hint="t('bonusDefaultExpiryHint')" />
    <ExpiryPresets permanent @pick="form.expiry = $event" />
    <OaSwitchField v-if="existing" v-model="form.active" :label="t('bonusActive')" :hint="t('bonusActiveHint')" />

    <template v-if="existing">
      <OaFormSection :title="t('bonusGrantsHeading')" />
      <div class="oa-card-actions">
        <button type="button" class="oa-btn" @click="openGrant">{{ t('bonusGrantAction') }}</button>
      </div>
      <p v-if="flash" class="oa-field-hint" role="status">{{ flash }}</p>
      <p v-if="grantsError" class="oa-field-hint">{{ grantsError }}</p>
      <p v-else-if="!grants.length" class="oa-field-hint">{{ t('bonusNoGrants') }}</p>
      <div v-else class="oa-card-list">
        <div v-for="row in grants" :key="row.id" class="oa-card-row">
          <OaCellStack :title="row.username" :sub="grantLine(row)" />
          <span class="oa-header-spacer" />
          <button type="button" class="oa-btn" :disabled="row.amount <= row.used" @click="revoke(row)">{{ t('bonusRevoke') }}</button>
        </div>
        <button v-if="grants.length < grantsTotal" type="button" class="oa-btn" @click="loadGrants(existing!.id, grants.length)">{{ t('bonusLoadMore') }}</button>
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
    <OaNumberField v-model="grantForm.days" :label="t('bonusGrantDays')" :hint="t('bonusGrantDaysHint')" :min="1" :max="3650" />
    <OaTextField v-model="grantForm.note" :label="t('bonusGrantNote')" :max-length="200" />
  </OaPanel>
</template>
