<script setup lang="ts">
// One reward: nothing, credits in a bonus bar, or reset cards.
//
// Used for the reward every check-in gives and for each milestone's. It edits
// a copy and emits a whole new reward, so a reward that changes kind starts
// from that kind's own defaults rather than carrying the other's fields.

import { computed } from 'vue';
import type { BonusBarRow, CheckinRewardBody } from '@/admin/api';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';

const props = defineProps<{
  modelValue: CheckinRewardBody;
  bars: readonly BonusBarRow[];
  label: string;
  /** A milestone has to give something; the daily reward may give nothing. */
  allowNone?: boolean;
}>();

const emit = defineEmits<{ 'update:modelValue': [value: CheckinRewardBody] }>();

const kinds = computed(() => [
  ...(props.allowNone ? [{ value: '', label: t('checkinRewardNone') }] : []),
  { value: 'bonus', label: t('checkinRewardBonusKind') },
  { value: 'card', label: t('checkinRewardCardKind') },
]);

const barChoices = computed(() => props.bars.filter((bar) => bar.active).map((bar) => ({ value: bar.id, label: bar.name })));

const scopeChoices = computed(() => [
  { value: 'full', label: t('cardResetFull') },
  { value: '5h', label: t('cardReset5H') },
  { value: '1w', label: t('cardReset1W') },
  { value: '1m', label: t('cardReset1M') },
]);

function setKind(kind: string): void {
  if (kind === 'bonus') {
    emit('update:modelValue', { kind: 'bonus', bar_id: barChoices.value[0]?.value ?? '', amount: 1, valid_days: 7 });
  } else if (kind === 'card') {
    emit('update:modelValue', { kind: 'card', name: '', windows: [], cards: 1, valid_days: 30 });
  } else {
    emit('update:modelValue', { kind: '' });
  }
}

function patch(change: Partial<CheckinRewardBody>): void {
  emit('update:modelValue', { ...props.modelValue, ...change });
}

const scope = computed(() => props.modelValue.windows?.[0] ?? 'full');
</script>

<template>
  <div class="oa-reward-editor">
    <OaSelectField
      :model-value="props.modelValue.kind"
      :label="props.label"
      :options="kinds"
      :searchable="false"
      @update:model-value="setKind"
    />
    <template v-if="props.modelValue.kind === 'bonus'">
      <OaSelectField
        :model-value="props.modelValue.bar_id ?? ''"
        :label="t('checkinRewardBar')"
        :options="barChoices"
        @update:model-value="patch({ bar_id: $event })"
      />
      <OaNumberField
        :model-value="props.modelValue.amount ?? null"
        :label="t('checkinRewardAmount')"
        :min="0"
        :step="0.1"
        @update:model-value="patch({ amount: $event ?? 0 })"
      />
      <OaNumberField
        :model-value="props.modelValue.valid_days ?? null"
        :label="t('checkinRewardValidDays')"
        :min="0"
        :max="3650"
        @update:model-value="patch({ valid_days: $event ?? 0 })"
      />
    </template>
    <template v-else-if="props.modelValue.kind === 'card'">
      <OaTextField
        :model-value="props.modelValue.name ?? ''"
        :label="t('checkinRewardCardName')"
        :max-length="64"
        @update:model-value="patch({ name: $event })"
      />
      <OaSelectField
        :model-value="scope"
        :label="t('checkinRewardCardScope')"
        :options="scopeChoices"
        :searchable="false"
        @update:model-value="patch({ windows: $event === 'full' ? [] : [$event] })"
      />
      <OaNumberField
        :model-value="props.modelValue.cards ?? null"
        :label="t('checkinRewardCardCount')"
        :min="1"
        :max="20"
        @update:model-value="patch({ cards: $event ?? 1 })"
      />
      <OaNumberField
        :model-value="props.modelValue.valid_days ?? null"
        :label="t('checkinRewardValidDays')"
        :min="1"
        :max="3650"
        @update:model-value="patch({ valid_days: $event ?? 30 })"
      />
    </template>
  </div>
</template>
