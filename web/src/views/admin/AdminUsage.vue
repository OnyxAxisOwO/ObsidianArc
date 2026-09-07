<script setup lang="ts">
// Usage: what has been spent, by whom, on what — and the instance-wide
// default allowance.
//
// Every figure here is an aggregate over the ledger, so the same page answers
// "what is this costing" and "why did that request fail" without either being
// a separate feature.

import { computed, onMounted, ref } from 'vue';
import {
  adminApi, emptyPolicy,
  type Group, type QuotaWindowKind, type UsageBreakdown, type UsageMetric,
  type UsageRecord, type UsageTotals,
} from '@/admin/api';
import { ApiError } from '@/api/client';
import OaAdminSection from '@/components/OaAdminSection.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaChart from '@/components/OaChart.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaHoldButton from '@/components/OaHoldButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSpark from '@/components/OaSpark.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { celebrate } from '@/composables/useConfetti';
import { t, type StringKey } from '@/composables/useI18n';
import type { ChartShape } from '@/lib/chart';
import { compactNumber, relativeTime } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import CreditsField from './CreditsField.vue';
import StatusBadge from './StatusBadge.vue';
import { useAdminView } from './adminView';

const RANGES: Array<{ label: StringKey; hours: number }> = [
  { label: 'rangeDay', hours: 24 },
  { label: 'rangeWeek', hours: 24 * 7 },
  { label: 'rangeMonth', hours: 24 * 30 },
];

const WINDOWS: QuotaWindowKind[] = ['5h', '1w', '1m'];

const view = useAdminView();
view.setTitle(t('usageTitle'));

const range = ref(String(selectedRange));
// Remembered across visits: an operator who looks at credits by user does it
// again next time, and having to choose twice is friction with no benefit.
const metric = ref<UsageMetric>(selectedMetric);
const shape = ref<ChartShape>(selectedShape);
const dimension = ref<'model' | 'user' | 'provider'>(selectedDimension);

const totals = ref<UsageTotals | null>(null);
const byModel = ref<UsageBreakdown[]>([]);
const byProvider = ref<UsageBreakdown[]>([]);
const byUser = ref<UsageBreakdown[]>([]);
const series = ref<Array<{ at: number; requests: number; total_tokens: number }>>([]);
const bucketMs = ref(3600000);
const records = ref<UsageRecord[]>([]);
const recordTotal = ref(0);
const error = ref('');
const loaded = ref(false);

const totalStats = computed<Stat[]>(() => {
  const value = totals.value;
  if (!value) return [];
  return [
    {
      label: t('statRequests'),
      value: compactNumber(value.requests),
      note: value.errors ? t('nFailed', { count: value.errors }) : t('allFine'),
    },
    { label: t('statInputTokens'), value: compactNumber(value.input_tokens) },
    { label: t('statOutputTokens'), value: compactNumber(value.output_tokens) },
    { label: t('statReasoningTokens'), value: compactNumber(value.reasoning_tokens) },
    { label: t('statCredits'), value: compactNumber(value.credits) },
  ];
});

/** The figure the current metric ranks by, for the chart's own arithmetic. */
function metricValue(row: UsageBreakdown): number {
  if (metric.value === 'requests') return row.requests;
  if (metric.value === 'tokens') return row.total_tokens;
  return row.credits;
}

const ranked = computed(() => {
  const rows = dimension.value === 'model' ? byModel.value
    : dimension.value === 'user' ? byUser.value : byProvider.value;
  return rows.map((row) => ({
    key: row.key,
    label: row.label || row.key || '—',
    value: metricValue(row),
  }));
});

const breakdownColumns = computed<Array<Column<UsageBreakdown>>>(() => [
  { key: 'name', header: t('colModel'), text: (row) => row.label || row.key || '—' },
  { key: 'requests', header: t('colRequests'), text: (row) => compactNumber(row.requests), numeric: true },
  { key: 'tokens', header: t('colTokens'), text: (row) => compactNumber(row.total_tokens), numeric: true },
  { key: 'credits', header: t('colCredits'), text: (row) => compactNumber(row.credits), numeric: true },
  { key: 'failed', header: t('colFailed'), text: (row) => compactNumber(row.errors), numeric: true, secondary: true },
]);

const providerColumns = computed<Array<Column<UsageBreakdown>>>(() => [
  { key: 'name', header: t('colProvider'), text: (row) => row.label || row.key || '—' },
  { key: 'requests', header: t('colRequests'), text: (row) => compactNumber(row.requests), numeric: true },
  { key: 'tokens', header: t('colTokens'), text: (row) => compactNumber(row.total_tokens), numeric: true },
  { key: 'credits', header: t('colCredits'), text: (row) => compactNumber(row.credits), numeric: true },
]);

const recordColumns = computed<Array<Column<UsageRecord>>>(() => [
  { key: 'when', header: t('colWhen'), text: (row) => relativeTime(row.started_at) },
  { key: 'user', header: t('colUser'), text: (row) => row.username || row.user_id },
  { key: 'model', header: t('colModel'), secondary: true },
  { key: 'in', header: t('colIn'), text: (row) => compactNumber(row.input_tokens), numeric: true, secondary: true },
  { key: 'out', header: t('colOut'), text: (row) => compactNumber(row.output_tokens), numeric: true, secondary: true },
  { key: 'credits', header: t('colCredits'), text: (row) => compactNumber(row.credits), numeric: true },
  { key: 'took', header: t('colTook'), text: (row) => `${(row.duration_ms / 1000).toFixed(1)}s`, numeric: true, secondary: true },
  { key: 'status', header: t('colStatus') },
]);

function onRange(next: string): void {
  range.value = next;
  selectedRange = Number(next);
  void load();
}

function onMetric(next: UsageMetric): void {
  metric.value = next;
  selectedMetric = next;
  // A different ranking is a different query, not a different view of the
  // same fifty rows.
  void load();
}

function onShape(next: ChartShape): void {
  shape.value = next;
  selectedShape = next;
}

function onDimension(next: 'model' | 'user' | 'provider'): void {
  dimension.value = next;
  selectedDimension = next;
}

// --- the default allowance ------------------------------------------------------

const policyOpen = ref(false);
const policyBusy = ref(false);
const policyError = ref('');
const policyForm = ref({
  rpm: null as number | null,
  tpm: null as number | null,
  windows: {} as Record<QuotaWindowKind, { enabled: boolean; requests: number | null; tokens: number | null; credits: number | null }>,
});

async function openPolicy(): Promise<void> {
  let policy = emptyPolicy('global', '');
  try {
    const { policies } = await adminApi.policies();
    policy = policies.find((entry) => entry.scope === 'global') ?? policy;
  } catch {
    // Fall through with the blank policy; saving will create it.
  }
  const windows = {} as typeof policyForm.value.windows;
  for (const kind of WINDOWS) {
    const limits = policy.windows[kind];
    windows[kind] = {
      enabled: limits?.enabled === true,
      requests: limits?.requests ?? null,
      tokens: limits?.tokens ?? null,
      credits: limits?.credits ?? null,
    };
  }
  policyForm.value = { rpm: policy.rpm, tpm: policy.tpm, windows };
  policyError.value = '';
  policyOpen.value = true;
}

async function savePolicy(): Promise<void> {
  policyBusy.value = true;
  try {
    await adminApi.savePolicy({
      scope: 'global',
      scope_id: '',
      rpm: policyForm.value.rpm,
      tpm: policyForm.value.tpm,
      windows: Object.fromEntries(WINDOWS.map((kind) => [kind, {
        enabled: policyForm.value.windows[kind].enabled ? true : null,
        requests: policyForm.value.windows[kind].requests,
        tokens: policyForm.value.windows[kind].tokens,
        credits: policyForm.value.windows[kind].credits,
      }])),
    });
    policyOpen.value = false;
    view.reload();
  } catch (failure) {
    policyError.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    policyBusy.value = false;
  }
}

// --- putting an allowance back --------------------------------------------------

const resetOpen = ref(false);
const resetScope = ref<'all' | 'group' | 'user'>('all');
const resetGroup = ref('');
const resetAccount = ref('');
const resetGroups = ref<Group[]>([]);
const resetTitle = ref('');
const resetError = ref('');

async function openReset(): Promise<void> {
  resetScope.value = 'all';
  resetAccount.value = '';
  resetError.value = '';
  resetTitle.value = t('resetQuota');
  try {
    ({ groups: resetGroups.value } = await adminApi.groups());
    resetGroup.value = resetGroups.value[0]?.id ?? '';
  } catch {
    // The group option simply will not be offered. Everyone and one account
    // still work, and refusing to open at all would be worse.
  }
  resetOpen.value = true;
}

async function runReset(): Promise<void> {
  const scope = resetScope.value;
  if (scope === 'group' && !resetGroup.value) {
    resetError.value = t('resetNoGroup');
    return;
  }
  if (scope === 'user' && !resetAccount.value.trim()) {
    resetError.value = t('resetNoAccount');
    return;
  }
  try {
    const body = scope === 'all'
      ? { scope } as const
      : { scope, id: scope === 'group' ? resetGroup.value : resetAccount.value.trim() };
    const { accounts } = await adminApi.resetQuota(body);
    resetTitle.value = t('resetDone', { count: accounts });
    resetError.value = '';
    celebrate();
    view.reload();
  } catch (failure) {
    resetError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function load(): Promise<void> {
  error.value = '';
  const since = Date.now() - RANGES[Number(range.value)]!.hours * 3600_000;
  // The ranking is done in SQL, so which metric is being asked for has to go
  // with the request: the top fifty by credits is not the top fifty by
  // request count.
  const query = `?since=${since}&metric=${metric.value}`;
  try {
    const [summary, recordsResult] = await Promise.all([
      adminApi.usage(query),
      adminApi.usageRecords(`${query}&limit=50`),
    ]);
    totals.value = summary.totals;
    byModel.value = summary.by_model;
    byProvider.value = summary.by_provider;
    byUser.value = summary.by_user;
    series.value = summary.series;
    bucketMs.value = summary.bucket_ms;
    records.value = recordsResult.records;
    recordTotal.value = recordsResult.total;
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);
</script>

<script lang="ts">
let selectedRange = 1;
let selectedMetric: UsageMetric = 'credits';
let selectedShape: ChartShape = 'bar';
let selectedDimension: 'model' | 'user' | 'provider' = 'model';
</script>

<template>
  <Teleport :to="view.actionsHost">
    <div class="oa-filters" style="margin: 0">
      <OaSelect
        :model-value="range"
        class="oa-filter-select"
        :choices="RANGES.map((entry, index) => ({ value: String(index), label: t(entry.label) }))"
        @update:model-value="onRange"
      />
    </div>
    <button type="button" class="oa-btn" @click="openPolicy">{{ t('defaultLimits') }}</button>
    <button type="button" class="oa-btn oa-btn-danger" @click="openReset">{{ t('resetQuota') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <OaAdminSection :title="t('secTotals')">
      <OaStatGrid :stats="totalStats" />
    </OaAdminSection>

    <OaAdminSection :title="t('secOverTime')">
      <OaSpark :series="series" :bucket-ms="bucketMs" :empty-text="t('noRequestsPeriod')" />
    </OaAdminSection>

    <!-- Three choices rather than one, because "who used the most" has three
         defensible answers and a chart that picks silently is a chart that
         misleads. Shape and dimension repaint from what is already loaded;
         changing the metric refetches, because the ranking is the server's. -->
    <OaAdminSection :title="t('secRanking')">
      <div class="oa-ranking">
        <div class="oa-ranking-controls">
          <OaSelectField
            :model-value="dimension"
            :label="t('rankBy')"
            :options="[
              { value: 'model', label: t('rankModels') },
              { value: 'user', label: t('rankUsers') },
              { value: 'provider', label: t('rankProviders') },
            ]"
            @update:model-value="onDimension"
          />
          <OaSelectField
            :model-value="metric"
            :label="t('rankMetric')"
            :options="[
              { value: 'credits', label: t('metricCredits') },
              { value: 'tokens', label: t('metricTokens') },
              { value: 'requests', label: t('metricRequests') },
            ]"
            @update:model-value="onMetric"
          />
          <OaSelectField
            :model-value="shape"
            :label="t('chartShape')"
            :options="[
              { value: 'bar', label: t('chartBar') },
              { value: 'pie', label: t('chartPie') },
            ]"
            @update:model-value="onShape"
          />
        </div>
        <div class="oa-ranking-canvas">
          <OaChart
            :shape="shape"
            :data="ranked"
            :format="(value) => (metric === 'credits' ? value.toFixed(2) : compactNumber(value))"
            :empty-text="t('nothingInPeriod')"
            :other-label="t('chartOther')"
          />
        </div>
      </div>
    </OaAdminSection>

    <OaAdminSection :title="t('secByModel')">
      <OaTable :columns="breakdownColumns" :rows="byModel" :empty="t('nothingInPeriod')" />
    </OaAdminSection>

    <OaAdminSection :title="t('secByProvider')">
      <OaTable :columns="providerColumns" :rows="byProvider" :empty="t('nothingInPeriod')" />
    </OaAdminSection>

    <OaAdminSection :title="t('secRequestsN', { count: recordTotal })">
      <OaTable
        :columns="recordColumns"
        :rows="records"
        :empty="t('noRequestsPeriod')"
        :muted="(row) => row.status !== 'ok'"
      >
        <template #cell-model="{ row }">
          <OaCellStack :title="row.model_name || '—'" :sub="row.provider_name" />
        </template>
        <template #cell-status="{ row }">
          <StatusBadge :status="row.status" :error-code="row.error_code" />
        </template>
      </OaTable>
    </OaAdminSection>
  </template>

  <!-- The instance default: what applies to anyone whose group and account say
       nothing. Edited here rather than on the groups page because it is not a
       group. -->
  <OaPanel
    v-if="policyOpen"
    :title="t('defaultLimits')"
    :confirm-label="t('save')"
    :busy="policyBusy"
    :error="policyError"
    @close="policyOpen = false"
    @confirm="savePolicy"
  >
    <p class="oa-field-hint">{{ t('adminsExemptHint') }}</p>
    <OaNumberField
      v-model="policyForm.rpm"
      :label="t('requestsPerMinute')"
      :placeholder="t('noLimit')"
      :min="0"
      :hint="t('defaultLimitsHint')"
    />
    <OaNumberField
      v-model="policyForm.tpm"
      :label="t('tokensPerMinute')"
      :placeholder="t('noLimit')"
      :min="0"
    />
    <template v-for="kind in WINDOWS" :key="kind">
      <OaFormSection :title="t('everyWindow', { window: kind })" />
      <OaSwitchField
        v-model="policyForm.windows[kind].enabled"
        :label="t('enforceTheWindow', { window: kind })"
      />
      <OaNumberField
        v-model="policyForm.windows[kind].requests"
        :label="t('limitRequests')"
        :placeholder="t('noLimit')"
        :min="0"
      />
      <OaNumberField
        v-model="policyForm.windows[kind].tokens"
        :label="t('limitTokens')"
        :placeholder="t('noLimit')"
        :min="0"
      />
      <CreditsField v-model="policyForm.windows[kind].credits" />
    </template>
  </OaPanel>

  <!-- The scope is chosen before the button that does it appears, because
       "reset" with no number beside it is the same word whether it means one
       person or everyone, and the difference is the entire decision. -->
  <OaPanel
    v-if="resetOpen"
    :title="resetTitle"
    :footer="false"
    :error="resetError"
    @close="resetOpen = false"
  >
    <p class="oa-field-hint">{{ t('resetExplain') }}</p>
    <OaSelectField
      v-model="resetScope"
      :label="t('resetScope')"
      :options="[
        { value: 'all', label: t('resetScopeAll') },
        { value: 'group', label: t('resetScopeGroup') },
        { value: 'user', label: t('resetScopeUser') },
      ]"
    />
    <OaSelectField
      v-if="resetScope === 'group'"
      v-model="resetGroup"
      :label="t('groupsTitle')"
      :options="resetGroups.map((entry) => ({ value: entry.id, label: entry.name }))"
    />
    <OaTextField
      v-if="resetScope === 'user'"
      v-model="resetAccount"
      :label="t('resetAccountID')"
      placeholder="01ARZ3NDEKTSV4RRFFQ69G5FAV"
      :hint="t('resetAccountIDHint')"
      monospace
    />
    <OaHoldButton
      :label="t('resetConfirmLabel')"
      :holding-label="t('resetHolding')"
      @fire="runReset"
    />
    <p class="oa-field-hint oa-hold-note">{{ t('resetHoldHint') }}</p>
  </OaPanel>
</template>
