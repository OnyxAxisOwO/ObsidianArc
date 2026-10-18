<script setup lang="ts">
// A time series drawn as a filled curve over a labelled grid.
//
// The line and the fill are SVG, stretched to the box; everything that must
// keep its shape — the dot on the hovered point, the value bubble, the axis
// figures — is HTML laid over it by percentage. The stretched SVG used to draw
// the dot too, and a circle in a box scaled differently on each axis is an
// ellipse.

import { computed, ref, useId, watch } from 'vue';
import { currentLanguage } from '@/composables/useI18n';
import { niceScale, smoothPath, tickFormat, timeTicks } from './scale';
import type { PlotPoint } from './shape';

const props = withDefaults(defineProps<{
  points: readonly PlotPoint[];
  bucketMs: number;
  /**
   * The offset the series was cut on: what the server's buckets are aligned
   * to, and so what the axis and the bubble must read the clock by.
   */
  offsetMs: number;
  format: (value: number) => string;
  /** Read out for the whole chart, and prefixed to each point for keyboard readers. */
  label: string;
  height?: number;
  /** The hovered or focused bucket, owned by whoever shows its value. */
  active?: number | null;
}>(), { height: 180, active: null });

const emit = defineEmits<{ (event: 'update:active', index: number | null): void }>();

const WIDTH = 720;
const gradient = `oa-plot-fill-${useId()}`;
const focused = ref(false);
const locale = computed(() => currentLanguage() === 'zh' ? 'zh-CN' : 'en-US');

const scale = computed(() => niceScale(Math.max(0, ...props.points.map((point) => point.value))));
const start = computed(() => props.points[0]?.at ?? 0);
const span = computed(() => (props.points.at(-1)?.at ?? 0) - start.value);

function xOf(index: number): number {
  const point = props.points[index];
  if (!point) return 0;
  return span.value > 0 ? (point.at - start.value) / span.value : 0.5;
}
function yOf(index: number): number {
  const point = props.points[index];
  return point ? 1 - point.value / scale.value.max : 1;
}

const coords = computed(() => props.points.map((_, index) => [xOf(index) * WIDTH, yOf(index) * props.height] as const));
const line = computed(() => smoothPath(coords.value));
const area = computed(() => coords.value.length > 1
  ? `${line.value} L ${WIDTH} ${props.height} L 0 ${props.height} Z`
  : '');

/**
 * Six labels at most, on round hours or days, formatted to the resolution of
 * the buckets. A label at either end of the plot is pinned inside it rather
 * than centred on the edge, where half of it would hang off the card.
 */
const axis = computed(() => {
  if (!props.points.length) return [];
  const ticks = timeTicks(props.points.map((point) => point.at), props.bucketMs, props.offsetMs);
  const format = tickFormat(ticks.step, span.value);
  return ticks.indices.map((index) => {
    const at = props.points[index]!.at;
    const left = xOf(index) * 100;
    // Formatted in UTC on the shifted instant: that prints the wall clock the
    // grid was cut on, whatever the browser's zone says for this instant.
    const text = new Date(at + props.offsetMs).toLocaleString(locale.value, { ...format, timeZone: 'UTC' });
    return { key: `${index}-${at}`, left, text, edge: left < 4 ? 'start' : left > 96 ? 'end' : '' };
  });
});

const selected = computed(() => props.active === null ? null : props.points[props.active] ?? null);

/** The bucket as a span of time: "9月16日 18:00 – 24:00", or a day. */
function describe(at: number): string {
  const wall = (instant: number, options: Intl.DateTimeFormatOptions): string =>
    new Date(instant + props.offsetMs).toLocaleString(locale.value, { ...options, timeZone: 'UTC' });
  if (props.bucketMs >= 86_400_000) {
    return wall(at, { month: 'short', day: 'numeric', weekday: 'short' });
  }
  const from = wall(at, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  const to = wall(at + props.bucketMs, { hour: '2-digit', minute: '2-digit' });
  return `${from} – ${to}`;
}

defineExpose({ describe });

function nearest(event: PointerEvent): void {
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect();
  const ratio = Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width));
  let best = 0;
  props.points.forEach((_, index) => {
    if (Math.abs(xOf(index) - ratio) < Math.abs(xOf(best) - ratio)) best = index;
  });
  emit('update:active', props.points.length ? best : null);
}

function step(event: KeyboardEvent): void {
  const last = props.points.length - 1;
  if (last < 0 || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const current = props.active ?? last;
  const next = event.key === 'Home' ? 0
    : event.key === 'End' ? last
      : Math.max(0, Math.min(last, current + (event.key === 'ArrowRight' ? 1 : -1)));
  emit('update:active', next);
}

// Watches the buckets, not the array: the page rebuilds the series on every
// refresh, and a readout the reader is looking at must not be cleared by one
// that brings the same buckets back. A different set of buckets is the case
// where an index into the old series points at nothing.
const buckets = computed(() => props.points.map((point) => point.at).join());
watch(buckets, () => { if (!focused.value) emit('update:active', null); });
</script>

<template>
  <div class="oa-plot" :style="{ '--oa-plot-height': `${props.height}px` }">
    <div class="oa-plot-y" aria-hidden="true">
      <span v-for="tick in scale.ticks" :key="tick" :style="{ top: `${(1 - tick / scale.max) * 100}%` }">{{ props.format(tick) }}</span>
    </div>
    <div
      class="oa-plot-area"
      tabindex="0"
      role="slider"
      :aria-label="props.label"
      :aria-valuemin="1"
      :aria-valuemax="props.points.length"
      :aria-valuenow="(props.active ?? props.points.length - 1) + 1"
      :aria-valuetext="selected ? `${describe(selected.at)}: ${props.format(selected.value)}` : undefined"
      @pointermove="nearest"
      @pointerleave="!focused && emit('update:active', null)"
      @focus="focused = true; emit('update:active', props.points.length - 1)"
      @blur="focused = false; emit('update:active', null)"
      @keydown="step"
    >
      <svg :viewBox="`0 0 ${WIDTH} ${props.height}`" preserveAspectRatio="none" aria-hidden="true">
        <defs>
          <linearGradient :id="gradient" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="var(--ai-primary)" stop-opacity=".22" />
            <stop offset="100%" stop-color="var(--ai-primary)" stop-opacity="0" />
          </linearGradient>
        </defs>
        <line
          v-for="tick in scale.ticks" :key="tick" class="oa-plot-grid"
          x1="0" :x2="WIDTH" :y1="(1 - tick / scale.max) * props.height" :y2="(1 - tick / scale.max) * props.height"
          vector-effect="non-scaling-stroke"
        />
        <path v-if="area" :d="area" :fill="`url(#${gradient})`" />
        <path class="oa-plot-line" :d="line" vector-effect="non-scaling-stroke" />
      </svg>
      <template v-if="selected && props.active !== null">
        <span class="oa-plot-crosshair" :style="{ left: `${xOf(props.active) * 100}%` }" />
        <span class="oa-plot-dot" :style="{ left: `${xOf(props.active) * 100}%`, top: `${yOf(props.active) * 100}%` }" />
        <span
          class="oa-plot-bubble"
          :class="{ flip: xOf(props.active) > 0.72 }"
          :style="{ left: `${xOf(props.active) * 100}%`, top: `${yOf(props.active) * 100}%` }"
          aria-hidden="true"
        ><strong>{{ props.format(selected.value) }}</strong><small>{{ describe(selected.at) }}</small></span>
      </template>
      <span v-else-if="props.points.length === 1" class="oa-plot-dot" :style="{ left: '50%', top: `${yOf(0) * 100}%` }" />
    </div>
    <div class="oa-plot-x" aria-hidden="true">
      <span v-for="tick in axis" :key="tick.key" :class="tick.edge" :style="{ left: `${tick.left}%` }">{{ tick.text }}</span>
    </div>
  </div>
</template>
