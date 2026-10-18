<script setup lang="ts">
// The figure over the curve and the curve itself, with the point the pointer
// or the keyboard is on.
//
// The hovered point lives here rather than on the usage page because the
// figure and its caption are all that read it: held by the page, every point
// the pointer crossed re-ran the page's whole template, its boards, matrix
// and ledger included, to change two lines of text.

import { computed, ref, watch } from 'vue';
import type { UsagePoint, UsageTotals } from '@/admin/api';
import { t } from '@/composables/useI18n';
import UsagePlot from './UsagePlot.vue';
import type { PlotPoint } from './shape';

type TrendMetric = 'requests' | 'total_tokens' | 'credits' | 'users';

const props = defineProps<{
  /** The buckets, filled, that the points were drawn from. */
  series: readonly UsagePoint[];
  points: readonly PlotPoint[];
  totals: UsageTotals | null;
  metric: TrendMetric;
  format: (value: number) => string;
  bucketMs: number;
  offsetMs: number;
}>();

const active = ref<number | null>(null);
const plot = ref<InstanceType<typeof UsagePlot> | null>(null);

// A point picked on one line is not a point on the next: its figure would be
// read as the new metric's.
watch(() => props.metric, () => { active.value = null; });

function figure(value: number | undefined): number {
  return Number.isFinite(value) ? value! : 0;
}

const summary = computed(() => {
  const point = active.value === null ? null : props.series[active.value];
  if (point) return figure(point[props.metric]);
  // Accounts are counted once for the whole range, not summed per bucket:
  // somebody active every day is one account, not thirty.
  return props.totals ? figure(props.totals[props.metric]) : 0;
});
const caption = computed(() => {
  const point = active.value === null ? null : props.series[active.value];
  if (point && plot.value) return plot.value.describe(point.at);
  return props.metric === 'users' ? t('trendDistinct') : t('trendTotal');
});
const drawn = computed(() => props.points.some((point) => point.value > 0));
</script>

<template>
  <div class="oa-viz-summary">
    <strong>{{ props.format(summary) }}</strong>
    <span>{{ caption }}</span>
  </div>
  <UsagePlot
    v-if="drawn"
    ref="plot" v-model:active="active" :points="props.points" :bucket-ms="props.bucketMs"
    :offset-ms="props.offsetMs" :format="props.format" :label="t('dashboardChartKeyboard')" :height="190"
  />
  <p v-else class="oa-viz-empty">{{ t('noRequestsPeriod') }}</p>
</template>
