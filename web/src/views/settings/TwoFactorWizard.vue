<script setup lang="ts">
// Setting up two-step verification, one step at a time.
//
// Four steps rather than one form, because most people meeting this have
// never used an authenticator app, and the order matters: an app before a
// picture to scan, a code from it before anything changes, and the recovery
// codes last — the only moment they exist in plain text. Nothing on the
// account changes until the third step succeeds, so abandoning the wizard at
// any point before it leaves the account exactly as it was.
//
// The same component is drawn in three places — the security settings, the
// screen the policy holds an account at, and the backoffice's gate — so it
// adopts nothing itself. It hands the updated account to whoever drew it, and
// only on the last step: adopting it earlier would let a gate that is watching
// the account disappear with the recovery codes still unsaved.

import { computed, nextTick, ref } from 'vue';
import type { Account } from '@/api/auth';
import { saveAsFile } from '@/api/backup';
import { ApiError } from '@/api/client';
import { beginTwoFactor, enableTwoFactor, type TwoFactorSetup } from '@/api/twofactor';
import { copyToClipboard } from '@/chat/markdown';
import OaTextField from '@/components/OaTextField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconCheck, IconCopy, IconDownload } from '@/icons';

const props = withDefaults(defineProps<{
  /** Offer a way back out of the first step. The gates do not: there is
   *  nowhere to go back to. */
  cancellable?: boolean;
}>(), { cancellable: false });

const emit = defineEmits<{
  (event: 'done', user: Account): void;
  (event: 'cancel'): void;
}>();

type Step = 'app' | 'scan' | 'verify' | 'save';
const STEPS: Array<{ id: Step; label: StringKey }> = [
  { id: 'app', label: 'twoFactorStepApp' },
  { id: 'scan', label: 'twoFactorStepScan' },
  { id: 'verify', label: 'twoFactorStepVerify' },
  { id: 'save', label: 'twoFactorStepSave' },
];

const step = ref<Step>('app');
const index = computed(() => STEPS.findIndex((entry) => entry.id === step.value));
const setup = ref<TwoFactorSetup | null>(null);
const showKey = ref(false);
const code = ref('');
// Turning this on signs every other device out, so the server asks the
// account for its password too — unless it has none to ask for.
const password = ref('');
const busy = ref(false);
const error = ref('');
const recovery = ref<string[]>([]);
const saved = ref(false);
const copied = ref('');
let enabledAccount: Account | null = null;

const codeField = ref<HTMLInputElement | null>(null);
const passwordField = ref<InstanceType<typeof OaTextField> | null>(null);
const needsPassword = computed(() => setup.value?.password_required === true);
/** Everything the confirm button needs before it can be pressed. */
const ready = computed(() =>
  code.value.replace(/\D/g, '').length === 6 && (!needsPassword.value || password.value !== ''));

/** The suggestions as tags. One string per language rather than a list of
 *  keys, because which apps are worth naming differs by where people are. */
const apps = computed(() => t('twoFactorAppsList').split('·').map((name) => name.trim()).filter(Boolean));

/** Four at a time, the way apps and password managers print keys. */
const groupedKey = computed(() => (setup.value?.secret ?? '').replace(/(.{4})(?=.)/g, '$1 '));

function refusal(failure: unknown): string {
  if (!(failure instanceof ApiError)) return String(failure);
  switch (failure.code) {
    case 'two_factor_code': return t('twoFactorCodeWrong');
    case 'two_factor_no_setup': return t('twoFactorNoSetup');
    case 'two_factor_unavailable': return t('twoFactorUnavailable');
    case 'password_required': return t('twoFactorPasswordRequired');
    case 'current_password_wrong': return t('currentPasswordWrong');
    default: return failure.message;
  }
}

async function begin(): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    setup.value = await beginTwoFactor();
    showKey.value = false;
    code.value = '';
    step.value = 'scan';
  } catch (failure) {
    error.value = refusal(failure);
  } finally {
    busy.value = false;
  }
}

async function toVerify(): Promise<void> {
  error.value = '';
  step.value = 'verify';
  await nextTick();
  if (needsPassword.value) passwordField.value?.focus();
  else codeField.value?.focus();
}

async function verify(): Promise<void> {
  const digits = code.value.replace(/\D/g, '');
  if (busy.value || digits.length !== 6) return;
  if (needsPassword.value && password.value === '') {
    error.value = t('twoFactorPasswordRequired');
    return;
  }
  busy.value = true;
  error.value = '';
  try {
    const result = needsPassword.value
      ? await enableTwoFactor(digits, password.value)
      : await enableTwoFactor(digits);
    enabledAccount = result.user;
    recovery.value = result.recovery_codes;
    password.value = '';
    step.value = 'save';
  } catch (failure) {
    error.value = refusal(failure);
    // An expired setup cannot be confirmed by any code; the way on is a new
    // secret, which is the first step's button.
    if (failure instanceof ApiError && failure.code === 'two_factor_no_setup') step.value = 'app';
    if (failure instanceof ApiError
      && (failure.code === 'current_password_wrong' || failure.code === 'password_required')) {
      // The code is still good for its thirty seconds; only the password is
      // asked for again.
      password.value = '';
      await nextTick();
      passwordField.value?.focus();
    } else {
      code.value = '';
      await nextTick();
      codeField.value?.focus();
    }
  } finally {
    busy.value = false;
  }
}

/** Six digits is the whole answer, so there is nothing to wait for — unless
 *  the password has not been typed yet, which the button then waits on. */
function onCodeInput(event: Event): void {
  code.value = (event.target as HTMLInputElement).value;
  if (code.value.replace(/\D/g, '').length === 6 && (!needsPassword.value || password.value !== '')) {
    void verify();
  }
}

function copy(value: string): void {
  void copyToClipboard(value).then((ok) => {
    if (!ok) return;
    copied.value = value;
    window.setTimeout(() => {
      if (copied.value === value) copied.value = '';
    }, 1500);
  });
}

function recoveryText(): string {
  const heading = t('twoFactorRecoveryFile', {
    account: setup.value?.account ?? '',
    issuer: setup.value?.issuer ?? '',
  });
  return `${heading}\n\n${recovery.value.join('\n')}\n`;
}

function download(): void {
  const name = (setup.value?.issuer ?? 'account').replace(/[^\p{L}\p{N}._-]+/gu, '-');
  saveAsFile(`${name}-recovery-codes.txt`, recoveryText(), 'text/plain');
}

function finish(): void {
  if (!saved.value || !enabledAccount) return;
  emit('done', enabledAccount);
}
</script>

<template>
  <div class="oa-2fa-wizard">
    <ol class="oa-2fa-steps" :aria-label="t('twoFactorSetUp')">
      <li
        v-for="(entry, position) in STEPS"
        :key="entry.id"
        class="oa-2fa-step"
        :class="{ active: position === index, done: position < index }"
        :aria-current="position === index ? 'step' : undefined"
      >
        <span class="oa-2fa-step-dot">
          <IconCheck v-if="position < index" :size="12" />
          <template v-else>{{ position + 1 }}</template>
        </span>
        <span class="oa-2fa-step-label">{{ t(entry.label) }}</span>
      </li>
    </ol>

    <!-- 1. An app to put the secret in. -->
    <section v-if="step === 'app'" class="oa-2fa-pane">
      <header class="oa-2fa-pane-head">
        <h3 class="oa-2fa-title">{{ t('twoFactorAppTitle') }}</h3>
        <p class="oa-2fa-desc">{{ t('twoFactorAppBody') }}</p>
      </header>
      <ul class="oa-2fa-apps">
        <li v-for="app in apps" :key="app">{{ app }}</li>
      </ul>
      <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>
      <footer class="oa-2fa-foot">
        <button v-if="props.cancellable" type="button" class="oa-btn" @click="emit('cancel')">
          {{ t('cancel') }}
        </button>
        <button type="button" class="oa-btn primary" :disabled="busy" @click="begin">
          {{ t('twoFactorAppHaveOne') }}
        </button>
      </footer>
    </section>

    <!-- 2. The secret, as a picture and, for a camera that will not
         cooperate, as text. -->
    <section v-else-if="step === 'scan' && setup" class="oa-2fa-pane">
      <header class="oa-2fa-pane-head">
        <h3 class="oa-2fa-title">{{ t('twoFactorScanTitle') }}</h3>
        <p class="oa-2fa-desc">{{ t('twoFactorScanBody', { issuer: setup.issuer }) }}</p>
      </header>
      <div class="oa-2fa-scan">
        <!-- The quiet zone is part of the picture: a scanner needs four light
             modules around the symbol to find its edge. -->
        <svg
          class="oa-2fa-qr"
          :viewBox="`-4 -4 ${setup.qr.size + 8} ${setup.qr.size + 8}`"
          shape-rendering="crispEdges"
          role="img"
          :aria-label="t('twoFactorScanTitle')"
        >
          <rect class="oa-2fa-qr-light" x="-4" y="-4" :width="setup.qr.size + 8" :height="setup.qr.size + 8" />
          <path class="oa-2fa-qr-dark" :d="setup.qr.path" />
        </svg>
        <button type="button" class="oa-2fa-link" :aria-expanded="showKey" @click="showKey = !showKey">
          {{ t('twoFactorCantScan') }}
        </button>
        <div v-if="showKey" class="oa-2fa-key">
          <p class="oa-2fa-desc">{{ t('twoFactorManualHint') }}</p>
          <div class="oa-2fa-key-row">
            <span>{{ t('twoFactorAccountLabel') }}</span>
            <span class="oa-2fa-key-value">{{ setup.account }}</span>
          </div>
          <div class="oa-2fa-key-row">
            <span>{{ t('twoFactorKeyLabel') }}</span>
            <button type="button" class="oa-2fa-secret" :title="t('copy')" @click="copy(setup.secret)">
              <span>{{ groupedKey }}</span>
              <IconCheck v-if="copied === setup.secret" :size="13" />
              <IconCopy v-else :size="13" />
            </button>
          </div>
        </div>
      </div>
      <footer class="oa-2fa-foot">
        <button type="button" class="oa-btn" @click="step = 'app'">{{ t('back') }}</button>
        <button type="button" class="oa-btn primary" @click="toVerify">{{ t('twoFactorContinue') }}</button>
      </footer>
    </section>

    <!-- 3. A code from the app, which is what proves the two agree. -->
    <form v-else-if="step === 'verify' && setup" class="oa-2fa-pane" novalidate @submit.prevent="verify">
      <header class="oa-2fa-pane-head">
        <h3 class="oa-2fa-title">{{ t('twoFactorVerifyTitle') }}</h3>
        <p class="oa-2fa-desc">{{ t('twoFactorVerifyBody', { issuer: setup.issuer }) }}</p>
      </header>
      <OaTextField
        v-if="needsPassword"
        ref="passwordField"
        v-model="password"
        :label="t('currentPassword')"
        :hint="t('twoFactorPasswordHint')"
        type="password"
        autocomplete="current-password"
      />
      <div class="oa-field">
        <input
          ref="codeField"
          class="oa-2fa-code"
          :value="code"
          type="text"
          inputmode="numeric"
          autocomplete="one-time-code"
          spellcheck="false"
          maxlength="7"
          placeholder="000000"
          :aria-label="t('twoFactorCodeLabel')"
          @input="onCodeInput"
        >
      </div>
      <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>
      <footer class="oa-2fa-foot">
        <button type="button" class="oa-btn" :disabled="busy" @click="step = 'scan'">{{ t('back') }}</button>
        <button
          type="submit"
          class="oa-btn primary"
          :disabled="busy || !ready"
        >
          {{ busy ? t('twoFactorVerifying') : t('twoFactorVerifyAndEnable') }}
        </button>
      </footer>
    </form>

    <!-- 4. The recovery codes, the one time they exist in plain text. -->
    <section v-else-if="step === 'save'" class="oa-2fa-pane">
      <header class="oa-2fa-pane-head">
        <h3 class="oa-2fa-title">{{ t('twoFactorSaveTitle') }}</h3>
        <p class="oa-2fa-desc">{{ t('twoFactorSaveBody') }}</p>
      </header>
      <div class="oa-2fa-codes-box">
        <ol class="oa-2fa-codes">
          <li v-for="entry in recovery" :key="entry">{{ entry }}</li>
        </ol>
        <div class="oa-2fa-codes-actions">
          <button type="button" class="oa-btn" @click="copy(recoveryText())">
            <IconCheck v-if="copied === recoveryText()" :size="14" />
            <IconCopy v-else :size="14" />
            {{ copied === recoveryText() ? t('copied') : t('copy') }}
          </button>
          <button type="button" class="oa-btn" @click="download">
            <IconDownload :size="14" />
            {{ t('download') }}
          </button>
        </div>
      </div>
      <label class="oa-checkbox-field">
        <input v-model="saved" type="checkbox">
        <span>{{ t('twoFactorSavedConfirm') }}</span>
      </label>
      <footer class="oa-2fa-foot">
        <button type="button" class="oa-btn primary" :disabled="!saved" @click="finish">
          {{ t('twoFactorFinish') }}
        </button>
      </footer>
    </section>
  </div>
</template>
