<script setup lang="ts">
const props = defineProps<{ title: string }>();
</script>

<template>
  <div class="oa-admin-section">
    <h2 class="oa-admin-section-title">{{ props.title }}</h2>
    <slot />
  </div>
</template>
