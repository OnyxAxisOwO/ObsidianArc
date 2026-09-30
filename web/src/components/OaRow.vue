<script setup lang="ts">
// One row of an OaGroup.
//
// Two shapes. Beside: what it is on the left (a title, a line under it, a mark
// before it) and the control on the right — a button, a switch, a value — the
// way the connections and the devices are listed. Stacked: for a field, where
// the control wants the width, the row is padding and the field brings its own
// label and explanation.

import type { Component } from 'vue';

defineProps<{
  title?: string | undefined;
  meta?: string | undefined;
  /** A mark before the text, for a row that is one of a kind of thing. */
  icon?: Component | undefined;
  /** The default slot fills the row under the text instead of sitting beside it. */
  stacked?: boolean;
}>();
</script>

<template>
  <div class="oa-group-row" :class="{ stacked }">
    <span v-if="icon" class="oa-group-row-mark"><component :is="icon" :size="16" /></span>
    <div v-if="title || meta || $slots['text']" class="oa-group-row-text">
      <span v-if="title" class="oa-group-row-title">{{ title }}</span>
      <span v-if="meta" class="oa-group-row-meta">{{ meta }}</span>
      <slot name="text" />
    </div>
    <div v-if="$slots['default']" class="oa-group-row-control"><slot /></div>
  </div>
</template>
