<script setup lang="ts" generic="T extends string">
import OaField from './OaField.vue';
import OaSelect from './OaSelect.vue';
import type { Choice } from './choice';

const props = defineProps<{
  modelValue: T;
  label: string;
  options: ReadonlyArray<Choice<T>>;
  hint?: string | undefined;
}>();

defineEmits<{ (event: 'update:modelValue', value: T): void }>();
</script>

<template>
  <OaField :label="props.label" :hint="props.hint">
    <OaSelect
      :model-value="props.modelValue"
      :choices="props.options"
      @update:model-value="$emit('update:modelValue', $event)"
    />
  </OaField>
</template>
