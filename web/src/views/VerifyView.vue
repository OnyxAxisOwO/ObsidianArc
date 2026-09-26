<script setup lang="ts">
// The page a verification link lands on.
//
// It has to work for somebody who is not signed in — the link is opened out
// of a mail client, quite possibly in a different browser from the one that
// registered — so it does its own request and never assumes a session.

import { nextTick, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { fetchMe, verifyEmail } from '@/api/auth';
import { t, type StringKey } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import { verificationErrorKey } from '@/lib/verification-error';
import { adopt, currentUser, siteInfo } from '@/stores/session';
import { useLoginBackground } from '@/composables/useLoginBackground';

const route = useRoute();
const router = useRouter();
const { loginBgUrl } = useLoginBackground();

const title = ref<StringKey>('verifyPageReadyTitle');
const body = ref<StringKey | null>(null);
const action = ref<StringKey | null>(null);
const actionButton = ref<HTMLButtonElement | null>(null);
const token = ref('');
const busy = ref(false);
const nextAction = ref<'confirm' | 'continue' | 'signin' | null>(null);

onMounted(() => {
  const query = route.query;
  const queryToken = query['token'];
  if ('token' in query) {
    // The token is single-use; leaving it in the address bar invites it into a
    // history entry, a bookmark or a screenshot.
    const clean = { ...query };
    delete clean['token'];
    void router.replace({ path: route.path, query: clean });
  }

  token.value = typeof queryToken === 'string' ? queryToken : '';
  if (!token.value) {
    title.value = 'verifyPageMissingTitle';
    body.value = 'verifyPageMissingBody';
    action.value = 'signIn';
    nextAction.value = 'signin';
    return;
  }

  title.value = 'verifyPageReadyTitle';
  body.value = 'verifyPageReadyBody';
  action.value = 'verifyPageConfirm';
  nextAction.value = 'confirm';
});

async function confirm(): Promise<void> {
  if (busy.value || !token.value) return;
  busy.value = true;
  title.value = 'verifyPageChecking';
  body.value = null;
  action.value = 'verifyPageChecking';
  try {
    await verifyEmail(token.value);
    token.value = '';
    if (currentUser.value) {
      try {
        const session = await fetchMe();
        adopt(session.user, session.preferences);
      } catch {
        // A valid link has already changed the account; refreshing the cookie-backed view is best effort.
      }
    }
    title.value = 'verifyPageDone';
    body.value = null;
    action.value = 'verifyPageContinue';
    nextAction.value = 'continue';
    await nextTick();
    actionButton.value?.focus();
  } catch (error) {
    title.value = 'verifyPageFailedTitle';
    body.value = verificationErrorKey(error);
    // An invalid or already-used link still leaves sign-in as a useful next step.
    action.value = 'signIn';
    nextAction.value = 'signin';
  } finally {
    busy.value = false;
  }
}

function activate(): void {
  if (nextAction.value === 'confirm') {
    void confirm();
  } else if (nextAction.value === 'continue') {
    void router.replace('/');
  } else if (nextAction.value === 'signin') {
    void router.replace('/login');
  }
}
</script>

<template>
  <div
    class="oa-auth"
    :class="{ 'has-login-bg': !!loginBgUrl }"
    :style="loginBgUrl ? { backgroundImage: `url(${loginBgUrl})` } : undefined"
  >
    <div class="oa-auth-card">
      <div class="oa-auth-brand">
        <img v-if="siteInfo.logo_url" :src="siteInfo.logo_url" class="oa-auth-brand-logo" alt="">
        <span v-else class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ siteInfo.name }}</span>
      </div>
      <h1 class="oa-auth-title">{{ t(title) }}</h1>
      <p v-if="body" class="oa-auth-sub">{{ t(body) }}</p>
      <button
        ref="actionButton"
        type="button"
        class="oa-btn primary oa-btn-block"
        :hidden="!action"
        :disabled="busy"
        @click="activate"
      >{{ action ? t(action) : '' }}</button>
    </div>
  </div>
</template>
