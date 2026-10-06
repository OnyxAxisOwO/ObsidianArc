<script setup lang="ts">
import type { Component } from 'vue';
import OaGroup from '@/components/OaGroup.vue';
defineProps<{ title: string; hint?: string | undefined; icon?: Component | undefined }>();
</script>

<template>
  <OaGroup class="oa-control-card" :title="title" :hint="hint" :icon="icon">
    <template #actions><slot name="actions" /></template>
    <slot />
  </OaGroup>
</template>
