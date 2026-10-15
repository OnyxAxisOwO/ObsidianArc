<script setup lang="ts">
// Quick picks under an expiry field: a week, a month, a year from now.
//
// One component for the fields that had the same buttons written out by
// hand, each with its own copy of the date arithmetic and an inline style
// squeezing it to two pixels of padding — so the three sets were not quite
// the same size as each other or as anything else in the panel.

import { t, type StringKey } from '@/composables/useI18n';
import { addMonths } from './shared';

const props = withDefaults(defineProps<{
  /** Offer "no expiry" too, for a field where empty means for ever. */
  permanent?: boolean;
}>(), { permanent: false });

const emit = defineEmits<{ (event: 'pick', value: string): void }>();

type Span = '1w' | '1m' | '3m' | '6m' | '1y';
const PRESETS: Array<{ span: Span; label: StringKey }> = [
  { span: '1w', label: 'expiry1Week' },
  { span: '1m', label: 'expiry1Month' },
  { span: '3m', label: 'expiry3Months' },
  { span: '6m', label: 'expiryHalfYear' },
  { span: '1y', label: 'expiry1Year' },
];

const MONTHS: Record<Exclude<Span, '1w'>, number> = { '1m': 1, '3m': 3, '6m': 6, '1y': 12 };

/** That far from now, as the value a datetime-local field takes. */
function later(span: Span): string {
  const now = new Date();
  if (span === '1w') now.setDate(now.getDate() + 7);
  const date = span === '1w' ? now : addMonths(now, MONTHS[span]);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
}
</script>

<template>
  <div class="oa-chip-row">
    <button v-for="preset in PRESETS" :key="preset.span" type="button" class="oa-chip-btn" @click="emit('pick', later(preset.span))">
      {{ t(preset.label) }}
    </button>
    <button v-if="props.permanent" type="button" class="oa-chip-btn" @click="emit('pick', '')">{{ t('membershipPermanent') }}</button>
  </div>
</template>
