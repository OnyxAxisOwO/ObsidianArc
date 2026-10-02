<script setup lang="ts">
// A credits limit with a live readout of what it buys.
//
// Credits are a unit this instance invents: how much one is worth is the
// model weights, which are on another screen. A number with no idea attached
// is the reason this setting was unreadable, so the field carries the
// conversion instead of the operator having to do it.

import { computed, onMounted, ref } from 'vue';
import type { AdminModel } from '@/admin/api';
import OaNumberField from '@/components/OaNumberField.vue';
import { t } from '@/composables/useI18n';
import { priciestFew, pricedModels, round, worstCase } from './shared';

const props = defineProps<{ modelValue: number | null }>();
defineEmits<{ (event: 'update:modelValue', value: number | null): void }>();

const TOP = 5;

const top = ref<AdminModel[]>([]);

const rows = computed(() => {
  const limit = props.modelValue;
  if (limit === null || limit <= 0) return [];
  return top.value.map((entry) => {
    const cost = worstCase(entry);
    const args = { name: entry.display_name, cost: round(cost), turns: Math.floor(limit / cost) };
    return args.turns > 0 ? t('creditsRow', args) : t('creditsRowShort', args);
  });
});

onMounted(() => {
  void pricedModels().then((models) => { top.value = priciestFew(models, TOP); });
});
</script>

<template>
  <OaNumberField
    :model-value="props.modelValue"
    :label="t('limitCredits')"
    :placeholder="t('noLimit')"
    :min="0"
    :step="0.1"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <template #after>
      <div v-if="rows.length" class="oa-field-hint">
        <div>{{ t('creditsTop') }}</div>
        <div v-for="row in rows" :key="row">{{ row }}</div>
      </div>
    </template>
  </OaNumberField>
</template>
