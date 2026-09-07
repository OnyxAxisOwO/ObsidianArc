<script setup lang="ts">
// A sheet over the page.
//
// For the two things here that are genuinely modal: a refusal the reader has
// to acknowledge, and an announcement shown unprompted. Everything else in
// this interface is a column rather than a sheet, on purpose.
//
// It teleports to <body> so no ancestor's transform or backdrop-filter can
// trap it, and it fades itself in and out — `close` is emitted after the
// fade, so the caller's `v-if` does not cut the animation off.

import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useEventListener } from '@vueuse/core';

const props = withDefaults(defineProps<{
  overlayClass: string;
  /** False while a countdown is still running on the dismiss button. */
  dismissible?: boolean;
}>(), { dismissible: true });

const emit = defineEmits<{ (event: 'close'): void }>();

const shown = ref(false);
let closing = false;

onMounted(() => requestAnimationFrame(() => { shown.value = true; }));

function close(): void {
  if (closing || !props.dismissible) return;
  closing = true;
  shown.value = false;
  window.setTimeout(() => emit('close'), 200);
}

useEventListener(document, 'keydown', (event: KeyboardEvent) => {
  if (event.key === 'Escape') close();
});

onBeforeUnmount(() => { closing = true; });

defineExpose({ close });
</script>

<template>
  <Teleport to="body">
    <div
      :class="[props.overlayClass, { open: shown }]"
      @click="($event.target === $event.currentTarget) && close()"
    >
      <slot :close="close" />
    </div>
  </Teleport>
</template>
