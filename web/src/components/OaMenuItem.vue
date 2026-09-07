<script setup lang="ts">
// A row inside a menu: a title, an optional second line, an optional icon.
//
// The leading/trailing slots put the title inside a row rather than beside
// it, which is the only structural difference between the two shapes — so
// whether that row exists is decided by whether anything was passed for it.

import { useSlots } from 'vue';

const props = defineProps<{
  title: string;
  sub?: string | undefined;
  active?: boolean;
  disabled?: boolean;
}>();

const slots = useSlots();
const inRow = () => !!slots['leading'] || !!slots['trailing'];
</script>

<template>
  <button
    type="button"
    class="oa-menu-item"
    :class="{ active: props.active }"
    :disabled="props.disabled"
  >
    <div v-if="inRow()" class="oa-menu-row">
      <slot name="leading" />
      <span class="oa-menu-item-title">{{ props.title }}</span>
      <slot name="trailing" />
    </div>
    <span v-else class="oa-menu-item-title">{{ props.title }}</span>
    <span v-if="props.sub" class="oa-menu-item-sub">{{ props.sub }}</span>
  </button>
</template>
