<script setup lang="ts">
// A fast chat burst is ambiguous: it can be a script, or simply somebody
// working quickly. The challenge pauses exactly that turn and sends it after
// proof instead of discarding the reader's text or punishing the whole account.

import { ref } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t } from '@/composables/useI18n';
import { IconClose, IconLock } from '@/icons';
import { siteInfo } from '@/stores/session';
import {
  cancelChatChallenge, chatChallengeError, completeChatChallenge,
} from './useChat';

const guard = ref<InstanceType<typeof OaTurnstile> | null>(null);
const sending = ref(false);

async function solved(): Promise<void> {
  const token = guard.value?.token() ?? '';
  if (!token || sending.value) return;
  sending.value = true;
  await completeChatChallenge(token);
  sending.value = false;
  guard.value?.reset();
}
</script>

<template>
  <OaOverlay v-slot="{ close }" overlay-class="oa-modal-overlay" @close="cancelChatChallenge">
    <div class="oa-auth-card oa-modal-card">
      <OaIconButton class="oa-icon-btn oa-modal-close" :label="t('close')" @click="close">
        <IconClose :size="16" />
      </OaIconButton>

      <div class="oa-auth-brand">
        <span class="oa-auth-mark"><IconLock :size="15" /></span>
        <span>{{ siteInfo.name }}</span>
      </div>
      <h1 class="oa-auth-title">{{ t('chatChallengeTitle') }}</h1>
      <p class="oa-auth-sub">{{ t('chatChallengeBody') }}</p>

      <div class="oa-auth-form">
        <OaTurnstile
          ref="guard"
          :site-key="siteInfo.turnstile_site_key ?? ''"
          @solved="solved"
        />
        <p v-if="chatChallengeError" class="oa-auth-error" role="alert">
          {{ chatChallengeError }}
        </p>
      </div>
    </div>
  </OaOverlay>
</template>
