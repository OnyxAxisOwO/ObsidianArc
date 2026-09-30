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

import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { safeNext, serverOwned } from '@/lib/next';
import { completeSignIn, fetchPoWChallenge, login, logout, register, type LoginResult, type PoWSolution } from '@/api/auth';
import { solvePoW, type PoWTask } from '@/lib/pow';
import { signInURL } from '@/api/oauth';
import HomeNotice from '@/announce/HomeNotice.vue';
import OaField from '@/components/OaField.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import OaAccountFields from '@/components/OaAccountFields.vue';
import { fieldProblem, fieldValues, signupFields } from '@/lib/account-fields';
import { guards, pluginOAuthError } from '@/plugins/registry';
import type { GuardAction } from '@/plugins/types';
import { t, type StringKey } from '@/composables/useI18n';
import { ApiError } from '@/api/client';
import { loginRefusalText, refusalText } from '@/lib/refusal';
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
// The account fields a plugin added, where sign-up asks for them. None for
// the first account, which no registration control applies to.
const fieldPlan = computed(() => (setup.value ? { keys: [], required: [] } : signupFields(site.value)));

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
const fields = ref<Record<string, string>>({});
const password = ref('');
const error = ref('');
const busy = ref(false);
const buttonLabel = ref('');

const identifierField = ref<HTMLInputElement | null>(null);
const passwordField = ref<HTMLInputElement | null>(null);
const guard = ref<InstanceType<typeof OaTurnstile> | null>(null);

let currentPoWTask: PoWTask | null = null;
let powPromise: Promise<PoWSolution> | null = null;
let powSolution: PoWSolution | null = null;

function startPoWIfNeeded(): void {
  if (!registering.value || !site.value.pow_on_signup) return;
  if (powSolution || powPromise) return;

  powPromise = fetchPoWChallenge()
    .then((challenge) => {
      currentPoWTask = solvePoW(challenge);
      return currentPoWTask.promise;
    })
    .then((solution) => {
      powSolution = solution;
      return solution;
    })
    .catch((err) => {
      powPromise = null;
      currentPoWTask = null;
      throw err;
    });
}

function resetPoW(): void {
  if (currentPoWTask) {
    currentPoWTask.cancel();
    currentPoWTask = null;
  }
  powPromise = null;
  powSolution = null;
}

watch(
  [registering, () => site.value.pow_on_signup],
  ([isReg, isPoW]) => {
    if (isReg && isPoW) {
      startPoWIfNeeded();
    } else {
      resetPoW();
    }
  },
  { immediate: true },
);

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

// A plugin's guards, on the same terms. Each is prepared as soon as the card
// opens on a door it stands at, rather than on submit: a service that scores
// behaviour has nothing to score if it starts at submit time.
const guardAction = computed<GuardAction>(() => (registering.value ? 'register' : 'login'));
const activeGuards = computed(() => (setup.value
  ? []
  : guards().filter(({ guard, config }) => guard.active(guardAction.value, config))));
const formEl = ref<HTMLFormElement | null>(null);

watch(activeGuards, (list) => {
  for (const { guard, config } of list) guard.prepare?.(guardAction.value, config);
}, { immediate: true });

// The sign-ins that do not start here. Never during setup: the first account
// is the administrator, and a provider cannot be configured before there is
// one to configure it.
const providers = computed(() => (setup.value ? [] : site.value.oauth ?? []));
const thirdPartyOnlySignup = computed(() => !!(site.value.oauth_only_signup || site.value.oidc_only_signup));

function onTurnstileSolved(): void {
  if (error.value === t('challengeRequired') || error.value === t('challengeFailed')) {
    error.value = '';
  }
}

function onProviderClick(event: MouseEvent, providerId: string): void {
  if (guarded.value) {
    const token = guard.value?.token() ?? '';
    if (!token) {
      event.preventDefault();
      error.value = t('challengeRequired');
      return;
    }
    event.preventDefault();
    window.location.assign(
      signInURL(providerId, {
        next: safeNext(route.query['next']),
        turnstile: token,
        register: registering.value,
      }),
    );
  }
}

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
  oidc_only: 'oauthOIDCOnly',
  third_party_only: 'oauthThirdPartyOnly',
  challenge_failed: 'challengeFailed',
  disabled: 'accountBanned',
  ip_blocked: 'signupBlocked',
  throttled: 'oauthThrottled',
  domain: 'oauthDomain',
  disposable_email: 'disposableEmailRejected',
  email_screening_unavailable: 'emailScreeningUnavailable',
  email_required: 'oauthEmailRequired',
};

onMounted(() => {
  if (typeof document !== 'undefined') {
    document.body.classList.add('has-auth-page');
  }
  const code = route.query['oauth_error'];
  if (typeof code === 'string' && code) {
    const banReason = route.query['ban_reason'];
    if (code === 'disabled' && typeof banReason === 'string' && banReason.trim()) {
      error.value = t('accountBannedWithReason', { reason: banReason.trim() });
    } else {
      const known = OAUTH_REFUSALS[code];
      error.value = known ? t(known) : pluginOAuthError(code) ?? t('oauthFailed');
    }
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
  resetPoW();
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
  const fieldError = registering.value ? fieldProblem(fields.value, fieldPlan.value) : null;
  if (fieldError) {
    error.value = fieldError;
    return;
  }
  if (registering.value && inviteMode.value === 'invite' && !inviteCode.value.trim()) {
    error.value = t('inviteRequiredHere');
    return;
  }

  busy.value = true;
  error.value = '';

  let solution: PoWSolution | undefined;
  if (registering.value && site.value.pow_on_signup) {
    if (!powSolution) {
      buttonLabel.value = t('powSolving');
      if (!powPromise) startPoWIfNeeded();
      try {
        if (powPromise) {
          solution = await powPromise;
        }
      } catch (failure) {
        busy.value = false;
        buttonLabel.value = '';
        error.value = refusalText(failure, domains.value);
        resetPoW();
        startPoWIfNeeded();
        return;
      }
    } else {
      solution = powSolution;
    }
  }

  // A guard's interactive check, where one asks, runs inside this call —
  // the button says what the reader is waiting on. A refusal here ends the
  // attempt before anything is submitted: resubmitting would only carry a
  // verdict the guard has already given.
  const guardTokens: Record<string, string> = {};
  for (const { guard, config } of activeGuards.value) {
    buttonLabel.value = guard.checking();
    try {
      guardTokens[guard.name] = await guard.token(guardAction.value, config, formEl.value ?? undefined);
    } catch (failure: unknown) {
      busy.value = false;
      buttonLabel.value = '';
      error.value = guard.failed(failure);
      return;
    }
  }

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
          fields: fieldValues(fields.value, fieldPlan.value),
          turnstile: guard.value?.token() ?? '',
          guards: guardTokens,
          inviteCode: inviteCode.value.trim(),
          ...(solution ? { pow: solution } : {}),
        })
      : await login(identity, secret, guard.value?.token() ?? '', guardTokens);

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
    resetPoW();
    startPoWIfNeeded();
    error.value = registering.value
      ? refusalText(failure, domains.value)
      : loginRefusalText(failure);
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
    :class="[
      { 'has-login-bg': !!loginBgUrl },
      `position-${site.auth_card_position || 'center'}`,
    ]"
    :style="loginBgUrl ? { backgroundImage: `url(${loginBgUrl})` } : undefined"
  >
    <!-- The instance's standing notice, for somebody who has not signed in
         either: this is the first page most of them see. -->
    <HomeNotice floating />
    <form ref="formEl" class="oa-auth-card" novalidate @submit.prevent="stage === 'code' ? onCode() : onSubmit()">
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
          {{
            setup
              ? t('firstAccountBody')
              : registering && thirdPartyOnlySignup
                ? t('oauthThirdPartyOnlyNotice')
                : registering
                  ? t('createAccountBody')
                  : t('welcomeBackBody')
          }}
        </p>

        <div v-if="registering && thirdPartyOnlySignup" class="oa-auth-form">
          <OaTurnstile
            v-if="guarded"
            ref="guard"
            :site-key="site.turnstile_site_key ?? ''"
            @solved="onTurnstileSolved"
          />
          <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>
          <div class="oa-auth-providers">
            <a
              v-for="provider in providers"
              :key="provider.id"
              class="oa-btn primary oa-btn-block oa-auth-provider"
              :href="signInURL(provider.id, { next: safeNext(route.query['next']), register: true })"
              @click="onProviderClick($event, provider.id)"
            >
              <component :is="mark(provider.id)" :size="15" />
              <span>{{ t('continueWith', { provider: provider.name }) }}</span>
            </a>
          </div>
        </div>

        <div v-else class="oa-auth-form">
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

          <OaAccountFields v-if="registering" v-model="fields" :plan="fieldPlan" />

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
            @solved="onTurnstileSolved"
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
                :href="signInURL(provider.id, { next: safeNext(route.query['next']), register: registering })"
                @click="onProviderClick($event, provider.id)"
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
          <template v-else-if="site.registration_enabled || thirdPartyOnlySignup">
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
