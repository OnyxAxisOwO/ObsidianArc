<script setup lang="ts">
// Which way a figure moved against the same span before it.
//
// No green for up and red for down: more requests is not good news or bad
// news by itself, and no token in this interface carries a hue of its own.
// The danger colour is kept for the figures where up really is worse —
// failures and waiting — which is what `inverse` says.

import { computed } from 'vue';
import { t } from '@/composables/useI18n';
import { change, percent } from './scale';

const props = withDefaults(defineProps<{
  current: number;
  previous: number | null | undefined;
  inverse?: boolean;
}>(), { inverse: false });

const state = computed(() => {
  if (props.previous === null || props.previous === undefined) return null;
  if (!(props.previous > 0)) return props.current > 0 ? { kind: 'new' as const } : null;
  const ratio = change(props.current, props.previous) ?? 0;
  if (Math.abs(ratio) < 0.0005) return { kind: 'flat' as const, ratio };
  return { kind: ratio > 0 ? 'up' as const : 'down' as const, ratio };
});
</script>

<template>
  <span
    v-if="state"
    class="oa-delta"
    :class="[state.kind, { worse: props.inverse && state.kind === 'up' }]"
    :title="t('deltaTitle')"
  >
    <template v-if="state.kind === 'new'">{{ t('deltaNew') }}</template>
    <template v-else-if="state.kind === 'flat'">{{ t('deltaFlat') }}</template>
    <template v-else>
      <span aria-hidden="true">{{ state.kind === 'up' ? '↑' : '↓' }}</span>
      <span class="oa-visually-hidden">{{ t(state.kind === 'up' ? 'deltaUp' : 'deltaDown') }}</span>{{ percent(Math.abs(state.ratio)) }}
    </template>
  </span>
</template>
