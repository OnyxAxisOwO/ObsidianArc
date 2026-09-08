<script setup lang="ts">
import { useRouter } from 'vue-router';
import { logout, type Account } from '@/api/auth';
import OaAvatar from '@/components/OaAvatar.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import { t } from '@/composables/useI18n';
import { IconChart, IconGear, IconImage, IconInfo, IconKey, IconLogout, IconSliders } from '@/icons';
import { displayName } from '@/lib/account';
import { forget } from '@/stores/session';

const props = defineProps<{ account: Account }>();

const router = useRouter();

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
  await router.replace('/login');
}
</script>

<template>
  <OaMenu>
    <template #trigger="{ open, toggle }">
      <button
        type="button"
        class="oa-account-btn"
        :title="t('account')"
        aria-haspopup="menu"
        :aria-expanded="open ? 'true' : 'false'"
        @click="toggle"
      >
        <OaAvatar :account="props.account" />
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
        <span v-if="props.account.role === 'admin'" class="oa-badge oa-badge-muted">
          {{ t('admin') }}
        </span>
      </div>

      <OaMenuItem :title="t('settings')" @click="go(close, '/settings')">
        <template #leading><IconGear :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('imageLab')" @click="go(close, '/image-lab')">
        <template #leading><IconImage :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('navUsage')" @click="go(close, '/usage')">
        <template #leading><IconChart :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('apiKeys')" @click="go(close, '/keys')">
        <template #leading><IconKey :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('about')" @click="go(close, '/about')">
        <template #leading><IconInfo :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem
        v-if="props.account.role === 'admin'"
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
