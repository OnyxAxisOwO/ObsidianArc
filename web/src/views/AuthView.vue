<script setup lang="ts">
// Sign in and sign up.
//
// One screen with two modes rather than two pages: the fields and the layout
// are nearly identical, and switching between them should not cost a
// navigation or re-render the card.
//
// Signing in has a second stage for an account with two-step verification:
// the same card asks for the code once the password was right. A provider
// sign-in arrives at that stage by redirect, with the session store already
// saying a code is wanted, so the card opens on it.

import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { safeNext, serverOwned } from '@/lib/next';
import { completeSignIn, login, logout, register, type LoginResult } from '@/api/auth';
import { signInURL } from '@/api/oauth';
import OaField from '@/components/OaField.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { ApiError } from '@/api/client';
import { refusalText } from '@/lib/refusal';
import { IconGithub, IconGoogle, IconKey, IconSpark, type OaIcon } from '@/icons';
import { adopt, forget, pendingSecondFactor, siteInfo } from '@/stores/session';
import { useLoginBackground } from '@/composables/useLoginBackground';

const props = defineProps<{ mode: 'login' | 'register' }>();

const router = useRouter();
const route = useRoute();
const site = computed(() => siteInfo.value);
const { loginBgUrl } = useLoginBackground();

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

// Registration mode, as the server derived it from registration.enabled and
// invites.required. Absent (an older server) reads as 'open' — the behaviour
// before the setting existed.
const inviteMode = computed(() => (setup.value ? 'open' : site.value.invite_mode ?? 'open'));
const inviteCode = ref('');
// Open once a code is required, or once one arrived on the link — never
// collapsed back on its own, so a link with ?invite= does not hide what it
// just filled in.
const inviteOpen = ref(false);
const inviteVisible = computed(() => registering.value && (inviteMode.value === 'invite' || inviteOpen.value));

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

// --- the second stage ----------------------------------------------------------

const stage = computed(() => (!registering.value && pendingSecondFactor.value ? 'code' : 'password'));
const code = ref('');
/** A recovery code instead of one from the app: a different field, not a
 *  different endpoint. */
const recoveryMode = ref(false);
const remember = ref(false);
const rememberDays = computed(() => site.value.two_factor_remember_days ?? 0);
const codeField = ref<HTMLInputElement | null>(null);

/** Where to go once signed in. A path the router has no screen for is the
 *  server's, and gets a real navigation. */
async function proceed(): Promise<void> {
  const next = safeNext(route.query['next']) || '/';
  if (serverOwned(next)) {
    window.location.assign(next);
    return;
  }
  await router.replace(next);
}

async function askForCode(): Promise<void> {
  pendingSecondFactor.value = true;
  code.value = '';
  recoveryMode.value = false;
  error.value = '';
  await nextTick();
  codeField.value?.focus();
}

async function onCode(): Promise<void> {
  const value = code.value.trim();
  if (busy.value || !value) return;
  busy.value = true;
  error.value = '';
  try {
    const result = await completeSignIn(value, remember.value);
    adopt(result.user);
    await proceed();
  } catch (failure) {
    busy.value = false;
    error.value = refusalText(failure);
    // Timed out or already gone: the code has nothing to finish, so the card
    // goes back to the password rather than asking for codes forever.
    if (failure instanceof ApiError && (failure.code === 'two_factor_expired' || failure.code === 'account_banned')) {
      forget();
      await nextTick();
      passwordField.value?.focus();
      return;
    }
    code.value = '';
    await nextTick();
    codeField.value?.focus();
  }
}

/** Six digits is the whole answer from an app, so there is nothing to wait for. */
function onCodeInput(event: Event): void {
  code.value = (event.target as HTMLInputElement).value;
  if (!recoveryMode.value && code.value.replace(/\D/g, '').length === 6) void onCode();
}

function toggleRecovery(): void {
  recoveryMode.value = !recoveryMode.value;
  code.value = '';
  error.value = '';
  void nextTick(() => codeField.value?.focus());
}

/** Out of the half-way sign-in, which the server forgets too. */
function startOver(): void {
  void logout().catch(() => {
    // Expired already; the cookie is worth nothing either way.
  }).finally(() => {
    forget();
    // The account that was halfway in is the one being left behind.
    identifier.value = '';
    password.value = '';
    error.value = '';
    void nextTick(() => identifierField.value?.focus());
  });
}

// Challenged on sign-up or sign-in when the operator switched them on.
// Never during first-run setup, where no challenge has been configured yet.
const guarded = computed(() =>
  registering.value
    ? !setup.value && !!site.value.turnstile_on_signup
    : !setup.value && !!site.value.turnstile_on_login,
);

// The sign-ins that do not start here. Never during setup: the first account
// is the administrator, and a provider cannot be configured before there is
// one to configure it.
const providers = computed(() => (setup.value ? [] : site.value.oauth ?? []));

const MARKS: Record<string, OaIcon> = { github: IconGithub, google: IconGoogle };
function mark(id: string): OaIcon {
  return MARKS[id] ?? IconKey;
}

/**
 * Why a provider sign-in came back without signing anybody in.
 *
 * The callback is a redirect, so there is no response body to read — the
 * reason arrives as a code in the query and is worded here, in the reader's
 * own language. Anything unrecognised gets the general sentence rather than
 * the raw code, which would mean nothing to the person reading it.
 */
const OAUTH_REFUSALS: Record<string, StringKey> = {
  denied: 'oauthDenied',
  state: 'oauthState',
  unavailable: 'oauthUnavailable',
  provider: 'oauthProviderFailed',
  address_taken: 'oauthAddressTaken',
  signup_closed: 'oauthSignupClosed',
  registration_closed: 'registrationClosed',
  disabled: 'accountBanned',
  ip_blocked: 'signupBlocked',
  throttled: 'oauthThrottled',
  domain: 'oauthDomain',
  disposable_email: 'disposableEmailRejected',
  email_screening_unavailable: 'emailScreeningUnavailable',
  email_required: 'oauthEmailRequired',
  qq_required: 'oauthQQRequired',
};

onMounted(() => {
  if (typeof document !== 'undefined') {
    document.body.classList.add('has-auth-page');
  }
  const code = route.query['oauth_error'];
  if (typeof code === 'string' && code) {
    error.value = t(OAUTH_REFUSALS[code] ?? 'oauthFailed');
    // Out of the address bar: a reload should not raise a message about a
    // sign-in that is long over.
    void router.replace({ path: route.path, query: {} });
  }
  // A partner or friend's link: the code the visitor arrived with is worth
  // more than an empty field they would have to go find again.
  const invite = route.query['invite'];
  if (typeof invite === 'string' && invite) {
    inviteCode.value = invite;
    inviteOpen.value = true;
  }
  void nextTick(() => (stage.value === 'code' ? codeField.value : identifierField.value)?.focus());
});

onUnmounted(() => {
  if (typeof document !== 'undefined') {
    document.body.classList.remove('has-auth-page');
  }
});

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
  if (registering.value && inviteMode.value === 'invite' && !inviteCode.value.trim()) {
    error.value = t('inviteRequiredHere');
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
    const result: LoginResult = registering.value
      ? await register({
          username: identity,
          password: secret,
          email: email.value.trim(),
          qq: qqValue,
          turnstile: guard.value?.token() ?? '',
          inviteCode: inviteCode.value.trim(),
        })
      : await login(identity, secret, guard.value?.token() ?? '');

    window.clearTimeout(reviewNote);
    guard.value?.reset();
    if (!result.user) {
      busy.value = false;
      buttonLabel.value = '';
      await askForCode();
      return;
    }
    adopt(result.user);
    // Back to whatever asked for a session — the consent screen, usually,
    // where another site is waiting on the answer.
    await proceed();
  } catch (failure) {
    window.clearTimeout(reviewNote);
    // A token is good for one submission, so a refusal for any reason — a
    // taken username as much as a failed challenge — leaves a spent token
    // behind that would fail the next attempt on its own.
    guard.value?.reset();
    error.value = refusalText(failure, domains.value);
    busy.value = false;
    buttonLabel.value = '';
    await nextTick();
    passwordField.value?.focus();
    passwordField.value?.select();
  }
}
</script>

<template>
  <div
    class="oa-auth"
    :class="{ 'has-login-bg': !!loginBgUrl }"
    :style="loginBgUrl ? { backgroundImage: `url(${loginBgUrl})` } : undefined"
  >
    <form class="oa-auth-card" novalidate @submit.prevent="stage === 'code' ? onCode() : onSubmit()">
      <div class="oa-auth-brand">
        <img v-if="site.logo_url" :src="site.logo_url" class="oa-auth-brand-logo" alt="">
        <span v-else class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </div>

      <template v-if="stage === 'code'">
        <h1 class="oa-auth-title">{{ t('twoFactorSignInTitle') }}</h1>
        <p class="oa-auth-sub">
          {{ recoveryMode ? t('twoFactorSignInRecoveryBody') : t('twoFactorSignInBody') }}
        </p>
        <div class="oa-auth-form">
          <OaField :label="recoveryMode ? t('twoFactorRecoveryLabel') : t('twoFactorCodeLabel')">
            <input
              ref="codeField"
              class="oa-2fa-code"
              :value="code"
              type="text"
              :inputmode="recoveryMode ? 'text' : 'numeric'"
              :autocomplete="recoveryMode ? 'off' : 'one-time-code'"
              spellcheck="false"
              :maxlength="recoveryMode ? 16 : 7"
              :placeholder="recoveryMode ? 'xxxxx-xxxxx' : '000000'"
              @input="onCodeInput"
            >
          </OaField>
          <label v-if="rememberDays > 0" class="oa-checkbox-field">
            <input v-model="remember" type="checkbox">
            <span>{{ t('twoFactorRemember', { days: rememberDays }) }}</span>
          </label>
          <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>
          <button
            type="submit"
            class="oa-btn primary oa-btn-block"
            :disabled="busy || !code.trim()"
            :data-busy="busy ? 'true' : undefined"
          >
            {{ busy ? t('twoFactorVerifying') : t('twoFactorVerify') }}
          </button>
        </div>
        <p class="oa-auth-switch">
          <button type="button" @click="toggleRecovery">
            {{ recoveryMode ? t('twoFactorUseApp') : t('twoFactorUseRecovery') }}
          </button>
          <span> · </span>
          <button type="button" @click="startOver">{{ t('twoFactorOtherAccount') }}</button>
        </p>
        <p v-if="recoveryMode" class="oa-auth-note">{{ t('twoFactorLostHelp') }}</p>
      </template>

      <template v-else>
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

          <!-- Required in invite-only mode; otherwise a collapsed link, so a
               field almost nobody fills in does not sit open on every visit. -->
          <p v-if="registering && inviteMode === 'open' && !inviteOpen" class="oa-auth-switch">
            <button type="button" @click="inviteOpen = true">{{ t('haveInviteCode') }}</button>
          </p>
          <OaField v-if="inviteVisible" :label="inviteMode === 'invite' ? t('inviteCodeLabel') : t('inviteCodeOptionalLabel')">
            <input
              v-model="inviteCode"
              type="text"
              spellcheck="false"
              :placeholder="t('inviteCodePlaceholder')"
              autocomplete="off"
              maxlength="32"
              :required="inviteMode === 'invite'"
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

          <!-- Links rather than buttons, because each one is a navigation to
               somebody else's site: the server answers with a redirect, which a
               fetch could not follow anywhere useful. -->
          <template v-if="providers.length">
            <p class="oa-auth-or"><span>{{ t('orContinueWith') }}</span></p>
            <div class="oa-auth-providers">
              <a
                v-for="provider in providers"
                :key="provider.id"
                class="oa-btn oa-auth-provider"
                :href="signInURL(provider.id, { next: safeNext(route.query['next']) })"
              >
                <component :is="mark(provider.id)" :size="15" />
                <span>{{ t('continueWith', { provider: provider.name }) }}</span>
              </a>
            </div>
          </template>
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
      </template>
    </form>
  </div>

  <!-- The theme toggle belongs here too: the sign-in page is the first thing
       a new user sees, and being stuck in the wrong scheme until they have an
       account would be an odd first impression. -->
  <div class="oa-auth-corner">
    <OaThemeToggle />
  </div>
</template>
