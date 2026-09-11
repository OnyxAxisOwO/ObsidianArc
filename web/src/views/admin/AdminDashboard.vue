<script setup lang="ts">
// The dashboard.
//
// Six numbers, one chart and two short lists. There is no attempt to fill the
// screen: an operator opens this to find out whether the server is working
// and what it is costing, and every panel that does not answer one of those
// is a panel they have to look past to find the ones that do.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type Dashboard, type UsageBreakdown, type UsageRecord, type UsageTotals } from '@/admin/api';
import OaAdminSection from '@/components/OaAdminSection.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaChart from '@/components/OaChart.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSpark from '@/components/OaSpark.vue';
import OaStatGrid from '@/components/OaStatGrid.vue';
import OaTable from '@/components/OaTable.vue';
import type { Column } from '@/components/table-types';
import type { Stat } from '@/components/stat';
import { t } from '@/composables/useI18n';
import type { ChartShape } from '@/lib/chart';
import { compactNumber, relativeTime } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import StatusBadge from './StatusBadge.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navDashboard'));

const data = ref<Dashboard | null>(null);
const error = ref('');

// Remembered across visits, so an operator who prefers a pie gets one.
const shape = ref<ChartShape>(dashboardShape);

function usageStats(totals: UsageTotals): Stat[] {
  return [
    {
      label: t('statRequests'),
      value: compactNumber(totals.requests),
      note: totals.errors ? t('nFailed', { count: totals.errors }) : t('allFine'),
    },
    {
      label: t('statTokens'),
      value: compactNumber(totals.total_tokens),
      note: t('tokensInOut', {
        input: compactNumber(totals.input_tokens),
        output: compactNumber(totals.output_tokens),
      }),
    },
    { label: t('statCredits'), value: compactNumber(totals.credits) },
  ];
}

const instanceStats = computed<Stat[]>(() => {
  const counts = data.value?.counts;
  if (!counts) return [];
  return [
    { label: t('statUsers'), value: String(counts.users), note: t('nActive', { count: counts.active_users }) },
    { label: t('statProviders'), value: String(counts.providers), note: t('nEnabled', { count: counts.enabled_providers }) },
    { label: t('statModels'), value: String(counts.models), note: t('nEnabled', { count: counts.enabled_models }) },
  ];
});

// The dashboard ranks by credits: it is the figure an operator is watching
// when they open this page at all.
function slices(rows: UsageBreakdown[]): Array<{ key: string; label: string; value: number }> {
  return rows.map((row) => ({ key: row.key, label: row.label || row.key || '—', value: row.credits }));
}

const busiestColumns = computed<Array<Column<UsageBreakdown>>>(() => [
  { key: 'model', header: t('colModel'), text: (row) => row.label || row.key },
  { key: 'requests', header: t('colRequests'), text: (row) => compactNumber(row.requests), numeric: true },
  { key: 'tokens', header: t('colTokens'), text: (row) => compactNumber(row.total_tokens), numeric: true, secondary: true },
  { key: 'credits', header: t('colCredits'), text: (row) => compactNumber(row.credits), numeric: true },
]);

const recentColumns = computed<Array<Column<UsageRecord>>>(() => [
  { key: 'when', header: t('colWhen'), text: (row) => relativeTime(row.started_at) },
  { key: 'user', header: t('colUser'), text: (row) => row.username || row.user_id },
  { key: 'model', header: t('colModel'), secondary: true },
  { key: 'tokens', header: t('colTokens'), text: (row) => compactNumber(row.total_tokens), numeric: true },
  { key: 'status', header: t('colStatus') },
]);

function onShape(next: ChartShape): void {
  shape.value = next;
  dashboardShape = next;
}

async function load(): Promise<void> {
  error.value = '';
  try {
    data.value = await adminApi.dashboard();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  }
}

onMounted(load);
</script>

<script lang="ts">
let dashboardShape: ChartShape = 'bar';
</script>

<template>
  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!data" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <OaAdminSection id="secInstance" :title="t('secInstance')">
      <OaStatGrid :stats="instanceStats" />
    </OaAdminSection>

    <OaAdminSection :title="t('secLast24h')">
      <OaStatGrid :stats="usageStats(data.last_24h)" />
    </OaAdminSection>

    <OaAdminSection :title="t('secLast7d')">
      <OaSpark :series="data.series" :bucket-ms="data.bucket_ms" :empty-text="t('noRequestsWeek')" />
    </OaAdminSection>

    <!-- Which models are popular and which accounts are heavy, side by side
         and in whichever shape reads better. Both charts share one shape
         control rather than having one each: they are two views of the same
         question, and letting them disagree about how to draw it would be a
         choice with no meaning behind it. -->
    <OaAdminSection
      v-if="data.top_models.length || data.top_users.length"
      :title="t('secRanking')"
    >
      <div class="oa-ranking">
        <div class="oa-ranking-controls">
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
        <div class="oa-ranking-pair">
          <div class="oa-ranking-column">
            <h4 class="oa-panel-section-title">{{ t('secBusiestModels') }}</h4>
            <OaChart
              :shape="shape"
              :data="slices(data.top_models)"
              :format="(value) => value.toFixed(2)"
              :empty-text="t('nothingYet')"
              :max="6"
              :other-label="t('chartOther')"
            />
          </div>
          <div class="oa-ranking-column">
            <h4 class="oa-panel-section-title">{{ t('secTopUsers') }}</h4>
            <OaChart
              :shape="shape"
              :data="slices(data.top_users)"
              :format="(value) => value.toFixed(2)"
              :empty-text="t('nothingYet')"
              :max="6"
              :other-label="t('chartOther')"
            />
          </div>
        </div>
      </div>
    </OaAdminSection>

    <OaAdminSection v-if="data.top_models.length" id="secBusiestModels" :title="t('secBusiestModels')">
      <OaTable :columns="busiestColumns" :rows="data.top_models" :empty="t('nothingYet')" />
    </OaAdminSection>

    <OaAdminSection id="secRecentRequests" :title="t('secRecentRequests')">
      <OaTable
        :columns="recentColumns"
        :rows="data.recent"
        :empty="t('noRequestsYet')"
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
</template>
