<script setup lang="ts">
// A list of things each carrying a three-way access tier.
//
// The model is a record of only the granted rows: "none" is the absence of an
// entry rather than a value in it, which is what the server stores and what
// keeps a payload from carrying a row for every model on the instance.

import { t } from '@/composables/useI18n';
import type { AccessTier, ListItem } from './list-items';

const props = defineProps<{
  modelValue: Record<string, 'use' | 'view'>;
  label: string;
  items: readonly ListItem[];
  emptyText: string;
  hint?: string | undefined;
}>();

const emit = defineEmits<{
  (event: 'update:modelValue', value: Record<string, 'use' | 'view'>): void;
}>();

const TIERS: Array<{ tier: AccessTier; label: () => string }> = [
  { tier: 'none', label: () => t('tierNone') },
  { tier: 'view', label: () => t('tierView') },
  { tier: 'use', label: () => t('tierUse') },
];

function tierOf(value: string): AccessTier {
  return props.modelValue[value] ?? 'none';
}

function set(value: string, tier: AccessTier): void {
  const next = { ...props.modelValue };
  if (tier === 'none') delete next[value];
  else next[value] = tier;
  emit('update:modelValue', next);
}
</script>

<template>
  <div class="oa-field">
    <span class="oa-field-label">{{ props.label }}</span>
    <div class="oa-check-list">
      <p v-if="!props.items.length" class="oa-menu-empty">{{ props.emptyText }}</p>
      <div v-for="item in props.items" :key="item.value" class="oa-tier-row">
        <span class="oa-check-text">
          <span class="oa-check-title">{{ item.label }}</span>
          <span v-if="item.sub" class="oa-check-sub">{{ item.sub }}</span>
        </span>
        <div class="oa-segmented">
          <button
            v-for="entry in TIERS"
            :key="entry.tier"
            type="button"
            class="oa-segmented-option"
            :class="{ active: tierOf(item.value) === entry.tier }"
            @click="set(item.value, entry.tier)"
          >
            {{ entry.label() }}
          </button>
        </div>
      </div>
    </div>
    <span v-if="props.hint" class="oa-field-hint">{{ props.hint }}</span>
  </div>
</template>
