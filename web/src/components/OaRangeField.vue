<script setup lang="ts">
// A slider with its value beside the label.
//
// For the settings that are a quantity rather than a figure: nobody knows
// what "35% translucent" looks like, so the useful control is the one you can
// push until the screen looks right. `update:modelValue` fires all the way
// through the drag so the change can be shown live; `commit` fires once, on
// release, for whatever should not run per pixel — persisting it, usually.

import { computed, useId } from 'vue';

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

const id = useId();
const progress = computed(() => {
  const span = props.max - props.min;
  return span > 0 ? Math.max(0, Math.min(100, (props.modelValue - props.min) / span * 100)) : 0;
});
</script>

<template>
  <div class="oa-field oa-range-field">
    <div class="oa-range-head">
      <label :for="id" class="oa-field-label">{{ props.label }}</label>
      <span class="oa-range-value">
        {{ props.format ? props.format(props.modelValue) : String(props.modelValue) }}
      </span>
    </div>
    <!-- change, not pointerup: it also covers the keyboard, and a slider that
         only saved when a mouse let go of it would quietly lose an arrow key. -->
    <input
      :id="id"
      type="range"
      :style="{ '--oa-range-progress': `${progress}%` }"
      :aria-valuetext="props.format?.(props.modelValue)"
      :aria-describedby="props.hint ? `${id}-hint` : undefined"
      :min="props.min"
      :max="props.max"
      :step="props.step"
      :value="props.modelValue"
      @input="emit('update:modelValue', Number(($event.target as HTMLInputElement).value))"
      @change="emit('commit', Number(($event.target as HTMLInputElement).value))"
    >
    <span v-if="props.hint" :id="`${id}-hint`" class="oa-field-hint">{{ props.hint }}</span>
  </div>
</template>
