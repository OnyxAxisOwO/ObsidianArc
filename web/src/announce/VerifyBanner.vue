<script setup lang="ts">
// The strip that tells somebody their address is still unconfirmed.
//
// It sits above the chat rather than replacing it: an account in this state
// can sign in, read, and change its address — it just cannot spend anything
// until the link is opened. Locking it out of the interface entirely would
// leave nowhere to press resend from.

import { computed, ref } from 'vue';
import { resendVerification } from '@/api/auth';
import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';
import { currentUser, siteInfo } from '@/stores/session';

const visible = computed(() => {
  const account = currentUser.value;
  if (!account || account.email_verified) return false;
  // The server decides whether verification is in force. Without mail it is
  // inert, and an account left unverified from a period when it was on should
  // not be nagged about something it can no longer do.
  return siteInfo.value.verify_email ?? false;
});

const busy = ref(false);
const label = ref('');

function resend(): void {
  busy.value = true;
  void resendVerification()
    .then(() => { label.value = t('verifyResendSent'); })
    .catch((error: unknown) => {
      // Including the throttle, which is a real answer rather than a failure:
      // a link went out recently and is worth looking for.
      label.value = error instanceof ApiError ? error.message : t('failed');
    });
}
</script>

<template>
  <div v-if="visible" class="oa-verify-banner">
    <div class="oa-verify-text">
      <strong>{{ t('verifyBannerTitle') }}</strong>
      <span>
        {{ currentUser?.email
          ? t('verifyBannerBody', { email: currentUser.email })
          : t('verifyBannerNoAddress') }}
      </span>
    </div>
    <button
      v-if="currentUser?.email"
      type="button"
      class="oa-btn"
      :disabled="busy"
      @click="resend"
    >
      {{ label || t('verifyResend') }}
    </button>
  </div>
</template>
