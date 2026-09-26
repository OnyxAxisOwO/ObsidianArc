<script setup lang="ts">
// The strip that tells somebody their address is still unconfirmed.
// It sits above the chat so a pending account can still reach resend and profile settings.

import { computed, ref } from 'vue';
import { fetchMe, resendVerification, verifyEmailCode } from '@/api/auth';
import { t, type StringKey } from '@/composables/useI18n';
import { verificationErrorKey } from '@/lib/verification-error';
import { adopt, currentUser, siteInfo } from '@/stores/session';

const visible = computed(() => {
  const account = currentUser.value;
  if (!account || account.email_verified) return false;
  return siteInfo.value.verify_email ?? false;
});

const busy = ref(false);
const codeBusy = ref(false);
const code = ref('');
const label = ref<StringKey | null>(null);

function resend(): void {
  if (busy.value || codeBusy.value) return;
  busy.value = true;
  label.value = null;
  void resendVerification()
    .then(() => { label.value = 'verifyResendSent'; })
    .catch((error: unknown) => { label.value = verificationErrorKey(error); })
    .finally(() => { busy.value = false; });
}

async function verifyCode(): Promise<void> {
  if (codeBusy.value || !/^\d{6}$/.test(code.value)) return;
  codeBusy.value = true;
  label.value = null;
  try {
    await verifyEmailCode(code.value);
    try {
      const session = await fetchMe();
      adopt(session.user, session.preferences);
    } catch {
      // The code is already accepted; a session refresh can be retried by reloading.
    }
    label.value = 'verifyCodeSuccess';
  } catch (error) {
    label.value = verificationErrorKey(error);
  } finally {
    codeBusy.value = false;
  }
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
    <form v-if="currentUser?.email" class="oa-verify-code" @submit.prevent="verifyCode">
      <input
        v-model="code"
        :aria-label="t('verifyCodeLabel')"
        :placeholder="t('verifyCodePlaceholder')"
        inputmode="numeric"
        autocomplete="one-time-code"
        maxlength="6"
        pattern="[0-9]{6}"
        :disabled="codeBusy"
        @input="code = code.replace(/\D/g, '').slice(0, 6); label = null"
      >
      <button type="submit" class="oa-btn primary" :disabled="codeBusy || code.length !== 6">
        {{ codeBusy ? t('verifying') : t('verifyCodeSubmit') }}
      </button>
      <button type="button" class="oa-btn" :disabled="busy || codeBusy" @click="resend">
        {{ busy ? t('sending') : t('verifyResend') }}
      </button>
    </form>
    <span v-if="label" class="oa-verify-status" role="status">{{ t(label) }}</span>
  </div>
</template>
