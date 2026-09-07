<script setup lang="ts">
// The page a verification link lands on.
//
// It has to work for somebody who is not signed in — the link is opened out
// of a mail client, quite possibly in a different browser from the one that
// registered — so it does its own request and never assumes a session.

import { nextTick, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { verifyEmail } from '@/api/auth';
import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import { siteInfo } from '@/stores/session';

const route = useRoute();
const router = useRouter();

const title = ref(t('verifyPageChecking'));
const body = ref('');
const action = ref('');
const actionButton = ref<HTMLButtonElement | null>(null);

onMounted(() => {
  const token = String(route.query['token'] ?? '');
  if ('token' in route.query) {
    // The token is single-use and is now spent; leaving it in the address bar
    // only invites it into a history entry, a bookmark or a screenshot.
    const clean = { ...route.query };
    delete clean['token'];
    void router.replace({ path: route.path, query: clean });
  }

  void verifyEmail(token)
    .then(async () => {
      title.value = t('verifyPageDone');
      body.value = '';
      action.value = t('verifyPageContinue');
      await nextTick();
      actionButton.value?.focus();
    })
    .catch((error: unknown) => {
      title.value = t('verifyPageFailedTitle');
      body.value = error instanceof ApiError ? error.message : String(error);
      // Still somewhere to go: the link may have already been used, in which
      // case the account is fine and signing in is the right next step.
      action.value = t('signIn');
    });
});
</script>

<template>
  <div class="oa-auth">
    <div class="oa-auth-card">
      <div class="oa-auth-brand">
        <span class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ siteInfo.name }}</span>
      </div>
      <h1 class="oa-auth-title">{{ title }}</h1>
      <p class="oa-auth-sub">{{ body }}</p>
      <button
        ref="actionButton"
        type="button"
        class="oa-btn primary oa-btn-block"
        :hidden="!action"
        @click="router.replace('/')"
      >{{ action }}</button>
    </div>
  </div>
</template>
