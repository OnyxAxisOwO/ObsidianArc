<script setup lang="ts">
import DefaultTheme from 'vitepress/theme';
import { useData } from 'vitepress';
import Home from './components/Home.vue';

const { Layout } = DefaultTheme;
const { frontmatter } = useData();
</script>

<template>
  <Layout class="oa-docs">
    <template #home-hero-before>
      <Home v-if="frontmatter.home" />
    </template>
  </Layout>
</template>