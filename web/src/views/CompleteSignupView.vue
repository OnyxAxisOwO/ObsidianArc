<script setup lang="ts">
// The step a provider sign-in stops at when this server wants something the
// provider had no way to supply — an account field a plugin requires, or an
// address where GitHub proved none.
//
// It is the sign-up form with everything a provider already answered taken
// out, which is usually one field. Nothing has been written when somebody
// lands here: the identity is held in a signed cookie, and the account is
// opened by the button below. Walking away leaves no account behind.

import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { completeSignup, fetchPendingSignup, type PendingSignup } from '@/api/oauth';
import OaAccountFields from '@/components/OaAccountFields.vue';
import OaField from '@/components/OaField.vue';
import { fieldProblem, fieldValues, type FieldPlan } from '@/lib/account-fields';
import { fieldSpec } from '@/plugins/registry';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t } from '@/composables/useI18n';
import { IconGithub, IconGoogle, IconKey, IconSpark, type OaIcon } from '@/icons';
import { refusalText } from '@/lib/refusal';
import { siteInfo } from '@/stores/session';
import { useLoginBackground } from '@/composables/useLoginBackground';

const router = useRouter();
const route = useRoute();
const site = computed(() => siteInfo.value);
const { loginBgUrl } = useLoginBackground();

// Same rule the sign-up form uses: a code is asked for here too, because this
// screen is the other door an account gets created through.
const inviteMode = computed(() => site.value.invite_mode ?? 'open');
const inviteCode = ref('');
const inviteOpen = ref(false);
const inviteVisible = computed(() => inviteMode.value === 'invite' || inviteOpen.value);

const pending = ref<PendingSignup | null>(null);
const gone = ref('');
const error = ref('');
const busy = ref(false);

const username = ref('');
const password = ref('');
const fields = ref<Record<string, string>>({});
const email = ref('');

// The required fields are asked for; a value the provider already answered
// (a subject a plugin binds to a field) rides along whether or not it is.
const fieldPlan = computed<FieldPlan>(() => {
  const required = (pending.value?.needs.fields ?? []).filter((key) => fieldSpec(key));
  return { keys: required, required };
});

const showPassword = computed(() => pending.value?.needs_password !== false);
const passwordRequired = computed(() => !!pending.value?.password_required);

const MARKS: Record<string, OaIcon> = { github: IconGithub, google: IconGoogle };
const mark = computed<OaIcon>(() => MARKS[pending.value?.provider ?? ''] ?? IconKey);

const domains = computed(() => pending.value?.email_domains ?? []);

/** Which addresses would be accepted, and whether a link is coming. */
const emailHint = computed(() => {
  const parts: string[] = [];
  if (domains.value.length) parts.push(t('emailAccepted', { domains: domains.value.join(', ') }));
  if (pending.value?.verify_email) parts.push(t('verifySignupNote'));
  return parts.length ? parts.join(' ') : undefined;
});

onMounted(() => {
  void fetchPendingSignup()
    .then((result) => {
      pending.value = result;
      // A provider that vouches with a field's value itself fills it; the
      // answer still goes through the button, so it stays editable.
      fields.value = { ...(result.fields ?? {}) };
      if (result.suggested_username) {
        username.value = result.suggested_username;
      } else if (result.login && /^[A-Za-z0-9._-]{3,32}$/.test(result.login)) {
        username.value = result.login;
      }
    })
    .catch(() => { gone.value = t('signupCompleteGone'); });
  const invite = route.query['invite'];
  if (typeof invite === 'string' && invite) {
    inviteCode.value = invite;
    inviteOpen.value = true;
  }
});

async function submit(): Promise<void> {
  if (busy.value || !pending.value) return;

  const chosenUsername = username.value.trim();
  if (!chosenUsername) {
    error.value = t('usernameRequired');
    return;
  }
  if (!/^[A-Za-z0-9._-]{3,32}$/.test(chosenUsername)) {
    error.value = t('usernameInvalid');
    return;
  }

  const chosenPassword = password.value;
  if (showPassword.value) {
    if (passwordRequired.value && !chosenPassword) {
      error.value = t('passwordRequired');
      return;
    }
    if (chosenPassword && chosenPassword.length < 8) {
      error.value = t('passwordTooShort');
      return;
    }
  }

  // Checked here only so the answer is immediate; the server decides.
  const fieldError = fieldProblem(fields.value, fieldPlan.value);
  if (fieldError) {
    error.value = fieldError;
    return;
  }
  const address = email.value.trim();
  if (pending.value.needs.email && !address) {
    error.value = t('emailRequiredHere');
    return;
  }
  if (inviteMode.value === 'invite' && !inviteCode.value.trim()) {
    error.value = t('inviteRequiredHere');
    return;
  }

  busy.value = true;
  error.value = '';
  try {
    const details: {
      username: string; password?: string; fields: Record<string, string>; email: string; inviteCode: string;
    } = {
      username: chosenUsername,
      fields: { ...fields.value, ...fieldValues(fields.value, fieldPlan.value) },
      email: address,
      inviteCode: inviteCode.value.trim(),
    };
    if (showPassword.value && chosenPassword) {
      details.password = chosenPassword;
    }
    const { redirect } = await completeSignup(details);
    // A full navigation rather than a route change: the session cookie has
    // just been set, and everything on the other side of this reads the
    // account once at boot.
    window.location.href = redirect;
  } catch (failure) {
    error.value = refusalText(failure, domains.value);
    busy.value = false;
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
    <form class="oa-auth-card" novalidate @submit.prevent="submit">
      <div class="oa-auth-brand">
        <img v-if="site.logo_url" :src="site.logo_url" class="oa-auth-brand-logo" alt="">
        <span v-else class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </div>

      <template v-if="gone">
        <h1 class="oa-auth-title">{{ t('signupCompleteGoneTitle') }}</h1>
        <p class="oa-auth-sub">{{ gone }}</p>
        <button type="button" class="oa-btn oa-btn-block" @click="router.replace('/login')">
          {{ t('signIn') }}
        </button>
      </template>

      <template v-else-if="!pending">
        <p class="oa-auth-sub">{{ t('loading') }}</p>
      </template>

      <template v-else>
        <h1 class="oa-auth-title">{{ t('signupCompleteTitle') }}</h1>
        <p class="oa-auth-sub">
          {{ t('signupCompleteBody', { provider: pending.provider_name || pending.provider }) }}
        </p>

        <!-- Whose sign-in this is finishing. Without it the page is a stranger
             asking for details. -->
        <div class="oa-connection oa-signup-who">
          <span class="oa-connection-mark"><component :is="mark" :size="16" /></span>
          <span class="oa-connection-body">
            <span class="oa-connection-name">{{ pending.login || pending.provider_name }}</span>
            <span v-if="pending.email" class="oa-connection-meta">{{ pending.email }}</span>
          </span>
        </div>

        <div class="oa-auth-form">
          <OaField :label="t('username')">
            <input
              v-model="username"
              type="text"
              spellcheck="false"
              :placeholder="t('usernameHint')"
              autocomplete="username"
              maxlength="32"
              required
            >
          </OaField>

          <OaField
            v-if="showPassword"
            :label="passwordRequired ? t('password') : t('passwordOptional')"
          >
            <input
              v-model="password"
              type="password"
              spellcheck="false"
              :placeholder="passwordRequired ? t('passwordHint') : t('passwordOptionalHint')"
              autocomplete="new-password"
              maxlength="256"
              :required="passwordRequired"
            >
          </OaField>

          <OaAccountFields v-model="fields" :plan="fieldPlan" />

          <OaField v-if="pending.needs.email" :label="t('email')" :hint="emailHint">
            <input
              v-model="email"
              type="email"
              spellcheck="false"
              :placeholder="domains.length ? `you@${domains[0]}` : 'you@example.com'"
              autocomplete="email"
              maxlength="254"
            >
          </OaField>

          <p v-if="inviteMode === 'open' && !inviteOpen" class="oa-auth-switch">
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

          <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>

          <button type="submit" class="oa-btn primary oa-btn-block" :disabled="busy">
            {{ busy ? t('creatingAccount') : t('signupCompleteSubmit') }}
          </button>
        </div>

        <p class="oa-auth-switch">
          <button type="button" @click="router.replace('/login')">
            {{ t('signupCompleteCancel') }}
          </button>
        </p>
      </template>
    </form>
  </div>

  <div class="oa-auth-corner">
    <OaThemeToggle />
  </div>
</template>
