<script setup lang="ts">
// The rows that say how many amounts of thinking a model offers.
//
// Not the tier list from the components folder, which grants a group access
// to a model: the two words only collide in English.
//
// An empty list is the built-in three, so deleting the last row is how an
// administrator goes back to them rather than a state to be guarded against.

import { ref, watch } from 'vue';
import type { ReasoningTier } from '@/admin/api';
import OaIconButton from '@/components/OaIconButton.vue';
import { t } from '@/composables/useI18n';
import { IconClose } from '@/icons';

interface Row {
  id: string;
  name: string;
  budget: string;
}

const props = defineProps<{ modelValue: ReasoningTier[] }>();
const emit = defineEmits<{ (event: 'update:modelValue', value: ReasoningTier[]): void }>();

const rows = ref<Row[]>(props.modelValue.map((tier) => ({
  id: tier.id,
  name: tier.name,
  budget: tier.budget ? String(tier.budget) : '',
})));

// Half-filled rows are dropped here as well as on the server: a tier with no
// name would be a blank stop on the slider, and one with no value could never
// be told apart from its neighbour.
watch(rows, (next) => {
  emit('update:modelValue', next
    .map((row) => ({
      id: row.id.trim(),
      name: row.name.trim(),
      budget: Math.max(0, Math.trunc(Number(row.budget.trim()) || 0)),
    }))
    .filter((tier) => tier.id !== '' && tier.name !== ''));
}, { deep: true });

function add(): void {
  rows.value = [...rows.value, { id: '', name: '', budget: '' }];
}

function remove(index: number): void {
  rows.value = rows.value.filter((_, at) => at !== index);
}
</script>

<template>
  <div class="oa-field">
    <span class="oa-field-label">{{ t('reasoningTiers') }}</span>
    <div class="oa-thinking-tiers">
      <div v-for="(row, index) in rows" :key="index" class="oa-thinking-tier">
        <!-- The column headers are the placeholders, which disappear the
             moment a row is filled in, so the name has to survive somewhere a
             screen reader can still reach. -->
        <input
          v-model="row.name"
          class="oa-thinking-name"
          type="text"
          spellcheck="false"
          :placeholder="t('tierName')"
          :aria-label="t('tierName')"
        >
        <input
          v-model="row.id"
          class="oa-thinking-value"
          type="text"
          spellcheck="false"
          :placeholder="t('tierValue')"
          :aria-label="t('tierValue')"
        >
        <input
          v-model="row.budget"
          class="oa-thinking-budget"
          type="text"
          inputmode="numeric"
          spellcheck="false"
          :placeholder="t('tierBudget')"
          :aria-label="t('tierBudget')"
        >
        <OaIconButton class="oa-icon-btn" :label="t('removeTier')" @click="remove(index)">
          <IconClose :size="14" />
        </OaIconButton>
      </div>
    </div>
    <button type="button" class="oa-btn" @click="add">{{ t('addTier') }}</button>
    <span class="oa-field-hint">{{ t('reasoningTiersHint') }}</span>
  </div>
</template>
