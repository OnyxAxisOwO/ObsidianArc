<script setup lang="ts">
// The chat screen, and the column that opens beside it.
//
// /settings, /keys, /usage and /about are child routes of this one rather
// than pages of their own. That is what the hand-written version was doing by
// calling `renderChatPage` at the top of each of those screens and then
// opening a panel over the row it returned — except that it rebuilt the whole
// chat every time. Here the chat stays mounted and the child route is the
// panel, which is both less code and one fewer thing to get wrong.

import { computed, onMounted } from 'vue';
import { useMediaQuery } from '@vueuse/core';
import { useRoute, useRouter } from 'vue-router';
import HomeNotice from '@/announce/HomeNotice.vue';
import VerifyBanner from '@/announce/VerifyBanner.vue';
import ChatSurface from '@/chat/ChatSurface.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import { t } from '@/composables/useI18n';
import { IconMenu } from '@/icons';
import { dragging, historyOpen, isEmpty, refreshList, startNewConversation } from '@/chat/useChat';
import { loadModels, restorePreferences } from '@/chat/useModels';
import { useRailCollapse } from '@/composables/useRailCollapse';
import AppShell from './AppShell.vue';

const router = useRouter();
const route = useRoute();

const narrow = useMediaQuery('(max-width: 900px)');
const rail = useRailCollapse('obsidian-arc-rail-collapsed');

const bodyClass = computed(() => ({
  'ai-chat': true,
  'ai-chat-wide': true,
  'history-open': historyOpen.value,
  'is-empty': isEmpty.value,
  dragging: dragging.value,
  'rail-collapsed': rail.collapsed.value,
}));

/**
 * Two affordances on one button: on a wide screen the rail is a permanent
 * column and this slides it away; below the breakpoint where the rail becomes
 * an overlay, sliding it would do nothing useful, so it defers to the chat's
 * own overlay toggle.
 */
function onRailToggle(): void {
  if (narrow.value) historyOpen.value = !historyOpen.value;
  else rail.toggle();
}

/**
 * The router ignores a navigation whose destination matches the current path.
 * Clicking the brand while already on the chat root resets to an empty
 * conversation rather than doing nothing.
 */
function onBrand(): void {
  if (route.path === '/') startNewConversation();
  else void router.push('/');
}

onMounted(() => {
  restorePreferences();
  void refreshList();
  void loadModels().catch(() => {
    // No models is a state the surface already draws — the setup card — and
    // an unreachable list is that same state as far as this screen goes.
  });
});
</script>

<template>
  <AppShell :body-class="bodyClass" @brand="onBrand">
    <template #leading>
      <OaIconButton class="oa-icon-btn" :label="t('railToggle')" @click="onRailToggle">
        <IconMenu :size="17" />
      </OaIconButton>
    </template>

    <template #banners>
      <!-- Above the chat, not instead of it: this account can still read and
           still change its address, it just cannot send anything yet. -->
      <VerifyBanner />
      <!-- Under the verification strip when both are up: that one is about
           this account and is something to act on, this one is about the
           instance and is something to know. -->
      <HomeNotice />
    </template>

    <ChatSurface @open-setup="router.push('/admin/providers')" />
    <RouterView />
  </AppShell>
</template>
