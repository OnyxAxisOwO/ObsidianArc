<script setup lang="ts">
// Two lines in one cell: a name and the thing that disambiguates it.

const props = defineProps<{
  title: string;
  sub?: string | undefined;
  /** For the cells that hold an id or a code rather than a name. */
  monospace?: boolean;
}>();
</script>

<template>
  <div class="oa-cell-stack">
    <span
      class="oa-cell-title"
      :style="props.monospace ? { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' } : undefined"
    >{{ props.title }}</span>
    <span v-if="props.sub" class="oa-cell-sub">{{ props.sub }}</span>
  </div>
</template>
