<script setup lang="ts">
import { ref } from 'vue';
import OaField from './OaField.vue';

const props = withDefaults(defineProps<{
  modelValue: string;
  label: string;
  hint?: string | undefined;
  placeholder?: string | undefined;
  rows?: number;
}>(), { rows: 3 });

defineEmits<{ (event: 'update:modelValue', value: string): void }>();

const area = ref<HTMLTextAreaElement | null>(null);
defineExpose({ focus: (options?: FocusOptions) => area.value?.focus(options) });
</script>

<template>
  <OaField :label="props.label" :hint="props.hint">
    <textarea
      ref="area"
      :rows="props.rows"
      spellcheck="false"
      :value="props.modelValue"
      :placeholder="props.placeholder"
      @input="$emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    />
  </OaField>
</template>
