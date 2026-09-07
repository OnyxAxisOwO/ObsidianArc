<script setup lang="ts">
// One allowance window, as a labelled figure over a bar.
//
// Two screens draw these and have nothing else in common — the composer's
// menu, where somebody reads their own allowance before sending, and the
// administration panel, where somebody reads another account's. The decisions
// that make the bars honest are the same in both places and are made here:
// which dimension a window is judged on, which way the bar travels, and how
// the figure beside it is phrased.
//
// A window with no limit on any dimension gets no bar: there is nothing for
// it to be a fraction of, and a full-width track would say "at the ceiling"
// about an account that has none.

import { computed } from 'vue';
import { windowFigures, windowPressure, type UsageDisplay, type UsageWindow } from '@/api/usage';
import { t, type StringKey } from '@/composables/useI18n';

const props = withDefaults(defineProps<{
  window: UsageWindow;
  display?: UsageDisplay;
  size?: 'compact' | 'large';
}>(), { display: 'absolute', size: 'compact' });

const pressure = computed(() => windowPressure(props.window));

const label = computed<StringKey>(() => {
  if (props.window.kind === '5h') return 'quota5h';
  if (props.window.kind === '1w') return 'quotaWeek';
  return 'quotaMonth';
});

/**
 * The figure beside a window's name, in whichever phrasing the instance
 * chose. A percentage needs a ratio to exist; with no limit on any dimension
 * there is nothing to be a percentage of, so those windows fall back to the
 * count regardless of the setting.
 */
const value = computed(() => {
  const ratio = pressure.value;
  if (props.display !== 'absolute' && ratio !== null) {
    const percent = Math.round(ratio * 100);
    return props.display === 'remaining'
      ? t('quotaRemaining', { percent: Math.max(0, 100 - percent) })
      : t('quotaUsed', { percent });
  }
  const figures = windowFigures(props.window);
  return figures
    ? `${compact(figures.used)} / ${compact(figures.limit)}`
    : compact(props.window.used_requests);
});

/**
 * The bar has to travel the way the figure beside it reads. A track filled a
 * tenth under the words "90% left" is two answers to one question, and at a
 * glance the shape is the one believed. So an allowance phrased as what
 * remains drains as it is spent; used, and the raw figures, fill up.
 */
const fillWidth = computed(() => {
  const ratio = pressure.value ?? 0;
  const draining = props.display === 'remaining';
  return `${Math.round((draining ? 1 - ratio : ratio) * 100)}%`;
});

// Keyed to pressure rather than to the width: nearly gone is nearly gone
// whichever direction the bar happens to be travelling.
const warn = computed(() => (pressure.value ?? 0) >= 0.9);

// Thousands as "1.2k": one of these rows has no space for six digits, and the
// exact figure is not what anyone reads here.
function compact(amount: number): string {
  if (amount < 1000) return String(Math.round(amount * 10) / 10);
  if (amount < 1_000_000) return `${Math.round(amount / 100) / 10}k`;
  return `${Math.round(amount / 100_000) / 10}M`;
}

const resets = computed(() => {
  const minutes = Math.max(0, Math.round((props.window.resets_at - Date.now()) / 60000));
  if (minutes < 60) return t('inMinutes', { count: minutes });
  const hours = Math.round(minutes / 60);
  if (hours < 48) return t('inHours', { count: hours });
  return t('inDays', { count: Math.round(hours / 24) });
});
</script>

<template>
  <div :class="props.size === 'large' ? 'oa-usage-large' : undefined">
    <div class="oa-usage-row">
      <span>{{ t(label) }}</span>
      <span class="oa-usage-value">{{ value }}</span>
    </div>
    <div v-if="pressure !== null" class="oa-meter">
      <div class="oa-meter-fill" :class="{ warn }" :style="{ width: fillWidth }" />
    </div>
    <div class="oa-usage-reset">{{ t('quotaResets', { when: resets }) }}</div>
  </div>
</template>
