<script setup lang="ts">
// A bar per bucket, scaled to the tallest.
//
// Drawn with divs because a charting library would be many times the size of
// the one chart it draws, and this chart has no interaction to justify it.

import { computed } from 'vue';
import { t } from '@/composables/useI18n';
import { compactNumber } from '@/lib/format';

interface SparkPoint {
  at: number;
  requests: number;
  total_tokens: number;
}

const props = defineProps<{
  series: readonly SparkPoint[];
  bucketMs: number;
  emptyText: string;
}>();

const peak = computed(() => Math.max(...props.series.map((point) => point.total_tokens), 1));

function height(point: SparkPoint): string {
  return `${Math.max(2, Math.round((point.total_tokens / peak.value) * 100))}%`;
}

function tooltip(point: SparkPoint): string {
  return t('chartTooltip', {
    when: new Date(point.at).toLocaleString(),
    requests: compactNumber(point.requests),
    tokens: compactNumber(point.total_tokens),
  });
}
</script>

<template>
  <div class="oa-spark" :aria-label="t('chartAria', { hours: Math.round(props.bucketMs / 3600000) })">
    <span v-if="!props.series.length" class="oa-spark-empty">{{ props.emptyText }}</span>
    <template v-else>
      <div
        v-for="point in props.series"
        :key="point.at"
        class="oa-spark-bar"
        :style="{ height: height(point) }"
        :title="tooltip(point)"
      />
    </template>
  </div>
</template>
