<script setup lang="ts">
// A slider with its value beside the label.
//
// For the settings that are a quantity rather than a figure: nobody knows
// what "35% translucent" looks like, so the useful control is the one you can
// push until the screen looks right. `update:modelValue` fires all the way
// through the drag so the change can be shown live; `commit` fires once, on
// release, for whatever should not run per pixel — persisting it, usually.

const props = withDefaults(defineProps<{
  modelValue: number;
  label: string;
  min: number;
  max: number;
  step?: number;
  hint?: string | undefined;
  format?: ((value: number) => string) | undefined;
}>(), { step: 1 });

const emit = defineEmits<{
  (event: 'update:modelValue', value: number): void;
  (event: 'commit', value: number): void;
}>();
</script>

<template>
  <div class="oa-field oa-range-field">
    <div class="oa-range-head">
      <span class="oa-field-label">{{ props.label }}</span>
      <span class="oa-range-value">
        {{ props.format ? props.format(props.modelValue) : String(props.modelValue) }}
      </span>
    </div>
    <!-- change, not pointerup: it also covers the keyboard, and a slider that
         only saved when a mouse let go of it would quietly lose an arrow key. -->
    <input
      type="range"
      :min="props.min"
      :max="props.max"
      :step="props.step"
      :value="props.modelValue"
      @input="emit('update:modelValue', Number(($event.target as HTMLInputElement).value))"
      @change="emit('commit', Number(($event.target as HTMLInputElement).value))"
    >
    <span v-if="props.hint" class="oa-field-hint">{{ props.hint }}</span>
  </div>
</template>
