<script setup lang="ts">
// An icon-only control.
//
// The only thing this adds over a plain <button> is the part that is easy to
// forget: an icon has no text, so the name has to be somewhere a screen
// reader and a hovering pointer can both find it. Passing `label` puts it in
// both places at once, which is why every icon control in this interface goes
// through here rather than writing `title` and `aria-label` by hand.

const props = defineProps<{
  label: string;
  disabled?: boolean;
}>();
</script>

<template>
  <button type="button" :title="props.label" :aria-label="props.label" :disabled="props.disabled">
    <slot />
  </button>
</template>
