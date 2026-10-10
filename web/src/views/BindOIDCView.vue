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
// Connecting starts with a request from here, as it does on the settings
// screen: the server checks the account's proof (its password, where it has
// one) and answers with the provider's consent screen, which the browser then
// navigates to. The callback there writes the connection and redirects here
// again — at which point the account this page reads should no longer need it,
// and the query string says why the browser just arrived.

import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { fetchMe, logout } from '@/api/auth';
import { ApiError } from '@/api/client';
import { connectProvider } from '@/api/oauth';
import OaField from '@/components/OaField.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import { connectRefusalText } from '@/lib/refusal';
import { safeNext } from '@/lib/next';
import { adopt, currentUser, forget, siteInfo } from '@/stores/session';
import { useLoginBackground } from '@/composables/useLoginBackground';
import OaBackdrop from '@/components/OaBackdrop.vue';

const route = useRoute();
const router = useRouter();
const site = computed(() => siteInfo.value);
const { loginBg, loginBgUrl, loginBgFrame } = useLoginBackground();

// The one provider this screen ever offers a button for: OIDC is the
// identity the policy is about, and a GitHub or Google connection would not
// satisfy it. Absent when the operator has switched the provider itself off
// while the policy is still on — see oauth.Service.RequireForAll — in which
// case there is nothing this screen can ask for yet.
const provider = computed(() => site.value.oauth?.find((item) => item.id === 'oidc'));

const next = computed(() => safeNext(route.query['next']) || '/');

// Connecting is a request, not a link: the account proves itself first, and the
// browser leaves for the provider only with the address the server answers. An
// account with a password is asked for it here, as the settings screen asks, and
// nothing is started until the password is right.
const askPassword = ref(false);
const password = ref('');
const busy = ref(false);
const flash = ref('');

function connect(): void {
  sendConnect('');
}

function submitConnect(): void {
  sendConnect(password.value);
}

function sendConnect(proof: string): void {
  busy.value = true;
  flash.value = '';
  void connectProvider('oidc', { password: proof, next: '/bind-oidc?next=' + encodeURIComponent(next.value) })
    .then((answer) => {
      // Off this screen for good: the provider's consent screen, then back here.
      window.location.assign(answer.redirect);
    })
    .catch((error: unknown) => {
      if (error instanceof ApiError && error.code === 'password_required') {
        askPassword.value = true;
        return;
      }
      if (error instanceof ApiError && error.code === 'current_password_wrong') {
        askPassword.value = true;
        password.value = '';
        flash.value = t('currentPasswordWrong');
        return;
      }
      flash.value = connectRefusalText(error);
    })
    .finally(() => { busy.value = false; });
}

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
      { 'has-login-bg': !!loginBg },
      `position-${site.auth_card_position || 'center'}`,
    ]"
    :style="loginBgUrl ? { backgroundImage: `url(${loginBgUrl})` } : undefined"
  >
    <OaBackdrop v-if="loginBgFrame" :key="loginBgFrame" :url="loginBgFrame" />
    <div class="oa-auth-card oa-2fa-card">
      <div class="oa-auth-brand">
        <img v-if="site.logo_url" :src="site.logo_url" class="oa-auth-brand-logo" alt="">
        <span v-else class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </div>
      <h1 class="oa-auth-title">{{ t('oidcBindingRequiredTitle') }}</h1>
      <p class="oa-auth-sub">{{ t('oidcBindingRequiredBody', { site: site.name }) }}</p>

      <template v-if="provider">
        <OaField
          v-if="askPassword"
          :label="t('currentPassword')"
          :hint="t('connectPasswordHint', { provider: provider.name })"
        >
          <input
            v-model="password"
            type="password"
            spellcheck="false"
            autocomplete="current-password"
            maxlength="256"
            @keydown.enter.prevent="submitConnect"
          >
        </OaField>
        <p v-if="flash" class="oa-auth-error" role="alert">{{ flash }}</p>
        <button
          type="button"
          class="oa-btn primary oa-btn-block"
          :disabled="busy"
          @click="askPassword ? submitConnect() : connect()"
        >
          {{ t('oidcBindingButton', { provider: provider.name }) }}
        </button>
      </template>

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
