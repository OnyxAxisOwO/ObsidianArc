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
import { priciest, pricedModels, round, worstCase } from './shared';

const props = defineProps<{ modelValue: number | null }>();
defineEmits<{ (event: 'update:modelValue', value: number | null): void }>();

const worst = ref<AdminModel | null>(null);

const note = computed(() => {
  const model = worst.value;
  const limit = props.modelValue;
  if (!model || limit === null || limit <= 0) return '';
  const cost = worstCase(model);
  return limit < cost
    ? t('creditsTooSmall', { name: model.display_name, cost: round(cost) })
    : t('creditsBuys', { turns: Math.floor(limit / cost), name: model.display_name, cost: round(cost) });
});

onMounted(() => {
  void pricedModels().then((models) => { worst.value = priciest(models); });
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
      <span class="oa-field-hint">{{ note }}</span>
    </template>
  </OaNumberField>
</template>
