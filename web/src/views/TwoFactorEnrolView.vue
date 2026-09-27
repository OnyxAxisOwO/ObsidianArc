<script setup lang="ts">
// Where the operator's two-step policy holds an account until it enrols.
//
// A card like the sign-in one rather than a panel over the chat: nothing
// behind it would work — the server refuses this account everything but the
// enrolment endpoints — so drawing the chat there would only draw a row of
// errors. Signing out is the one other way off this screen.

import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { logout, type Account } from '@/api/auth';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import { safeNext, serverOwned } from '@/lib/next';
import { adopt, currentPreferences, currentUser, forget, siteInfo } from '@/stores/session';
import TwoFactorWizard from './settings/TwoFactorWizard.vue';
import { useLoginBackground } from '@/composables/useLoginBackground';

const route = useRoute();
const router = useRouter();
const site = computed(() => siteInfo.value);
const { loginBgUrl } = useLoginBackground();

async function enrolled(user: Account): Promise<void> {
  adopt(user, currentPreferences.value);
  const next = safeNext(route.query['next']) || '/';
  if (serverOwned(next)) {
    window.location.assign(next);
    return;
  }
  await router.replace(next);
}

function signOut(): void {
  void logout().catch(() => {
    // Already gone; there is nothing left to end.
  }).finally(() => {
    forget();
    void router.replace('/login');
  });
}
</script>

<template>
  <div
    class="oa-auth"
    :class="[
      { 'has-login-bg': !!loginBgUrl },
      `position-${site.auth_card_position || 'center'}`,
    ]"
    :style="loginBgUrl ? { backgroundImage: `url(${loginBgUrl})` } : undefined"
  >
    <div class="oa-auth-card oa-2fa-card">
      <div class="oa-auth-brand">
        <img v-if="site.logo_url" :src="site.logo_url" class="oa-auth-brand-logo" alt="">
        <span v-else class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </div>
      <h1 class="oa-auth-title">{{ t('twoFactorRequiredTitle') }}</h1>
      <p class="oa-auth-sub">{{ t('twoFactorRequiredBody', { site: site.name }) }}</p>

      <TwoFactorWizard @done="enrolled" />

      <p class="oa-auth-switch">
        <span v-if="currentUser">@{{ currentUser.username }} · </span>
        <button type="button" @click="signOut">{{ t('signOut') }}</button>
      </p>
    </div>
  </div>

  <div class="oa-auth-corner">
    <OaThemeToggle />
  </div>
</template>
