<script setup lang="ts">
import { onMounted } from 'vue';
import { useRouter } from 'vue-router';
import { logout, type Account } from '@/api/auth';
import OaAvatar from '@/components/OaAvatar.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import { t } from '@/composables/useI18n';
import { IconArchive, IconChart, IconGear, IconImage, IconInfo, IconKey, IconLogout, IconMessage, IconPulse, IconSliders, IconTerminal, IconTrophy } from '@/icons';
import { displayName } from '@/lib/account';
import { feedbackUnread, forgetFeedbackUnread, refreshFeedbackUnread } from '@/stores/feedback';
import { forget, siteInfo, isAdmin, canAdmin } from '@/stores/session';

const props = defineProps<{ account: Account }>();

const router = useRouter();

// Once per page load, like the bell beside it. An answer that arrives while
// somebody is sitting on the page is found the next time they move, which is
// the same bargain every other unread mark here makes.
onMounted(() => void refreshFeedbackUnread());

function go(close: () => void, path: string): void {
  close();
  void router.push(path);
}

async function signOut(close: () => void): Promise<void> {
  close();
  try {
    await logout();
  } catch {
    // The cookie may already be gone. Either way the local state goes.
  }
  forget();
  forgetFeedbackUnread();
  await router.replace('/login');
}
</script>

<template>
  <OaMenu menu-class="oa-menu-account">
    <template #trigger="{ open, toggle }">
      <button
        type="button"
        class="oa-account-btn"
        :title="feedbackUnread ? `${t('account')} · ${t('feedbackHasReply')}` : t('account')"
        aria-haspopup="menu"
        :aria-expanded="open ? 'true' : 'false'"
        @click="toggle"
      >
        <OaAvatar :account="props.account" />
        <!-- A dot rather than a count, for the reason the bell carries one:
             how many answers are waiting is not a number anybody acts on. -->
        <span v-if="feedbackUnread" class="oa-account-dot" />
        <span class="oa-account-name">{{ displayName(props.account) }}</span>
      </button>
    </template>

    <template #default="{ close }">
      <div class="oa-menu-head">
        <OaAvatar :account="props.account" large />
        <div class="oa-menu-head-text">
          <span class="oa-menu-head-name">{{ displayName(props.account) }}</span>
          <span class="oa-menu-head-sub">
            {{ props.account.email || `@${props.account.username}` }}
          </span>
        </div>
      </div>

      <div
        v-if="props.account.group_name"
        class="oa-menu-head"
        style="border-bottom: none; padding-top: 0"
      >
        <span class="oa-badge">{{ props.account.group_name }}</span>
        <span v-if="isAdmin" class="oa-badge oa-badge-muted">
          {{ t(props.account.role === 'super_admin' ? 'superAdmin' : 'admin') }}
        </span>
      </div>

      <OaMenuItem :title="t('settings')" @click="go(close, '/settings')">
        <template #leading><IconGear :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('navUsage')" @click="go(close, '/usage')">
        <template #leading><IconChart :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('archivedConversations')" @click="go(close, '/archive')">
        <template #leading><IconArchive :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('imageLab')" @click="go(close, '/image-lab')">
        <template #leading><IconImage :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem
        v-if="canAdmin('availability') || siteInfo.health_show_users"
        :title="t('uptimeTitle')"
        @click="go(close, '/uptime')"
      >
        <template #leading><IconPulse :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem
        v-if="canAdmin('leaderboard') || siteInfo.leaderboard_show_users"
        :title="t('leaderboardTitle')"
        @click="go(close, '/leaderboard')"
      >
        <template #leading><IconTrophy :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('apiKeys')" @click="go(close, '/keys')">
        <template #leading><IconKey :size="14" /></template>
      </OaMenuItem>
      <!-- Beside the keys, the other thing here for somebody who works from a
           keyboard. Hidden only when the group has it switched off; a server
           too old to say is not a refusal. -->
      <OaMenuItem
        v-if="props.account.allow_terminal !== false"
        :title="t('navTerminal')"
        @click="go(close, '/terminal')"
      >
        <template #leading><IconTerminal :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('feedback')" @click="go(close, '/feedback')">
        <template #leading><IconMessage :size="14" /></template>
        <template v-if="feedbackUnread" #trailing>
          <span class="oa-menu-unread" :title="t('feedbackHasReply')" />
        </template>
      </OaMenuItem>
      <OaMenuItem :title="t('about')" @click="go(close, '/about')">
        <template #leading><IconInfo :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem
        v-if="isAdmin"
        :title="t('administration')"
        @click="go(close, '/admin')"
      >
        <template #leading><IconSliders :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('signOut')" @click="signOut(close)">
        <template #leading><IconLogout :size="14" /></template>
      </OaMenuItem>
    </template>
  </OaMenu>
</template>
