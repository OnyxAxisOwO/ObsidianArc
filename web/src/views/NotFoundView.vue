<script setup lang="ts">
// An address that names nothing.
//
// Signed in it is drawn inside the workspace, so the header and the way back
// are still there; signed out it is the notice on its own, because there is
// no workspace to put it in.

import { useRoute, useRouter } from 'vue-router';
import AppShell from '@/layouts/AppShell.vue';
import { t } from '@/composables/useI18n';
import { currentUser } from '@/stores/session';

const route = useRoute();
const router = useRouter();
</script>

<template>
  <AppShell v-if="currentUser" @brand="router.push('/')">
    <div class="oa-notice">
      <h2 class="oa-notice-title">{{ t('noSuchPage') }}</h2>
      <p class="oa-notice-body">{{ t('noSuchPageBody', { path: route.path }) }}</p>
    </div>
  </AppShell>
  <div v-else class="oa-notice">
    <h2 class="oa-notice-title">{{ t('noSuchPage') }}</h2>
    <p class="oa-notice-body">{{ t('noSuchPageBody', { path: route.path }) }}</p>
  </div>
</template>
