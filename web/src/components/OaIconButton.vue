<script setup lang="ts">
// An icon-only control.
//
// The only thing this adds over a plain <button> is the part that is easy to
// forget: an icon has no text, so the name has to be somewhere a screen
// reader and a hovering pointer can both find it. Passing `label` puts it in
// both places at once, which is why every icon control in this interface goes
// through here rather than writing `title` and `aria-label` by hand.

import { ref } from 'vue';

const props = defineProps<{
  label: string;
  disabled?: boolean;
}>();

const button = ref<HTMLButtonElement | null>(null);

defineExpose({
  focus(): void {
    button.value?.focus();
  },
});
</script>

<template>
  <button ref="button" type="button" :title="props.label" :aria-label="props.label" :disabled="props.disabled">
    <slot />
  </button>
</template>
