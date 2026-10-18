<script setup lang="ts">
// When in the week the instance is used: seven rows of twenty-four hours.
//
// Monday first whatever the locale, because this is read to plan around a
// working week — maintenance, a price change, a model swap — and a week that
// starts on Sunday splits the weekend across both ends of the chart.

import { computed, ref } from 'vue';
import type { UsageSlot } from '@/admin/api';
import { currentLanguage, t } from '@/composables/useI18n';
import { DAY, intensity } from './scale';

const props = defineProps<{
  slots: readonly UsageSlot[];
  metric: 'requests' | 'total_tokens';
  format: (value: number) => string;
}>();

const ORDER = [1, 2, 3, 4, 5, 6, 0];
const HOURS = Array.from({ length: 24 }, (_, hour) => hour);
// A Sunday, so that adding a weekday number lands on that weekday.
const SUNDAY = Date.UTC(2023, 11, 31);

const locale = computed(() => currentLanguage() === 'zh' ? 'zh-CN' : 'en-US');
const hovered = ref<{ weekday: number; hour: number } | null>(null);

const grid = computed(() => {
  const cells = new Map<string, number>();
  for (const slot of props.slots) cells.set(`${slot.weekday}:${slot.hour}`, slot[props.metric]);
  return cells;
});
const peak = computed(() => {
  let best: { weekday: number; hour: number; value: number } | null = null;
  for (const slot of props.slots) {
    const value = slot[props.metric];
    if (value > 0 && (!best || value > best.value)) best = { weekday: slot.weekday, hour: slot.hour, value };
  }
  return best;
});

function value(weekday: number, hour: number): number {
  return grid.value.get(`${weekday}:${hour}`) ?? 0;
}
// The emptiest occupied hour still has to read as occupied, so the ramp starts
// at a visible tint rather than at nothing.
function heatOf(level: number): string {
  return `${Math.round(14 + level * 86)}%`;
}
// The fill of every slot, worked out when the data changes. Hovering moves the
// active cell alone, so it must not make the template work every fill out again.
const days = computed(() => ORDER.map((weekday) => ({
  weekday,
  hours: HOURS.map((hour) => {
    const level = intensity(value(weekday, hour), peak.value?.value ?? 0);
    return { hour, heat: level > 0 ? heatOf(level) : undefined };
  }),
})));
function dayName(weekday: number): string {
  return new Date(SUNDAY + weekday * DAY).toLocaleDateString(locale.value, { weekday: 'short', timeZone: 'UTC' });
}
function hourName(hour: number): string {
  return `${String(hour).padStart(2, '0')}:00`;
}
function describe(weekday: number, hour: number): string {
  return t('heatmapSlot', {
    day: dayName(weekday), from: hourName(hour), to: hourName((hour + 1) % 24),
    value: props.format(value(weekday, hour)),
  });
}

const caption = computed(() => {
  if (hovered.value) return describe(hovered.value.weekday, hovered.value.hour);
  if (!peak.value) return t('heatmapQuiet');
  return t('heatmapPeak', { day: dayName(peak.value.weekday), hour: hourName(peak.value.hour), value: props.format(peak.value.value) });
});
</script>

<template>
  <div class="oa-heatmap" role="img" :aria-label="caption" @pointerleave="hovered = null">
    <p class="oa-heatmap-caption" aria-hidden="true">{{ caption }}</p>
    <div class="oa-heatmap-grid">
      <template v-for="day in days" :key="day.weekday">
        <span class="oa-heatmap-day">{{ dayName(day.weekday) }}</span>
        <span
          v-for="slot in day.hours"
          :key="slot.hour"
          class="oa-heatmap-cell"
          :class="{ filled: !!slot.heat, active: hovered?.weekday === day.weekday && hovered?.hour === slot.hour }"
          :style="slot.heat ? { '--oa-heat': slot.heat } : undefined"
          @pointerenter="hovered = { weekday: day.weekday, hour: slot.hour }"
        />
      </template>
      <span class="oa-heatmap-day" />
      <span v-for="hour in HOURS" :key="`h${hour}`" class="oa-heatmap-hour">{{ hour % 3 === 0 ? hour : '' }}</span>
    </div>
    <div class="oa-heatmap-legend" aria-hidden="true">
      <span>{{ t('heatmapLess') }}</span>
      <i v-for="level in [0, 0.25, 0.5, 0.75, 1]" :key="level" :class="{ filled: level > 0 }" :style="level > 0 ? { '--oa-heat': heatOf(level) } : undefined" />
      <span>{{ t('heatmapMore') }}</span>
    </div>
  </div>
</template>
