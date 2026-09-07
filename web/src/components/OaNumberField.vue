<script setup lang="ts">
// A number that may legitimately be absent.
//
// Empty reads as null, which is how "inherit" and "no limit" are expressed —
// a quota field that fell back to 0 would mean "allow nothing".

import { ref } from 'vue';
import OaField from './OaField.vue';

const props = defineProps<{
  modelValue: number | null;
  label: string;
  hint?: string | undefined;
  placeholder?: string | undefined;
  min?: number | undefined;
  max?: number | undefined;
  step?: number | undefined;
}>();

const emit = defineEmits<{ (event: 'update:modelValue', value: number | null): void }>();

const input = ref<HTMLInputElement | null>(null);

function onInput(event: Event): void {
  const raw = (event.target as HTMLInputElement).value.trim();
  if (raw === '') {
    emit('update:modelValue', null);
    return;
  }
  const parsed = Number(raw);
  emit('update:modelValue', Number.isFinite(parsed) ? parsed : null);
}

defineExpose({ focus: (options?: FocusOptions) => input.value?.focus(options) });
</script>

<template>
  <OaField :label="props.label" :hint="props.hint">
    <input
      ref="input"
      type="number"
      :value="props.modelValue === null ? '' : String(props.modelValue)"
      :placeholder="props.placeholder"
      :min="props.min"
      :max="props.max"
      :step="props.step"
      @input="onInput"
    >
    <template #after><slot name="after" /></template>
  </OaField>
</template>
