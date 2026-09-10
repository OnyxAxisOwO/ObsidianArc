<script setup lang="ts">
defineProps<{
  title?: string;
  subtitle?: string;
}>();
</script>

<template>
  <div class="oa-card">
    <div v-if="title || subtitle" class="oa-card-header">
      <div v-if="title" class="oa-card-title">{{ title }}</div>
      <div v-if="subtitle" class="oa-card-subtitle">{{ subtitle }}</div>
    </div>
    <div class="oa-card-body">
      <slot />
    </div>
  </div>
</template>
