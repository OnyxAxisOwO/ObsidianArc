<script setup lang="ts">
// Where the operator's OIDC policy holds an account until it links an
// identity — see settings.OAuthOIDCRequireForAll and oauth.Service.
// MustBindOIDC.
//
// A card like the two-step enrolment one and for the same reason: nothing
// behind it would work, the server refuses this account everything but the
// connect flow and a handful of read-only endpoints, so drawing the chat
// there would only draw a row of errors. Signing out is the one other way
// off this screen.
//
// Connecting itself is not a request from here, the way it is not one from
// the settings screen: the button is a link the browser navigates, out to the
// provider and back to a callback that writes the connection and redirects
// here again — at which point the account this page reads should no longer
// need it, and the query string says why the browser just arrived.

import { computed, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { fetchMe, logout } from '@/api/auth';
import { signInURL } from '@/api/oauth';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import { safeNext } from '@/lib/next';
import { adopt, currentUser, forget, siteInfo } from '@/stores/session';
import { useLoginBackground } from '@/composables/useLoginBackground';

const route = useRoute();
const router = useRouter();
const site = computed(() => siteInfo.value);
const { loginBgUrl } = useLoginBackground();

// The one provider this screen ever offers a button for: OIDC is the
// identity the policy is about, and a GitHub or Google connection would not
// satisfy it. Absent when the operator has switched the provider itself off
// while the policy is still on — see oauth.Service.RequireForAll — in which
// case there is nothing this screen can ask for yet.
const provider = computed(() => site.value.oauth?.find((item) => item.id === 'oidc'));

const next = computed(() => safeNext(route.query['next']) || '/');
const connectURL = computed(() => signInURL('oidc', { link: true, next: '/bind-oidc?next=' + encodeURIComponent(next.value) }));

// The connect flow leaves this page and comes back to it, so its outcome
// arrives in the query rather than in a response — the same contract the
// settings screen's own connections panel reads.
onMounted(async () => {
  const done = route.query['oauth'];
  const failure = route.query['oauth_error'];
  if (done || failure) void router.replace({ path: route.path, query: {} });
  if (done !== 'connected') return;
  // The callback just linked the identity; refresh the account so the field
  // that held this screen open clears and the router lets the visit through.
  try {
    const session = await fetchMe();
    adopt(session.user, session.preferences);
    const destination = next.value;
    if (destination !== '/bind-oidc') await router.replace(destination);
  } catch {
    // The connection is written either way; refreshing the cookie-backed
    // view is best effort.
  }
});

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
      <h1 class="oa-auth-title">{{ t('oidcBindingRequiredTitle') }}</h1>
      <p class="oa-auth-sub">{{ t('oidcBindingRequiredBody', { site: site.name }) }}</p>

      <a v-if="provider" class="oa-btn primary oa-btn-block" :href="connectURL">
        {{ t('oidcBindingButton', { provider: provider.name }) }}
      </a>

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
