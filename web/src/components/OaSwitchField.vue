<script setup lang="ts">
const props = defineProps<{
  modelValue: boolean;
  label: string;
  hint?: string | undefined;
}>();

defineEmits<{ (event: 'update:modelValue', value: boolean): void }>();
</script>

<template>
  <div class="oa-switch-field">
    <label class="oa-checkbox-field">
      <input
        type="checkbox"
        :checked="props.modelValue"
        @change="$emit('update:modelValue', ($event.target as HTMLInputElement).checked)"
      >
      <span>{{ props.label }}</span>
    </label>
    <span v-if="props.hint" class="oa-field-hint">{{ props.hint }}</span>
  </div>
</template>
