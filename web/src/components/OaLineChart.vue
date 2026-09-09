<script setup lang="ts">
// An inline SVG line chart showing a model's availability trend over time.
//
// Native SVG without external dependencies, matching the interface's design
// tokens. A chart library would add tens of kilobytes for one graph.

import { computed, ref } from 'vue';
import { t } from '@/composables/useI18n';

export interface ChartPoint {
  at: number;
  uptime: number | null;
  total: number;
}

const props = withDefaults(
  defineProps<{
    points?: readonly ChartPoint[];
    state?: 'up' | 'degraded' | 'down' | 'unknown';
  }>(),
  {
    points: () => [],
    state: 'unknown',
  },
);

const hovered = ref<number | null>(null);

const padL = 34;
const padR = 12;
const padT = 10;
const padB = 22;
const width = 340;
const height = 76;
const plotW = width - padL - padR;
const plotH = height - padT - padB;

const strokeColor = computed(() => {
  switch (props.state) {
    case 'up':
      return '#10b981';
    case 'degraded':
      return '#f59e0b';
    case 'down':
      return '#ef4444';
    default:
      return 'var(--ai-outline-variant)';
  }
});

const hasData = computed(() =>
  props.points.some((p) => p.total > 0 && p.uptime !== null),
);

// Map points to SVG coordinates, bridging empty buckets to the closest known
// value so quiet stretches do not visually register as outages.
const mapped = computed(() => {
  const pts = props.points;
  if (!pts.length) return [];

  // Find the last known rate or default to 1.0 if the model is operational.
  let fallback = props.state === 'up' ? 1.0 : props.state === 'down' ? 0.0 : 0.5;
  for (const p of pts) {
    if (p.uptime !== null) {
      fallback = p.uptime;
      break;
    }
  }

  let current = fallback;
  return pts.map((pt, i) => {
    const x = padL + (i / Math.max(pts.length - 1, 1)) * plotW;
    if (pt.uptime !== null) {
      current = Math.max(0, Math.min(1, pt.uptime));
    }
    const y = padT + (1.0 - current) * plotH;
    return { x, y, pt, hasTraffic: pt.total > 0 };
  });
});

const linePath = computed(() => {
  const pts = mapped.value;
  if (!pts.length) return '';
  return pts.map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ');
});

const areaPath = computed(() => {
  const pts = mapped.value;
  if (!pts.length) return '';
  const bottomY = padT + plotH;
  const first = pts[0]!;
  const last = pts[pts.length - 1]!;
  return `${linePath.value} L ${last.x.toFixed(1)} ${bottomY} L ${first.x.toFixed(1)} ${bottomY} Z`;
});

function formatTime(timestamp: number): string {
  const d = new Date(timestamp);
  const h = String(d.getHours()).padStart(2, '0');
  const m = String(d.getMinutes()).padStart(2, '0');
  return `${h}:${m}`;
}

function pointTooltip(p: ChartPoint): string {
  const timeStr = formatTime(p.at);
  if (p.total === 0 || p.uptime === null) {
    return `${timeStr} — ${t('uptimeNoData')}`;
  }
  const pct = (p.uptime * 100).toFixed(p.uptime >= 0.995 ? 0 : 1);
  return `${timeStr} — ${pct}% (${p.total} ${t('statRequests').toLowerCase()})`;
}

const activeInfo = computed(() => {
  if (hovered.value === null) return null;
  const m = mapped.value[hovered.value];
  if (!m) return null;
  return pointTooltip(m.pt);
});
</script>

<template>
  <div class="oa-linechart-container">
    <div class="oa-linechart-header">
      <span class="oa-linechart-title">{{ t('uptimeHistoryTitle') }}</span>
      <span v-if="activeInfo" class="oa-linechart-hover-info">{{ activeInfo }}</span>
    </div>

    <svg
      :viewBox="`0 0 ${width} ${height}`"
      class="oa-linechart-svg"
      role="img"
      :aria-label="t('uptimeHistoryTitle')"
      @mouseleave="hovered = null"
    >
      <defs>
        <linearGradient :id="`oa-linechart-grad-${props.state}`" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" :stop-color="strokeColor" stop-opacity="0.28" />
          <stop offset="100%" :stop-color="strokeColor" stop-opacity="0.0" />
        </linearGradient>
      </defs>

      <!-- Reference grid lines at 100% and 0% -->
      <line
        :x1="padL"
        :y1="padT"
        :x2="width - padR"
        :y2="padT"
        class="oa-linechart-grid"
      />
      <line
        :x1="padL"
        :y1="padT + plotH / 2"
        :x2="width - padR"
        :y2="padT + plotH / 2"
        class="oa-linechart-grid"
      />
      <line
        :x1="padL"
        :y1="padT + plotH"
        :x2="width - padR"
        :y2="padT + plotH"
        class="oa-linechart-grid"
      />

      <!-- Y axis labels -->
      <text :x="padL - 4" :y="padT + 3" text-anchor="end" class="oa-linechart-label">
        100%
      </text>
      <text :x="padL - 4" :y="padT + plotH + 3" text-anchor="end" class="oa-linechart-label">
        0%
      </text>

      <!-- Empty state when no samples exist in the window -->
      <text
        v-if="!hasData"
        :x="padL + plotW / 2"
        :y="padT + plotH / 2 + 3"
        text-anchor="middle"
        class="oa-linechart-empty-text"
      >
        {{ t('uptimeNoHistory') }}
      </text>

      <template v-else>
        <!-- Area gradient under the line -->
        <path :d="areaPath" :fill="`url(#oa-linechart-grad-${props.state})`" />

        <!-- Main trend line -->
        <path
          :d="linePath"
          fill="none"
          :stroke="strokeColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        />

        <!-- Hover vertical cursor -->
        <line
          v-if="hovered !== null && mapped[hovered]"
          :x1="mapped[hovered]!.x"
          :y1="padT"
          :x2="mapped[hovered]!.x"
          :y2="padT + plotH"
          stroke="var(--ai-text-secondary)"
          stroke-width="1"
          stroke-dasharray="2,2"
        />

        <!-- Data point markers and hover targets -->
        <g
          v-for="(m, i) in mapped"
          :key="i"
          class="oa-linechart-point-group"
          @mouseenter="hovered = i"
        >
          <!-- Transparent wider hit target for easier touch/hover -->
          <circle
            :cx="m.x"
            :cy="m.y"
            r="8"
            fill="transparent"
            class="oa-linechart-target"
          />
          <circle
            v-if="m.hasTraffic || hovered === i"
            :cx="m.x"
            :cy="m.y"
            :r="hovered === i ? 3.5 : 2"
            :fill="strokeColor"
            class="oa-linechart-dot"
          />
        </g>
      </template>

      <!-- X axis time labels -->
      <text :x="padL" :y="height - 4" text-anchor="start" class="oa-linechart-label">
        -24h
      </text>
      <text :x="padL + plotW / 2" :y="height - 4" text-anchor="middle" class="oa-linechart-label">
        -12h
      </text>
      <text :x="width - padR" :y="height - 4" text-anchor="end" class="oa-linechart-label">
        Now
      </text>
    </svg>
  </div>
</template>
