<script setup lang="ts">
// The dropdown the model chip, the bell and the account button all use.
//
// Only one menu is open at a time; opening a second closes the first, which
// is what stops two overlapping panels when a user clicks straight from one
// trigger to another.
//
// The contents are only in the document while it is open — the trigger slot
// gets `open` and `toggle`, the panel slot gets `close` — so a menu that has
// never been opened costs nothing and one that has been closed is not left
// holding a stale list.

import { onBeforeUnmount, ref } from 'vue';
import { onClickOutside, onKeyStroke } from '@vueuse/core';

const props = defineProps<{
  /** Extra class on the positioning wrapper, for alignment overrides. */
  groupClass?: string;
  menuClass?: string;
}>();

// Long enough for the transition in the stylesheet; a few ms over, so the
// last frame is not cut off.
const CLOSE_MS = 160;

const group = ref<HTMLElement | null>(null);

// Open is tracked separately from `mounted` because closing keeps the menu in
// the document until its transition has run, and it is shut as far as anyone
// clicking is concerned well before then.
const open = ref(false);
const mounted = ref(false);
const shown = ref(false);
let hideTimer = 0;

function show(): void {
  for (const other of others) if (other !== close) other();
  others.add(close);
  window.clearTimeout(hideTimer);
  open.value = true;
  mounted.value = true;
  // One frame closed, so the transition has a state to move from.
  requestAnimationFrame(() => {
    if (open.value) shown.value = true;
  });
}

function close(): void {
  if (!open.value) return;
  open.value = false;
  shown.value = false;
  others.delete(close);
  window.clearTimeout(hideTimer);
  hideTimer = window.setTimeout(() => {
    if (!open.value) mounted.value = false;
  }, CLOSE_MS);
}

function toggle(): void {
  if (open.value) close();
  else show();
}

onClickOutside(group, () => close());
onKeyStroke('Escape', () => close());

onBeforeUnmount(() => {
  window.clearTimeout(hideTimer);
  others.delete(close);
});

defineExpose({ isOpen: () => open.value, close, open: show });
</script>

<script lang="ts">
/** Every menu currently open. In practice never more than one. */
const others = new Set<() => void>();
</script>

<template>
  <div ref="group" class="oa-chip-group" :class="props.groupClass">
    <slot name="trigger" :open="open" :toggle="toggle" />
    <div v-if="mounted" class="oa-menu" :class="[props.menuClass, { open: shown }]">
      <slot :close="close" />
    </div>
  </div>
</template>
