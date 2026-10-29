<script setup lang="ts">
import { onMounted, ref, shallowRef } from 'vue';
import type { Account } from '@/api/auth';
import OaAvatar from '@/components/OaAvatar.vue';
import OaMenu from '@/components/OaMenu.vue';
import { t } from '@/composables/useI18n';
import { displayName } from '@/lib/account';
import { feedbackUnread, refreshFeedbackUnread } from '@/stores/feedback';

const props = defineProps<{ account: Account }>();
onMounted(() => void refreshFeedbackUnread());

// The trigger is part of every first paint; the grouped menu is needed only
// when opened. A failed chunk request stays retryable without reloading the chat.
const content = shallowRef<typeof import('./AccountMenuContent.vue')['default'] | null>(null);
const loading = ref(false);
const failed = ref(false);
async function loadContent(): Promise<void> {
  if (content.value || loading.value) return;
  loading.value = true;
  failed.value = false;
  try {
    content.value = (await import('./AccountMenuContent.vue')).default;
  } catch {
    failed.value = true;
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <OaMenu menu-class="oa-menu-account">
    <template #trigger="{ open, toggle }">
      <button
        type="button"
        class="oa-account-btn"
        :class="{ 'has-unread': feedbackUnread }"
        :title="feedbackUnread ? `${t('account')} · ${t('feedbackHasReply')}` : t('account')"
        aria-haspopup="menu"
        :aria-expanded="open ? 'true' : 'false'"
        @click="toggle(); loadContent()"
      >
        <OaAvatar :account="props.account" />
        <!-- A dot rather than a count, for the reason the bell carries one:
             how many answers are waiting is not a number anybody acts on. -->
        <span v-if="feedbackUnread" class="oa-account-dot" />
        <span class="oa-account-name">{{ displayName(props.account) }}</span>
      </button>
    </template>

    <template #default="{ close }">
      <component :is="content" v-if="content" :account="props.account" :close="close" />
      <button v-else-if="failed" type="button" class="oa-menu-item" @click="loadContent">{{ t('retry') }}</button>
      <p v-else class="oa-menu-empty" role="status">{{ t('loading') }}</p>
    </template>
  </OaMenu>
</template>
