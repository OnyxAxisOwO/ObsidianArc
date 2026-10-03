<script setup lang="ts">
// A ranked list — models, accounts, groups — with each row's share of the
// whole drawn as a bar and the figures that explain the rank underneath.
//
// Every row is in the list; `limit` is how many show before it scrolls. It
// used to fold the rest behind a "show all" button, and opening it on a few
// dozen accounts stretched one card of a side-by-side pair to three times the
// height of the other. A list that scrolls inside a fixed frame keeps the
// pair level whatever the instance has grown to.
//
// Sorted here rather than by the server, because two boards on one screen ask
// for two different orders from the same breakdown: the popular models are
// the ones the most people use, the heavy accounts the ones that spend the
// most tokens. The server returns every row either way.

import { computed, watch } from 'vue';
import type { UsageBreakdown } from '@/admin/api';
import { t, tn } from '@/composables/useI18n';
import { compactNumber, relativeTime } from '@/lib/format';
import { formatDuration, percent } from './scale';
import { useScrollFade } from './useScrollFade';
import { metricOf, type BoardKind, type BoardMetric } from './shape';
import { maskUser, maskProvider, maskBilling, isMasked } from '@/admin/safeMode';
import OaScrollArea from '@/components/OaScrollArea.vue';

const props = withDefaults(defineProps<{
  rows: readonly UsageBreakdown[];
  kind: BoardKind;
  metric: BoardMetric;
  /** How many rows show before the list scrolls. */
  limit?: number;
  selectable?: boolean;
  /** An account's most-used model, where the caller knows it. */
  favourites?: Readonly<Record<string, string>>;
  /**
   * Whether to say how many people used a model, or how many models an
   * account used. Off where the board is already about one of either — every
   * row of one account's models would say "1 person".
   */
  reach?: boolean;
  emptyText: string;
}>(), { limit: 6, selectable: true, favourites: () => ({}), reach: true });

const emit = defineEmits<{ (event: 'select', key: string): void }>();

const ranked = computed(() => [...props.rows]
  .filter((row) => row.key !== '' || row.requests > 0)
  .sort((a, b) => metricOf(b, props.metric) - metricOf(a, props.metric) || b.requests - a.requests));
const total = computed(() => props.metric === 'users'
  // People overlap between rows, so a share of "all users" is a share of the
  // widest row's audience rather than of a sum that counts some twice.
  ? Math.max(0, ...ranked.value.map((row) => row.users ?? 0))
  : ranked.value.reduce((sum, row) => sum + metricOf(row, props.metric), 0));
const overflows = computed(() => ranked.value.length > props.limit);

const { onScroll, reset, scrollClass } = useScrollFade(overflows);
watch(() => ranked.value.length, reset);

function value(row: UsageBreakdown): string {
  const figure = metricOf(row, props.metric);
  if (props.metric === 'credits' || props.metric === 'tokens') {
    return maskBilling(compactNumber(figure));
  }
  return props.metric === 'users' ? tn(figure, 'boardUsersOne', 'boardUsersOther', { count: figure }) : compactNumber(figure);
}
function share(row: UsageBreakdown): number {
  return total.value > 0 ? metricOf(row, props.metric) / total.value : 0;
}
function name(row: UsageBreakdown): string {
  const raw = row.label || row.key || '—';
  if (props.kind === 'user') return maskUser(raw);
  if (props.kind === 'provider') return maskProvider(raw);
  return raw;
}
function initial(row: UsageBreakdown): string {
  if (props.kind === 'user' && isMasked('users')) return '*';
  return Array.from(name(row))[0]?.toLocaleUpperCase() ?? '·';
}

/** The line under the name: what the rank is made of, in words. */
function notes(row: UsageBreakdown): string[] {
  const out: string[] = [];
  if (props.kind === 'user') {
    if (row.detail && row.detail !== row.label) out.push(`@${maskUser(row.detail)}`);
    if (props.favourites[row.key]) out.push(t('boardFavourite', { model: props.favourites[row.key]! }));
    else if (props.reach && row.models) out.push(tn(row.models, 'boardModelsOne', 'boardModelsOther', { count: row.models }));
  } else {
    // A model's detail is the provider it runs on, so it is masked as one.
    if (row.detail) out.push(props.kind === 'provider' || props.kind === 'model' ? maskProvider(row.detail) : row.detail);
    if (props.reach && props.metric !== 'users' && row.users) out.push(tn(row.users, 'boardUsersOne', 'boardUsersOther', { count: row.users }));
  }
  if (props.metric !== 'requests') out.push(t('boardRequests', { count: compactNumber(row.requests) }));
  if (props.kind === 'model' && row.requests) {
    out.push(t('boardSuccess', { rate: percent((row.requests - row.errors) / row.requests) }));
    if (row.duration_ms) out.push(formatDuration(row.duration_ms / row.requests));
  }
  if (props.kind === 'user' && row.last_at) out.push(relativeTime(row.last_at));
  return out;
}
</script>

<template>
  <p v-if="!ranked.length" class="oa-board-empty">{{ props.emptyText }}</p>
  <div v-else class="oa-board-frame" :style="{ '--board-rows': props.limit }">
    <OaScrollArea wrap-class="oa-board-wrap" :scroll-class="scrollClass" @scroll="onScroll">
      <ol class="oa-board" :class="`oa-board-${props.kind}`">
        <li v-for="(row, index) in ranked" :key="row.key || `row-${index}`">
          <component
            :is="props.selectable && row.key ? 'button' : 'div'"
            :type="props.selectable && row.key ? 'button' : undefined"
            class="oa-board-row"
            :title="props.selectable && row.key ? t('boardDrill', { name: name(row) }) : undefined"
            @click="props.selectable && row.key && emit('select', row.key)"
          >
            <span class="oa-board-rank">{{ String(index + 1).padStart(2, '0') }}</span>
            <span class="oa-board-mark" aria-hidden="true">{{ initial(row) }}</span>
            <span class="oa-board-main">
              <span class="oa-board-line">
                <span class="oa-board-name">{{ name(row) }}</span>
                <strong class="oa-board-value">{{ value(row) }}</strong>
              </span>
              <span class="oa-board-line oa-board-sub">
                <span class="oa-board-notes">{{ notes(row).join(' · ') }}</span>
                <small>{{ percent(share(row)) }}</small>
              </span>
              <span class="oa-board-track" aria-hidden="true"><span :style="{ width: `${Math.max(share(row) * 100, share(row) > 0 ? 1.5 : 0)}%` }" /></span>
            </span>
          </component>
        </li>
      </ol>
    </OaScrollArea>
    <p v-if="overflows" class="oa-board-count">{{ t('boardCount', { count: ranked.length }) }}</p>
  </div>
</template>
