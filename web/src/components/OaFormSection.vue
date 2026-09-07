<script setup lang="ts">
// A labelled divider between groups of fields inside one form.

const props = defineProps<{
  title: string;
  hint?: string | undefined;
}>();
</script>

<template>
  <div class="oa-form-section">
    <h3 class="oa-drawer-subhead">{{ props.title }}</h3>
    <p v-if="props.hint" class="oa-field-hint">{{ props.hint }}</p>
  </div>
</template>
