<script setup lang="ts">
// Who uses what: the heaviest accounts down the side, the busiest models
// across the top, and in each cell what that account spent on that model.
//
// A table rather than a drawing, because every cell is a figure somebody may
// want to read exactly, and a table is what a screen reader can walk. The
// fill is only there so the eye finds the heavy cells before reading any.

import { computed } from 'vue';
import type { UsageBreakdown, UsageCell, UsageMatrix } from '@/admin/api';
import { isMasked, maskBilling, maskProvider, maskUser } from '@/admin/safeMode';
import { t } from '@/composables/useI18n';
import { intensity } from './scale';

type CellMetric = 'requests' | 'total_tokens' | 'credits';

const props = defineProps<{
  matrix: UsageMatrix;
  users: readonly UsageBreakdown[];
  models: readonly UsageBreakdown[];
  metric: CellMetric;
  format: (value: number) => string;
}>();

const emit = defineEmits<{
  (event: 'user', key: string): void;
  (event: 'model', key: string): void;
}>();

// Looked up by key for every cell and every header: a search through the rows
// each time made the table cost rows times columns times rows.
const cells = computed(() => {
  const out = new Map<string, UsageCell>();
  for (const cell of props.matrix.cells) out.set(`${cell.row}\u0000${cell.col}`, cell);
  return out;
});
const usersByKey = computed(() => new Map(props.users.map((row) => [row.key, row] as const)));
const modelsByKey = computed(() => new Map(props.models.map((row) => [row.key, row] as const)));
const peak = computed(() => Math.max(0, ...props.matrix.cells.map((cell) => cell[props.metric])));

function cell(user: string, model: string): number {
  return cells.value.get(`${user}\u0000${model}`)?.[props.metric] ?? 0;
}
function modelLabel(key: string): string {
  return modelsByKey.value.get(key)?.label || key;
}
function userLabel(key: string): string {
  return usersByKey.value.get(key)?.label || key;
}
// Safe mode, applied to everything this table prints, the tooltips too: a
// cell's title is read by hovering, and hovering is what a screen recording
// catches. The fill stays — it shows where the weight is, not what it is.
function userName(key: string): string {
  return maskUser(userLabel(key));
}
function userMark(key: string): string {
  return isMasked('users') ? '*' : Array.from(userLabel(key))[0]?.toLocaleUpperCase() ?? '·';
}
function modelDetail(key: string): string {
  return maskProvider(modelsByKey.value.get(key)?.detail ?? '');
}
/** A request count is not spending; tokens and credits are. */
function figure(value: number): string {
  const text = props.format(value);
  return props.metric === 'requests' ? text : maskBilling(text);
}

/** The row's total over every model, not only the ones shown across the top. */
function rowTotal(user: string): number {
  const row = usersByKey.value.get(user);
  if (!row) return 0;
  return props.metric === 'requests' ? row.requests : props.metric === 'total_tokens' ? row.total_tokens : row.credits;
}

// Each cell's figures are worked out once per render, here, rather than asked
// for again by the template for every attribute it sets.
const grid = computed(() => props.matrix.rows.map((user) => {
  const name = userName(user);
  return {
    key: user,
    name,
    mark: userMark(user),
    total: figure(rowTotal(user)),
    cells: props.matrix.cols.map((model) => {
      const value = cell(user, model);
      const amount = intensity(value, peak.value);
      return {
        key: model,
        heat: amount > 0 ? `${Math.round(12 + amount * 88)}%` : undefined,
        strong: amount > 0.6,
        title: `${name} · ${modelLabel(model)} · ${figure(value)}`,
        text: value ? figure(value) : '·',
      };
    }),
  };
}));
</script>

<template>
  <div class="oa-matrix-wrap" tabindex="0" :aria-label="t('matrixTitle')">
    <table class="oa-matrix">
      <thead>
        <tr>
          <th scope="col" class="oa-matrix-corner">{{ t('colUser') }}</th>
          <th v-for="model in props.matrix.cols" :key="model" scope="col">
            <button type="button" class="oa-matrix-head" :title="t('boardDrill', { name: modelLabel(model) })" @click="emit('model', model)">
              <span>{{ modelLabel(model) }}</span>
              <small>{{ modelDetail(model) }}</small>
            </button>
          </th>
          <th scope="col" class="oa-matrix-total">{{ t('matrixAllModels') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in grid" :key="row.key">
          <th scope="row">
            <button type="button" class="oa-matrix-user" :title="t('boardDrill', { name: row.name })" @click="emit('user', row.key)">
              <span class="oa-board-mark" aria-hidden="true">{{ row.mark }}</span>
              <span>{{ row.name }}</span>
            </button>
          </th>
          <td
            v-for="c in row.cells"
            :key="c.key"
            :class="{ filled: !!c.heat, strong: c.strong }"
            :style="c.heat ? { '--oa-heat': c.heat } : undefined"
            :title="c.title"
          >{{ c.text }}</td>
          <td class="oa-matrix-total">{{ row.total }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
