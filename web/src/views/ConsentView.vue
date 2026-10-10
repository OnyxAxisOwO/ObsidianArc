<script setup lang="ts">
// "X wants to sign you in with your account here."
//
// The one screen in this application whose job is to be read rather than
// used. Somebody arrives from another site and has to decide whether to let
// it in, so the card is built around the four things that decision needs and
// nothing else: who is asking, who they would be let in as, what they would
// learn, and where the browser is about to be sent.
//
// The two marks at the top are the whole shape of the transaction in one
// glance — the application on the left, the person on the right, a run of
// dots between them. It is the same thing the sentence says, said faster.
//
// A page of its own rather than a panel, like the sign-in card: there is no
// chat behind this, and a column sliding in over one would be the wrong shape
// for a decision.

import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { ApiError } from '@/api/client';
import { decideConsent, fetchConsent, type ConsentRequest } from '@/api/consent';
import OaAvatar from '@/components/OaAvatar.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconArrowUpRight, IconCheck, IconSpark } from '@/icons';
import { initials } from '@/lib/account';
import { currentUser, siteInfo } from '@/stores/session';

const route = useRoute();
const site = computed(() => siteInfo.value);
const account = computed(() => currentUser.value);

const request = ref<ConsentRequest | null>(null);
const problem = ref('');
const busy = ref(false);

/** The ticket, exactly as the server signed it. */
const ticket = computed(() => {
  const value = route.query['request'];
  return typeof value === 'string' ? value : '';
});

/** What each scope would actually tell the application, in one line. */
const SCOPES: Record<string, StringKey> = {
  openid: 'scopeOpenID',
  profile: 'scopeProfile',
  email: 'scopeEmail',
  groups: 'scopeGroups',
};
function scopeLine(scope: string): string {
  const key = SCOPES[scope];
  return key ? t(key) : scope;
}

/**
 * The host the browser is about to be sent to.
 *
 * The whole callback is long and mostly noise; the host is the part worth
 * reading, and it is the part an impostor cannot fake — the server only ever
 * redirects to a callback the operator registered for that application.
 */
const destination = computed(() => {
  const target = request.value?.redirect_uri ?? '';
  try {
    return new URL(target).host;
  } catch {
    return target;
  }
});

const PROBLEMS: Record<string, StringKey> = {
  unknown_client: 'consentUnknownClient',
  bad_redirect: 'consentBadRedirect',
  failed: 'consentFailed',
};

onMounted(() => {
  const refused = route.query['error'];
  if (typeof refused === 'string' && refused) {
    // Own keys only, as in AuthView: an inherited name is not a refusal.
    const known = Object.hasOwn(PROBLEMS, refused) ? PROBLEMS[refused] : undefined;
    problem.value = t(known ?? 'consentFailed');
    return;
  }
  if (!ticket.value) {
    problem.value = t('consentFailed');
    return;
  }
  void fetchConsent(ticket.value)
    .then((result) => { request.value = result; })
    .catch((error: unknown) => {
      problem.value = error instanceof ApiError ? error.message : t('consentFailed');
    });
});

/**
 * Both buttons go through the same call, because a refusal is something the
 * application has to be told about: it is waiting at its own callback either
 * way, and an answer it never receives is a tab that sits there forever.
 */
function decide(approve: boolean): void {
  if (busy.value || !ticket.value) return;
  busy.value = true;
  void decideConsent(ticket.value, approve)
    .then((result) => {
      // Somebody else's site: a full navigation, not a route change.
      window.location.href = result.redirect;
    })
    .catch((error: unknown) => {
      problem.value = error instanceof ApiError ? error.message : t('consentFailed');
      busy.value = false;
    });
}
</script>

<template>
  <div class="oa-auth">
    <div class="oa-auth-card oa-consent-card">
      <!-- Which server this is. First, and in the same place as on the
           sign-in card, because "am I where I think I am" is the question
           underneath every other one on this screen. -->
      <div class="oa-auth-brand">
        <img v-if="site.logo_url" :src="site.logo_url" class="oa-auth-brand-logo" alt="">
        <span v-else class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </div>

      <template v-if="problem">
        <h1 class="oa-auth-title">{{ t('consentProblemTitle') }}</h1>
        <p class="oa-auth-sub">{{ problem }}</p>
        <p class="oa-auth-note">{{ t('consentProblemHint') }}</p>
      </template>

      <template v-else-if="!request">
        <p class="oa-auth-sub">{{ t('loading') }}</p>
      </template>

      <template v-else>
        <div class="oa-consent-head">
          <div class="oa-consent-parties">
            <span class="oa-consent-party">{{ initials(request.application.name) }}</span>
            <span class="oa-consent-run" aria-hidden="true"><i /><i /><i /></span>
            <OaAvatar v-if="account" class="oa-consent-party" :account="account" large />
          </div>

          <h1 class="oa-auth-title">
            {{ t('consentTitle', { application: request.application.name }) }}
          </h1>
          <!-- The operator's own words about the application, when they
               wrote any: it is the one description of it that did not come
               from the application itself. -->
          <p v-if="request.application.description" class="oa-auth-sub">
            {{ request.application.description }}
          </p>
          <p class="oa-consent-as">
            <template v-if="account">{{ t('consentAs', { name: account.username }) }}</template>
            <template v-else>{{ t('consentBody', { site: site.name }) }}</template>
          </p>
        </div>

        <p class="oa-consent-label">{{ t('consentWillLearn') }}</p>
        <ul class="oa-consent-scopes">
          <li v-for="scope in request.scopes" :key="scope">
            <span class="oa-consent-tick"><IconCheck :size="11" /></span>
            <span>{{ scopeLine(scope) }}</span>
          </li>
        </ul>

        <p class="oa-consent-where">
          <IconArrowUpRight :size="13" />
          <span>{{ t('consentDestination', { host: destination }) }}</span>
        </p>

        <!-- Side by side, not stacked: two full-width blocks read as a form
             to fill in, and this is a question to answer. -->
        <div class="oa-consent-actions">
          <button type="button" class="oa-btn" :disabled="busy" @click="decide(false)">
            {{ t('consentRefuse') }}
          </button>
          <button type="button" class="oa-btn primary" :disabled="busy" @click="decide(true)">
            {{ t('consentAllow') }}
          </button>
        </div>
      </template>
    </div>
  </div>

  <div class="oa-auth-corner">
    <OaThemeToggle />
  </div>
</template>
