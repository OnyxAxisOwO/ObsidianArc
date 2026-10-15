<script setup lang="ts">
// The week on the overview: requests or tokens, six hours to a point.
//
// The plot is the usage report's, so the two pages draw time the same way —
// one curve, one grid, one way to read a point — and a fix to either reaches
// both.

import { computed, ref, watch } from 'vue';
import type { UsagePoint, UsageTotals } from '@/admin/api';
import { maskBilling } from '@/admin/safeMode';
import { t } from '@/composables/useI18n';
import { compactNumber } from '@/lib/format';
import UsagePlot from './usage/UsagePlot.vue';
import { fillBuckets } from './usage/scale';

const props = defineProps<{ series: readonly UsagePoint[]; totals: UsageTotals; bucketMs: number }>();
const metric = ref<'requests' | 'total_tokens'>('requests');
const active = ref<number | null>(null);
const plot = ref<InstanceType<typeof UsagePlot> | null>(null);

// The clock the server cut its buckets on. Both the fill and the plot's axis
// read it, so they must share one value.
const offsetMs = -new Date().getTimezoneOffset() * 60_000;

// The week, with its silent hours put back: see fillBuckets.
const points = computed(() => {
  const now = Date.now();
  const filled = fillBuckets(props.series, props.bucketMs, {
    since: now - 7 * 86_400_000, until: now, offsetMs,
  }, (at): UsagePoint => ({
    at, requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0,
    credits: 0, errors: 0, users: 0, models: 0, duration_ms: 0,
  }));
  return filled.map((point) => ({ at: point.at, value: point[metric.value] }));
});
const metricLabel = computed(() => t(metric.value === 'requests' ? 'statRequests' : 'statTokens'));
const hasValues = computed(() => points.value.some((point) => point.value > 0));
const selected = computed(() => active.value === null ? null : points.value[active.value] ?? null);
const value = computed(() => selected.value?.value ?? props.totals[metric.value]);
const selectedLabel = computed(() => selected.value && plot.value ? plot.value.describe(selected.value.at) : t('dashboardWeekTotal'));
const peak = computed(() => Math.max(0, ...points.value.map((point) => point.value)));
// Through safe mode when the curve is tokens: a request count is not
// spending, but the axis, the summary figure and the peak beside a token
// trend are, and hovering to read them is exactly what a recording catches.
const format = computed(() => metric.value === 'total_tokens'
  ? (raw: number) => maskBilling(compactNumber(raw))
  : compactNumber);

watch(metric, () => { active.value = null; });
</script>

<template>
  <section class="oa-dashboard-card oa-dashboard-trend">
    <div class="oa-dashboard-section-head">
      <div><span class="oa-dashboard-kicker">{{ t('secLast7d') }}</span><h2>{{ t('dashboardTrend') }}</h2></div>
      <div class="oa-segment" role="group" :aria-label="t('dashboardTrendMetric')">
        <button type="button" :aria-pressed="metric === 'requests'" @click="metric = 'requests'">{{ t('statRequests') }}</button>
        <button type="button" :aria-pressed="metric === 'total_tokens'" @click="metric = 'total_tokens'">{{ t('statTokens') }}</button>
      </div>
    </div>
    <div class="oa-dashboard-trend-summary">
      <div><strong>{{ format(value) }}</strong><span>{{ metricLabel }}</span></div>
      <span>{{ selectedLabel }}</span>
    </div>
    <UsagePlot
      v-if="hasValues"
      ref="plot"
      v-model:active="active"
      :points="points"
      :bucket-ms="props.bucketMs"
      :offset-ms="offsetMs"
      :format="format"
      :label="t('dashboardChartKeyboard')"
      :height="176"
    />
    <p v-else class="oa-dashboard-empty oa-dashboard-trend-empty">{{ t(metric === 'requests' ? 'noRequestsWeek' : 'dashboardNoTokens') }}</p>
    <div class="oa-dashboard-trend-foot">
      <span><span class="oa-dashboard-dot" />{{ t('dashboardBucket', { hours: props.bucketMs / 3600000 }) }}</span>
      <span>{{ t('dashboardPeak', { value: format(peak) }) }}</span>
    </div>
  </section>
</template>
