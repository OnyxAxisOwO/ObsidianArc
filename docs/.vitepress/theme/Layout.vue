<script setup lang="ts">
import DefaultTheme from 'vitepress/theme';

const { Layout } = DefaultTheme;
</script>

<template>
  <Layout class="oa-theme-root">
    <template #layout-top>
      <div class="oa-ambient-glow" aria-hidden="true" />
    </template>
  </Layout>
</template>
