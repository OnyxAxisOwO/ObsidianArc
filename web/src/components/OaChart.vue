<script setup lang="ts">
// Bar and pie charts, drawn as SVG.
//
// Both take the same data, so a toggle between them is a re-render rather
// than a different code path — which is the whole point of offering the
// choice. Neither animates on data change: these are read, not watched.
//
// The legend carries the numbers. Both shapes need it: a bar chart without
// labels is a set of anonymous rectangles, and a pie chart's own labels never
// fit. Keeping it identical for both is also what makes the toggle feel like
// one chart in two shapes.

import { computed, ref } from 'vue';
import { onPaletteChange } from '@/composables/useTheme';
import {
  BAR_GAP, BAR_ROW_HEIGHT, BAR_WIDTH, PIE_RADIUS, PIE_SIZE,
  bars, fold, palette, wedges, type ChartShape, type Slice,
} from '@/lib/chart';

const props = withDefaults(defineProps<{
  shape: ChartShape;
  data: readonly Slice[];
  /** Renders each value for the axis and the legend. */
  format: (value: number) => string;
  emptyText: string;
  /** Slices past this are folded into one "other" entry. */
  max?: number;
  otherLabel?: string;
  selectable?: boolean;
}>(), { max: 8, otherLabel: 'Other', selectable: false });

const emit = defineEmits<{ (event: 'select', key: string): void }>();

// The accent decides the ramp, so a theme change is a repaint of every slice.
const paletteVersion = ref(0);
onPaletteChange(() => { paletteVersion.value += 1; });

const slices = computed(() => fold(props.data, props.max, props.otherLabel));
const empty = computed(() => !slices.value.length || slices.value.every((slice) => slice.value <= 0));
const colours = computed(() => {
  // palette() reads the accent from computed CSS, which Vue cannot watch, so
  // the version is read here only to make a theme change recompute the ramp.
  void paletteVersion.value;
  return palette(slices.value.length);
});

const total = computed(() => slices.value.reduce((sum, slice) => sum + slice.value, 0));
const barRows = computed(() => bars(slices.value, colours.value));
const pieWedges = computed(() => wedges(slices.value, colours.value));
const barHeight = computed(() => slices.value.length * (BAR_ROW_HEIGHT + BAR_GAP));

function share(value: number): number {
  return total.value > 0 ? Math.round((value / total.value) * 100) : 0;
}

function hit(key: string): boolean {
  return props.selectable && !!key;
}

// A bar, a wedge and a legend row all select the same way, so a click on any
// of them is one rule rather than three copies that could drift apart.
function select(slice: Slice): void {
  if (hit(slice.key)) emit('select', slice.key);
}

// The folded "other" entry has no key of its own, so its label stands in.
function sliceKey(slice: Slice): string {
  return slice.key || slice.label;
}
</script>

<template>
  <div class="oa-chart">
    <p v-if="empty" class="oa-menu-empty">{{ props.emptyText }}</p>
    <template v-else>
      <svg
        v-if="props.shape === 'bar'"
        class="oa-chart-svg"
        role="img"
        :viewBox="`0 0 ${BAR_WIDTH} ${barHeight}`"
      >
        <rect
          v-for="bar in barRows"
          :key="sliceKey(bar.slice)"
          x="0"
          :y="bar.y"
          :width="bar.length"
          :height="BAR_ROW_HEIGHT"
          rx="6"
          :fill="bar.colour"
          :class="{ 'oa-chart-hit': hit(bar.slice.key) }"
          @click="select(bar.slice)"
        >
          <title>{{ bar.slice.label }} — {{ props.format(bar.slice.value) }}</title>
        </rect>
      </svg>

      <svg v-else class="oa-chart-svg" role="img" :viewBox="`0 0 ${PIE_SIZE} ${PIE_SIZE}`">
        <!-- One slice is the whole circle, which an arc path cannot express. -->
        <circle
          v-if="slices.length === 1"
          :cx="PIE_SIZE / 2"
          :cy="PIE_SIZE / 2"
          :r="PIE_RADIUS"
          :fill="colours[0]"
        />
        <template v-else>
          <path
            v-for="wedge in pieWedges"
            :key="sliceKey(wedge.slice)"
            :d="wedge.path"
            :fill="wedge.colour"
            :class="{ 'oa-chart-hit': hit(wedge.slice.key) }"
            @click="select(wedge.slice)"
          >
            <title>{{ wedge.slice.label }} — {{ props.format(wedge.slice.value) }} ({{ wedge.share }}%)</title>
          </path>
        </template>
      </svg>

      <ul class="oa-chart-legend">
        <li
          v-for="(slice, index) in slices"
          :key="sliceKey(slice)"
          class="oa-chart-legend-row"
        >
          <span class="oa-chart-swatch" :style="{ background: colours[index] }" />
          <span
            class="oa-chart-legend-label"
            :class="{ 'oa-chart-hit': hit(slice.key) }"
            @click="select(slice)"
          >{{ slice.label }}</span>
          <span class="oa-chart-legend-value">{{ props.format(slice.value) }}</span>
          <span class="oa-chart-legend-share">{{ share(slice.value) }}%</span>
        </li>
      </ul>
    </template>
  </div>
</template>
