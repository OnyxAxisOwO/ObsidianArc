<script setup lang="ts">
const props = withDefaults(defineProps<{
  tone?: 'default' | 'muted' | 'danger' | 'warning';
}>(), { tone: 'default' });
</script>

<template>
  <span
    class="oa-badge"
    :class="{
      'oa-badge-muted': props.tone === 'muted',
      'oa-badge-danger': props.tone === 'danger',
      'oa-badge-warning': props.tone === 'warning',
    }"
  ><slot /></span>
</template>
