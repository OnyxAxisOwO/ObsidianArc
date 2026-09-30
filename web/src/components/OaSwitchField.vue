<script setup lang="ts">
const props = defineProps<{
  modelValue: boolean;
  label: string;
  hint?: string | undefined;
  disabled?: boolean;
}>();

const emit = defineEmits<{ (event: 'update:modelValue', value: boolean): void }>();

function onChange(event: Event): void {
  const target = event.target as HTMLInputElement;
  const next = target.checked;
  target.checked = props.modelValue;
  emit('update:modelValue', next);
}
</script>

<template>
  <div class="oa-switch-field">
    <label class="oa-checkbox-field">
      <input
        type="checkbox"
        :checked="props.modelValue"
        :disabled="props.disabled"
        @change="onChange"
      >
      <span>{{ props.label }}</span>
    </label>
    <span v-if="props.hint" class="oa-field-hint">{{ props.hint }}</span>
  </div>
</template>
