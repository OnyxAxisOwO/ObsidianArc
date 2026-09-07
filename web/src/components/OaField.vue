<script setup lang="ts">
// A labelled row: the name above the control, the explanation below it.
//
// A <label> rather than a div, so the text is part of the control's hit area
// and reads as its name — which is also why the select's outside-click check
// has to treat a click on this element as inside.

const props = defineProps<{
  label: string;
  hint?: string | undefined;
}>();
</script>

<template>
  <label class="oa-field">
    <span class="oa-field-label">{{ props.label }}</span>
    <slot />
    <span v-if="props.hint" class="oa-field-hint">{{ props.hint }}</span>
    <!-- After the hint, for the live readouts that belong to a field rather
         than to its explanation — the credits box says what its number buys. -->
    <slot name="after" />
  </label>
</template>
