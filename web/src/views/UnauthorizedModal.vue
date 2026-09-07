<script setup lang="ts">
// The backoffice, refused.
//
// The same mark, title and switcher as the sign-in card: this is the same
// door, answering differently.

import { nextTick, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { logout } from '@/api/auth';
import OaIconButton from '@/components/OaIconButton.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import { t } from '@/composables/useI18n';
import { IconClose, IconLock } from '@/icons';
import { forget, siteInfo } from '@/stores/session';

const router = useRouter();
const back = ref<HTMLButtonElement | null>(null);
/** Set when the way out is somewhere other than the chat behind this. */
let leaving = false;

onMounted(() => void nextTick(() => back.value?.focus()));

function onClosed(): void {
  if (leaving) return;
  void router.replace('/');
}

function switchAccount(close: () => void): void {
  leaving = true;
  void logout().finally(() => {
    forget();
    close();
    void router.replace('/login');
  });
}
</script>

<template>
  <OaOverlay v-slot="{ close }" overlay-class="oa-modal-overlay" @close="onClosed">
    <div class="oa-auth-card oa-modal-card">
      <OaIconButton class="oa-icon-btn oa-modal-close" :label="t('close')" @click="close">
        <IconClose :size="16" />
      </OaIconButton>

      <div class="oa-auth-brand">
        <span class="oa-auth-mark"><IconLock :size="15" /></span>
        <span>{{ siteInfo.name }}</span>
      </div>

      <h1 class="oa-auth-title">{{ t('accessDenied') }}</h1>
      <p class="oa-auth-sub">{{ t('noAdminAccess') }}</p>

      <div class="oa-auth-form">
        <button ref="back" type="button" class="oa-btn primary oa-btn-block" @click="close">
          {{ t('backToChat') }}
        </button>
      </div>

      <p class="oa-auth-switch">
        <span>{{ t('needDifferentAccount') }}</span>
        <button type="button" @click="switchAccount(close)">{{ t('switchAccount') }}</button>
      </p>
    </div>
  </OaOverlay>
</template>
