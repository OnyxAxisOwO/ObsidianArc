<script setup lang="ts">
// Sign in and sign up.
//
// One screen with two modes rather than two pages: the fields and the layout
// are nearly identical, and switching between them should not cost a
// navigation or re-render the card.

import { computed, nextTick, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { login, register, type Account } from '@/api/auth';
import { ApiError } from '@/api/client';
import OaField from '@/components/OaField.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t } from '@/composables/useI18n';
import { IconSpark } from '@/icons';
import { adopt, siteInfo } from '@/stores/session';

const props = defineProps<{ mode: 'login' | 'register' }>();

const router = useRouter();
const site = computed(() => siteInfo.value);

// An instance with no accounts is being set up: the person in front of it is
// about to become the administrator, and saying so removes the "did I just
// make a normal account?" doubt.
const setup = computed(() => site.value.setup_required);
const registering = computed(() => props.mode === 'register' || setup.value);

const domains = computed(() => site.value.email_domains ?? []);
const emailRequired = computed(() => !setup.value && (site.value.require_email ?? false));
const qqRequirement = computed(() =>
  site.value.qq_requirement ?? (site.value.require_qq ? 'required' : 'off'));
const qqRequired = computed(() => !setup.value && qqRequirement.value === 'required');
const qqEnabled = computed(() => !setup.value && qqRequirement.value !== 'off');

const identityLabel = computed(() => (registering.value ? t('username') : t('usernameOrEmail')));

// What to say under the address field: which domains are taken, that a link
// is coming, or both. Nothing when neither applies.
const emailHint = computed(() => {
  const parts: string[] = [];
  if (domains.value.length) parts.push(t('emailAccepted', { domains: domains.value.join(', ') }));
  if (!setup.value && (site.value.verify_email ?? false)) parts.push(t('verifySignupNote'));
  return parts.length ? parts.join(' ') : undefined;
});

const identifier = ref('');
const email = ref('');
const qq = ref('');
const password = ref('');
const error = ref('');
const busy = ref(false);
const buttonLabel = ref('');

const identifierField = ref<HTMLInputElement | null>(null);
const passwordField = ref<HTMLInputElement | null>(null);
const guard = ref<InstanceType<typeof OaTurnstile> | null>(null);

// Challenged on sign-up or sign-in when the operator switched them on.
// Never during first-run setup, where no challenge has been configured yet.
const guarded = computed(() =>
  registering.value
    ? !setup.value && !!site.value.turnstile_on_signup
    : !setup.value && !!site.value.turnstile_on_login,
);

onMounted(() => void nextTick(() => identifierField.value?.focus()));

async function onSubmit(): Promise<void> {
  if (busy.value) return;

  const identity = identifier.value.trim();
  const secret = password.value;
  if (!identity || !secret) {
    error.value = t('fillBothFields');
    return;
  }
  // Checked here as well as on the server, only so the answer is immediate.
  // The server is the one that decides.
  if (registering.value && emailRequired.value && !email.value.trim()) {
    error.value = t('emailRequiredHere');
    return;
  }
  const qqValue = qq.value.trim();
  if (registering.value && qqRequired.value && !qqValue) {
    error.value = t('qqRequiredHere');
    return;
  }
  if (registering.value && qqValue && !/^[1-9][0-9]{4,14}$/.test(qqValue)) {
    error.value = t('qqInvalid');
    return;
  }

  busy.value = true;
  error.value = '';
  buttonLabel.value = registering.value ? t('creatingAccount') : t('signingIn');
  // A review takes seconds. Saying so beats a button that sits on "creating
  // account" long enough to read as a form that has hung.
  const reviewNote = registering.value && site.value.signup_review
    ? window.setTimeout(() => { buttonLabel.value = t('signupReviewing'); }, 900)
    : 0;

  try {
    const result: { user: Account } = registering.value
      ? await register({
          username: identity,
          password: secret,
          email: email.value.trim(),
          qq: qqValue,
          turnstile: guard.value?.token() ?? '',
        })
      : await login(identity, secret, guard.value?.token() ?? '');

    window.clearTimeout(reviewNote);
    adopt(result.user);
    guard.value?.reset();
    await router.replace('/');
  } catch (failure) {
    window.clearTimeout(reviewNote);
    // A token is good for one submission, so a refusal for any reason — a
    // taken username as much as a failed challenge — leaves a spent token
    // behind that would fail the next attempt on its own.
    guard.value?.reset();
    error.value = refusal(failure);
    busy.value = false;
    buttonLabel.value = '';
    await nextTick();
    passwordField.value?.focus();
    passwordField.value?.select();
  }
}

/**
 * The refusals worth saying in the reader's own language. Everything else is
 * a server message with nothing to add.
 */
function refusal(failure: unknown): string {
  if (!(failure instanceof ApiError)) return String(failure);
  switch (failure.code) {
    case 'account_banned':
      return t('accountBanned');
    case 'signup_ip_blocked':
      return t('signupBlocked');
    case 'signup_refused': {
      // The operator's own words when they wrote any — a way to appeal is the
      // whole reason to write them — and a plain sentence otherwise.
      const notice = failure.details['notice'];
      return typeof notice === 'string' && notice.trim() ? notice : t('signupRefused');
    }
    case 'challenge_failed':
      return t('challengeFailed');
    case 'challenge_unavailable':
      return t('challengeUnavailable');
    case 'qq_required':
      return t('qqRequiredHere');
    case 'invalid_qq':
      return t('qqInvalid');
    case 'qq_taken':
      return t('qqTaken');
    case 'signups_throttled':
      return t('signupsThrottled', { count: Number(failure.details['retry_after_seconds'] ?? 60) });
    default: {
      const allowed = failure.details['allowed_domains'];
      if (Array.isArray(allowed) && allowed.length) {
        return t('emailDomainRejected', { domains: allowed.join(', ') });
      }
      if (domains.value.length && failure.status === 400 && /email/i.test(failure.message)) {
        return t('emailDomainRejected', { domains: domains.value.join(', ') });
      }
      return failure.message;
    }
  }
}
</script>

<template>
  <div class="oa-auth">
    <form class="oa-auth-card" novalidate @submit.prevent="onSubmit">
      <div class="oa-auth-brand">
        <span class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </div>

      <h1 class="oa-auth-title">
        {{ setup ? t('firstAccountTitle') : registering ? t('createAccountTitle') : t('welcomeBack') }}
      </h1>
      <p class="oa-auth-sub">
        {{ setup ? t('firstAccountBody') : registering ? t('createAccountBody') : t('welcomeBackBody') }}
      </p>

      <div class="oa-auth-form">
        <OaField :label="identityLabel">
          <input
            ref="identifierField"
            v-model="identifier"
            type="text"
            spellcheck="false"
            :placeholder="identityLabel"
            autocomplete="username"
            maxlength="254"
          >
        </OaField>

        <!-- The label carries whether it is optional; the hint carries which
             addresses will be taken. Both are things you want before typing,
             not after submitting. -->
        <OaField
          v-if="registering"
          :label="emailRequired ? t('email') : t('emailOptional')"
          :hint="emailHint"
        >
          <input
            v-model="email"
            type="email"
            spellcheck="false"
            :placeholder="domains.length ? `you@${domains[0]}` : 'you@example.com'"
            autocomplete="email"
            maxlength="254"
            :required="emailRequired"
          >
        </OaField>

        <OaField v-if="registering && qqEnabled" :label="qqRequired ? t('qq') : t('qqOptional')">
          <input
            v-model="qq"
            type="text"
            spellcheck="false"
            :placeholder="t('qqPlaceholder')"
            autocomplete="off"
            maxlength="15"
            :required="qqRequired"
          >
        </OaField>

        <OaField :label="t('password')">
          <input
            ref="passwordField"
            v-model="password"
            type="password"
            spellcheck="false"
            :placeholder="registering ? t('passwordHint') : t('password')"
            :autocomplete="registering ? 'new-password' : 'current-password'"
            maxlength="256"
          >
        </OaField>

        <OaTurnstile
          v-if="guarded"
          ref="guard"
          :site-key="site.turnstile_site_key ?? ''"
        />

        <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>

        <button
          type="submit"
          class="oa-btn primary oa-btn-block"
          :disabled="busy"
          :data-busy="busy ? 'true' : undefined"
        >
          {{ buttonLabel || (registering ? t('createAccount') : t('signIn')) }}
        </button>
      </div>

      <p v-if="!setup" class="oa-auth-switch">
        <template v-if="registering">
          <span>{{ t('haveAccount') }}</span>
          <button type="button" @click="router.push('/login')">{{ t('signIn') }}</button>
        </template>
        <template v-else-if="site.registration_enabled">
          <span>{{ t('noAccount') }}</span>
          <button type="button" @click="router.push('/register')">{{ t('createOne') }}</button>
        </template>
        <template v-else>{{ t('registrationClosed') }}</template>
      </p>

      <p v-if="site.description" class="oa-auth-note">{{ site.description }}</p>
    </form>
  </div>

  <!-- The theme toggle belongs here too: the sign-in page is the first thing
       a new user sees, and being stuck in the wrong scheme until they have an
       account would be an odd first impression. -->
  <div class="oa-auth-corner">
    <OaThemeToggle />
  </div>
</template>
