<script setup lang="ts">
import type { Stat } from './stat';

const props = defineProps<{ stats: readonly Stat[] }>();
</script>

<template>
  <div class="oa-stat-grid">
    <div v-for="stat in props.stats" :key="stat.label" class="oa-stat">
      <span class="oa-stat-label">{{ stat.label }}</span>
      <span class="oa-stat-value">{{ stat.value }}</span>
      <span v-if="stat.note" class="oa-stat-note">{{ stat.note }}</span>
    </div>
  </div>
</template>
