<script setup lang="ts">
// A set of checkboxes — which models a key may use, and nothing else yet.

import type { ListItem } from './list-items';

const props = defineProps<{
  modelValue: string[];
  label: string;
  items: readonly ListItem[];
  emptyText: string;
  hint?: string | undefined;
}>();

const emit = defineEmits<{ (event: 'update:modelValue', value: string[]): void }>();

function toggle(value: string, checked: boolean): void {
  const next = props.modelValue.filter((entry) => entry !== value);
  if (checked) next.push(value);
  emit('update:modelValue', next);
}
</script>

<template>
  <div class="oa-field">
    <span class="oa-field-label">{{ props.label }}</span>
    <div class="oa-check-list">
      <p v-if="!props.items.length" class="oa-menu-empty">{{ props.emptyText }}</p>
      <label v-for="item in props.items" :key="item.value" class="oa-check-row">
        <input
          type="checkbox"
          :checked="props.modelValue.includes(item.value)"
          @change="toggle(item.value, ($event.target as HTMLInputElement).checked)"
        >
        <span class="oa-check-text">
          <span class="oa-check-title">{{ item.label }}</span>
          <span v-if="item.sub" class="oa-check-sub">{{ item.sub }}</span>
        </span>
      </label>
    </div>
    <span v-if="props.hint" class="oa-field-hint">{{ props.hint }}</span>
  </div>
</template>
