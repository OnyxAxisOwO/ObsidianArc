<script setup lang="ts">
import { ref } from 'vue';
import OaField from './OaField.vue';

const props = defineProps<{
  modelValue: string;
  label: string;
  hint?: string | undefined;
  placeholder?: string | undefined;
  type?: string;
  maxLength?: number | undefined;
  autocomplete?: string | undefined;
  /** For the fields that hold an id, a URL or a key rather than prose. */
  monospace?: boolean;
  required?: boolean;
}>();

defineEmits<{ (event: 'update:modelValue', value: string): void }>();

const input = ref<HTMLInputElement | null>(null);

/**
 * Puts the caret here. `preventScroll` for a control in a panel that has not
 * slid in yet: the browser would otherwise scroll the whole row sideways to
 * reveal a field that is about to arrive on its own.
 */
defineExpose({ focus: (options?: FocusOptions) => input.value?.focus(options) });
</script>

<template>
  <OaField :label="props.label" :hint="props.hint">
    <input
      ref="input"
      :type="props.type ?? 'text'"
      spellcheck="false"
      :value="props.modelValue"
      :placeholder="props.placeholder"
      :maxlength="props.maxLength"
      :autocomplete="props.autocomplete"
      :required="props.required"
      :style="props.monospace ? { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' } : undefined"
      @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    >
  </OaField>
</template>
