<script setup lang="ts">
// The backoffice's door, where the operator asks for a code on top of the one
// signing in took.
//
// Drawn by the shell in place of whichever page was asked for, so the page
// never mounts, never asks the server for anything and never draws the row
// of refusals it would get. Once the code is in, the shell mounts the page
// afresh.

import { computed, nextTick, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError } from '@/api/client';
import { enterBackoffice } from '@/api/twofactor';
import { t } from '@/composables/useI18n';
import { IconLock } from '@/icons';
import { refusalText } from '@/lib/refusal';
import { currentUser } from '@/stores/session';
import { minutesLabel, failureText } from './shared';

const emit = defineEmits<{ (event: 'unlocked'): void }>();

const router = useRouter();
const code = ref('');
const busy = ref(false);
const error = ref('');
const field = ref<HTMLInputElement | null>(null);

/** Says which kind of door this is, so the visit's length is not a surprise. */
const explanation = computed(() => {
  const minutes = minutesLabel(currentUser.value?.two_factor_backoffice_minutes ?? 15);
  switch (currentUser.value?.two_factor_backoffice_verify) {
    case 'idle': return t('backofficeUnlockIdle', { duration: minutes });
    case 'interval': return t('backofficeUnlockInterval', { duration: minutes });
    default: return t('backofficeUnlockVisit');
  }
});

onMounted(() => { void nextTick(() => field.value?.focus()); });

async function submit(): Promise<void> {
  const value = code.value.trim();
  if (busy.value || !value) return;
  busy.value = true;
  error.value = '';
  try {
    await enterBackoffice(value);
    emit('unlocked');
  } catch (failure) {
    error.value = failure instanceof ApiError ? refusalText(failure) : failureText(failure);
    code.value = '';
    busy.value = false;
    await nextTick();
    field.value?.focus();
  }
}

/** Six digits from the app is the whole answer; a recovery code has letters
 *  and a dash, and waits for the button. */
function onInput(event: Event): void {
  code.value = (event.target as HTMLInputElement).value;
  if (/^\s*\d{3}\s?\d{3}\s*$/.test(code.value)) void submit();
}
</script>

<template>
  <form class="oa-2fa-gate oa-2fa-unlock" novalidate @submit.prevent="submit">
    <header class="oa-2fa-unlock-head">
      <span class="oa-2fa-head-mark"><IconLock :size="18" /></span>
      <div class="oa-2fa-pane-head">
        <h2 class="oa-2fa-title">{{ t('backofficeUnlockTitle') }}</h2>
        <p class="oa-2fa-desc">{{ explanation }}</p>
      </div>
    </header>
    <div class="oa-field">
      <input
        ref="field"
        class="oa-2fa-code"
        :value="code"
        type="text"
        inputmode="numeric"
        autocomplete="one-time-code"
        spellcheck="false"
        maxlength="16"
        placeholder="000000"
        :aria-label="t('twoFactorCodeLabel')"
        @input="onInput"
      >
    </div>
    <p class="oa-auth-error" role="alert" :hidden="!error">{{ error }}</p>
    <p class="oa-2fa-desc">{{ t('backofficeUnlockRecovery') }}</p>
    <footer class="oa-2fa-foot">
      <button type="button" class="oa-btn" @click="router.push('/')">{{ t('backToChat') }}</button>
      <button type="submit" class="oa-btn primary" :disabled="busy || !code.trim()">
        {{ busy ? t('twoFactorVerifying') : t('backofficeUnlockEnter') }}
      </button>
    </footer>
  </form>
</template>
