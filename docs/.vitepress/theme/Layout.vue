<script setup lang="ts">
import DefaultTheme from 'vitepress/theme';

const { Layout } = DefaultTheme;
</script>

<template>
  <Layout class="oa-theme-root">
    <template #layout-top>
      <div class="oa-ambient-glow" aria-hidden="true">
        <div class="oa-glow-top" />
        <div class="oa-glow-accent" />
      </div>
      <slot name="layout-top" />
    </template>

    <template v-for="(_, slotName) in $slots" #[slotName]="slotProps">
      <slot v-if="slotName !== 'layout-top'" :name="slotName" v-bind="slotProps" />
    </template>
  </Layout>
</template>
