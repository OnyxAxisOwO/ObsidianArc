<script setup lang="ts">
// Everything between the front door and an account.
//
// Split out of the settings screen because these are one subject and it was
// not: whether registration is open, what a new account must supply, how fast
// addresses may open them, whether a browser has to prove itself, and whether
// a model is asked to look at the result. An operator dealing with a wave of
// junk accounts opens one page, not seven sections of another.

import { computed, onMounted, ref } from 'vue';
import { adminApi, type AdminModel, type Group, type SecurityEvent } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { absoluteTime } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navSecurity'), t('securitySubtitle'));

const error = ref('');
const loaded = ref(false);
const mailConfigured = ref(false);
const groups = ref<Group[]>([]);
const models = ref<AdminModel[]>([]);
const flash = ref('');
const saveLabel = ref('');
const busy = ref(false);
const events = ref<SecurityEvent[]>([]);
const eventsTotal = ref(0);
const eventsLoading = ref(false);

const form = ref({
  registration: false,
  defaultGroup: '',
  requireEmail: false,
  verifyEmail: false,
  emailDomains: '',
  qqRequirement: 'off',
  perMinute: 0 as number | null,
  perHour: 0 as number | null,
  perIP: 0 as number | null,
  ipWindow: 60 as number | null,
  turnstileSiteKey: '',
  turnstileSecret: '',
  turnstileSecretHint: '',
  turnstileOnLogin: false,
  turnstileOnSignup: false,
  turnstileOnAPIKey: false,
  chatChallengeRequests: 0 as number | null,
  chatChallengeWindowSecs: 60 as number | null,
  chatChallengeClearMins: 30 as number | null,
  reviewEnabled: false,
  reviewModel: '',
  reviewMode: 'normal',
  reviewRestrictHours: 24 as number | null,
  reviewRefusal: '',
});

// Trying the reviewer on an account that is not being created.
const trial = ref({ username: '', email: '', qq: '', answer: '', running: false });

const enabledModels = computed(() => models.value.filter((entry) => entry.enabled));

/**
 * Only this page's keys. The settings endpoint writes what it is given and
 * leaves the rest alone, which is what lets two screens edit one store
 * without either one reverting the other's fields.
 */
function collect(): Record<string, string> {
  return {
    'registration.enabled': String(form.value.registration),
    'registration.default_group': form.value.defaultGroup,
    'registration.require_email': String(form.value.requireEmail),
    'registration.verify_email': String(form.value.verifyEmail),
    'registration.email_domains': form.value.emailDomains.trim(),
    'registration.qq_requirement': form.value.qqRequirement,
    'registration.per_minute': String(form.value.perMinute ?? 0),
    'registration.per_hour': String(form.value.perHour ?? 0),
    'registration.per_ip': String(form.value.perIP ?? 0),
    'registration.per_ip_window_minutes': String(form.value.ipWindow ?? 60),
    'turnstile.site_key': form.value.turnstileSiteKey.trim(),
    // Empty keeps what is stored: the field was never shown the secret, so
    // sending its emptiness back would erase it.
    'turnstile.secret_key': form.value.turnstileSecret.trim(),
    'turnstile.on_login': String(form.value.turnstileOnLogin),
    'turnstile.on_signup': String(form.value.turnstileOnSignup),
    'turnstile.on_api_key': String(form.value.turnstileOnAPIKey),
    'security.chat_challenge_requests': String(form.value.chatChallengeRequests ?? 0),
    'security.chat_challenge_window_seconds': String(form.value.chatChallengeWindowSecs ?? 60),
    'security.chat_challenge_clear_minutes': String(form.value.chatChallengeClearMins ?? 30),
    'security.signup_review': String(form.value.reviewEnabled),
    'security.signup_review_model': form.value.reviewModel,
    'security.signup_review_mode': form.value.reviewMode,
    'security.signup_review_restrict_hours': String(form.value.reviewRestrictHours ?? 24),
    'security.signup_review_refusal': form.value.reviewRefusal.trim(),
  };
}

async function save(): Promise<void> {
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  try {
    await adminApi.saveSettings(collect());
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
    saveLabel.value = '';
  } finally {
    busy.value = false;
  }
}

/**
 * It exists because "the review is not working" and "the review is working
 * and being generous" look identical from outside: both are a registration
 * that went through. Type the account that got past it and read what the
 * model actually said — including that it could not be reached, which is the
 * state in which everything is allowed.
 */
function runTrial(): void {
  trial.value.running = true;
  trial.value.answer = t('reviewTrying');
  void adminApi.tryReview({
    username: trial.value.username.trim(),
    email: trial.value.email.trim(),
    qq: trial.value.qq.trim(),
    // What a browser would have sent, so the answer is about the details and
    // not about a missing user agent.
    user_agent: navigator.userAgent,
  })
    .then((result) => {
      trial.value.answer = !result.ran
        ? t('reviewTryBroken', { decision: reviewDecision(result.decision), reason: result.reason })
        : t(result.decision === 'allow'
          ? 'reviewTryAllowed'
          : result.decision === 'restrict' ? 'reviewTryRestricted' : 'reviewTryRefused',
        { reason: result.reason });
    })
    .catch((failure: unknown) => {
      trial.value.answer = failure instanceof ApiError ? failure.message : String(failure);
    })
    .finally(() => { trial.value.running = false; });
}

function reviewDecision(decision: string): string {
  if (decision === 'allow') return t('securityDecisionAllow');
  if (decision === 'refuse') return t('securityDecisionRefuse');
  if (decision === 'required') return t('securityDecisionRequired');
  if (decision === 'passed') return t('securityDecisionPassed');
  if (decision === 'failed') return t('securityDecisionFailed');
  return t('securityDecisionRestrict');
}

function eventLabel(event: string): string {
  if (event === 'signup_review') return t('securityEventSignupReview');
  if (event === 'api_restriction') return t('securityEventAPIRestriction');
  if (event === 'api_restriction_lifted') return t('securityEventAPIRestrictionLifted');
  if (event === 'chat_challenge') return t('securityEventChatChallenge');
  return event;
}

async function loadEvents(): Promise<void> {
  eventsLoading.value = true;
  try {
    const result = await adminApi.securityEvents('?limit=50');
    events.value = result.events ?? [];
    eventsTotal.value = result.total;
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    eventsLoading.value = false;
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    // The models come along because one of these settings is which model
    // reviews a sign-up, and a select needs its options. The groups arrive
    // with the settings already.
    const [data, modelsResult, securityResult] = await Promise.all([
      adminApi.settings(), adminApi.models(), adminApi.securityEvents('?limit=50'),
    ]);
    const values = data.settings;
    mailConfigured.value = data.mail_configured;
    groups.value = data.groups;
    models.value = modelsResult.models;
    events.value = securityResult.events ?? [];
    eventsTotal.value = securityResult.total;

    form.value = {
      registration: values['registration.enabled'] === 'true',
      defaultGroup: values['registration.default_group'] ?? '',
      requireEmail: values['registration.require_email'] === 'true',
      verifyEmail: values['registration.verify_email'] === 'true',
      emailDomains: values['registration.email_domains'] ?? '',
      qqRequirement: values['registration.qq_requirement'] ?? 'off',
      perMinute: Number(values['registration.per_minute'] ?? 0),
      perHour: Number(values['registration.per_hour'] ?? 0),
      perIP: Number(values['registration.per_ip'] ?? 0),
      ipWindow: Number(values['registration.per_ip_window_minutes'] ?? 60),
      turnstileSiteKey: values['turnstile.site_key'] ?? '',
      turnstileSecret: '',
      turnstileSecretHint: values['turnstile.secret_key'] ?? '',
      turnstileOnLogin: values['turnstile.on_login'] === 'true',
      turnstileOnSignup: values['turnstile.on_signup'] === 'true',
      turnstileOnAPIKey: values['turnstile.on_api_key'] === 'true',
      chatChallengeRequests: Number(values['security.chat_challenge_requests'] ?? 0),
      chatChallengeWindowSecs: Number(values['security.chat_challenge_window_seconds'] ?? 60),
      chatChallengeClearMins: Number(values['security.chat_challenge_clear_minutes'] ?? 30),
      reviewEnabled: values['security.signup_review'] === 'true',
      reviewModel: values['security.signup_review_model'] ?? '',
      reviewMode: values['security.signup_review_mode'] ?? 'normal',
      reviewRestrictHours: Number(values['security.signup_review_restrict_hours'] ?? 24),
      reviewRefusal: values['security.signup_review_refusal'] ?? '',
    };
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn primary" :disabled="busy" @click="save">
      {{ saveLabel || t('save') }}
    </button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <div v-else class="oa-settings-panel">
    <OaFormSection id="secAccounts" :title="t('secAccounts')" />
    <OaSwitchField
      v-model="form.registration"
      :label="t('anyoneCanRegister')"
      :hint="t('anyoneCanRegisterHint')"
    />
    <OaSelectField
      v-model="form.defaultGroup"
      :label="t('newAccountsJoin')"
      :options="[
        { value: '', label: t('theDefaultGroup') },
        ...groups.map((group) => ({ value: group.id, label: group.name })),
      ]"
    />

    <OaFormSection id="secRegistration" :title="t('secRegistration')" />
    <OaSwitchField v-model="form.requireEmail" :label="t('requireEmail')" :hint="t('requireEmailHint')" />
    <!-- Offered but inert without SMTP, and the hint says so. The server
         ignores it in that state too, so an operator cannot lock every new
         account out of an instance that cannot send the link. -->
    <div :class="{ 'oa-field-inert': !mailConfigured }">
      <OaSwitchField
        v-model="form.verifyEmail"
        :label="t('verifyEmail')"
        :hint="mailConfigured ? t('verifyEmailHint') : t('verifyEmailNoMail')"
      />
    </div>
    <OaTextArea
      v-model="form.emailDomains"
      :label="t('emailDomains')"
      :rows="2"
      :placeholder="t('emailDomainsPlaceholder')"
      :hint="t('emailDomainsHint')"
    />
    <OaSelectField
      v-model="form.qqRequirement"
      :label="t('qqRequirement')"
      :hint="t('qqRequirementHint')"
      :options="[
        { value: 'off', label: t('qqRequirementOff') },
        { value: 'optional', label: t('qqRequirementOptional') },
        { value: 'required', label: t('qqRequirementRequired') },
      ]"
    />
    <OaNumberField v-model="form.perMinute" :label="t('signupsPerMinute')" :min="0" :max="1000" />
    <OaNumberField
      v-model="form.perHour"
      :label="t('signupsPerHour')"
      :min="0"
      :max="10000"
      :hint="t('signupThrottleHint')"
    />
    <OaNumberField v-model="form.perIP" :label="t('signupsPerIP')" :min="0" :hint="t('signupsPerIPHint')" />
    <OaNumberField
      v-model="form.ipWindow"
      :label="t('signupsIPWindow')"
      :min="1"
      :hint="t('signupsIPWindowHint')"
    />

    <OaFormSection id="secTurnstile" :title="t('secTurnstile')" :hint="t('turnstileHint')" />
    <OaTextField
      v-model="form.turnstileSiteKey"
      :label="t('turnstileSiteKey')"
      placeholder="0x4AAAAAAA…"
      :hint="t('turnstileSiteKeyHint')"
      monospace
    />
    <OaTextField
      v-model="form.turnstileSecret"
      :label="t('turnstileSecretKey')"
      :placeholder="form.turnstileSecretHint || '0x4AAAAAAA…'"
      :hint="t('turnstileSecretHint')"
      monospace
    />
    <OaSwitchField
      v-model="form.turnstileOnLogin"
      :label="t('turnstileOnLogin')"
      :hint="t('turnstileOnLoginHint')"
    />
    <OaSwitchField
      v-model="form.turnstileOnSignup"
      :label="t('turnstileOnSignup')"
      :hint="t('turnstileOnSignupHint')"
    />
    <OaSwitchField
      v-model="form.turnstileOnAPIKey"
      :label="t('turnstileOnAPIKey')"
      :hint="t('turnstileOnAPIKeyHint')"
    />
    <OaNumberField
      v-model="form.chatChallengeRequests"
      :label="t('chatChallengeRequests')"
      :hint="t('chatChallengeRequestsHint')"
      :min="0"
      :max="1000"
    />
    <template v-if="(form.chatChallengeRequests ?? 0) > 0">
      <OaNumberField
        v-model="form.chatChallengeWindowSecs"
        :label="t('chatChallengeWindow')"
        :hint="t('chatChallengeWindowHint')"
        :min="5"
        :max="3600"
      />
      <OaNumberField
        v-model="form.chatChallengeClearMins"
        :label="t('chatChallengeClearance')"
        :hint="t('chatChallengeClearanceHint')"
        :min="1"
        :max="1440"
      />
    </template>

    <OaFormSection id="secSignupReview" :title="t('secSignupReview')" :hint="t('signupReviewIntro')" />
    <OaSwitchField v-model="form.reviewEnabled" :label="t('signupReview')" :hint="t('signupReviewHint')" />
    <OaSelectField
      v-model="form.reviewModel"
      :label="t('signupReviewModel')"
      :hint="t('signupReviewModelHint')"
      :options="[
        { value: '', label: t('signupReviewNoModel') },
        ...enabledModels.map((entry) => ({ value: entry.id, label: entry.display_name })),
      ]"
    />
    <OaSelectField
      v-model="form.reviewMode"
      :label="t('signupReviewMode')"
      :hint="t('signupReviewModeHint')"
      :options="[
        { value: 'loose', label: t('reviewModeLoose') },
        { value: 'normal', label: t('reviewModeNormal') },
        { value: 'strict', label: t('reviewModeStrict') },
      ]"
    />
    <OaNumberField
      v-model="form.reviewRestrictHours"
      :label="t('signupReviewRestrictHours')"
      :hint="t('signupReviewRestrictHoursHint')"
      :min="0"
      :max="8760"
    />
    <OaTextArea
      v-model="form.reviewRefusal"
      :label="t('signupReviewRefusal')"
      :rows="3"
      :placeholder="t('signupReviewRefusalPlaceholder')"
      :hint="t('signupReviewRefusalHint')"
    />

    <div class="oa-field">
      <span class="oa-field-label">{{ t('reviewTry') }}</span>
      <span class="oa-field-hint">{{ t('reviewTryHint') }}</span>
      <OaTextField v-model="trial.username" :label="t('username')" placeholder="123123123123" />
      <OaTextField v-model="trial.email" :label="t('email')" placeholder="123123123123@qq.com" />
      <OaTextField v-model="trial.qq" :label="t('qq')" placeholder="123123123123" />
      <button type="button" class="oa-btn" :disabled="trial.running" @click="runTrial">
        {{ t('reviewTryRun') }}
      </button>
      <p class="oa-field-hint">{{ trial.answer }}</p>
    </div>

    <OaFormSection id="secSecurityLog" :title="t('secSecurityLog')" :hint="t('securityLogHint')" />
    <button type="button" class="oa-btn" :disabled="eventsLoading" @click="loadEvents">
      {{ t('refresh') }}
    </button>
    <p v-if="eventsLoading" class="oa-table-empty">{{ t('loading') }}</p>
    <p v-else-if="!events.length" class="oa-table-empty">{{ t('securityLogEmpty') }}</p>
    <div v-else class="oa-log-list">
      <div v-for="event in events" :key="event.id" class="oa-log-row">
        <div class="oa-log-row-main">
          <div class="oa-log-row-head">
            <span class="oa-log-path">{{ eventLabel(event.event) }}</span>
            <OaBadge
              :tone="event.severity === 'danger'
                ? 'danger' : event.severity === 'warning' ? 'warning' : 'muted'"
            >{{ reviewDecision(event.decision ?? '') }}</OaBadge>
          </div>
          <div class="oa-log-row-meta">
            <span>{{ absoluteTime(event.at) }}</span>
            <span v-if="event.username">@{{ event.username }}</span>
            <span v-if="event.ip">{{ event.ip }}</span>
            <span v-if="event.actor_username">
              {{ t('securityLogActor', { name: `@${event.actor_username}` }) }}
            </span>
          </div>
          <span v-if="event.reason" class="oa-field-hint">{{ event.reason }}</span>
        </div>
      </div>
    </div>
    <p v-if="eventsTotal > events.length" class="oa-field-hint">
      {{ t('securityLogShowing', { shown: events.length, total: eventsTotal }) }}
    </p>

    <p class="oa-drawer-flash" :class="{ visible: !!flash }">{{ flash }}</p>
  </div>
</template>
