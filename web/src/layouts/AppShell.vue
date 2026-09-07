<script setup lang="ts">
// The application chrome: the header bar, plus the account menu.
//
// Every signed-in screen is drawn inside this, so the brand, the theme toggle
// and the account button sit in the same place whether the user is chatting,
// in settings, or in the backoffice.
//
// It also provides the panel host. The body element below is the flex row a
// side panel becomes a column of, and providing it here is what lets a screen
// five components deep open one without being handed an element to put it in.

import { ref, type HTMLAttributes } from 'vue';
import AnnounceBell from '@/announce/AnnounceBell.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { providePanelHost } from '@/composables/usePanelHost';
import { t } from '@/composables/useI18n';
import { currentUser, siteInfo } from '@/stores/session';
import AccountMenu from './AccountMenu.vue';

const props = defineProps<{
  /** What the row is: `ai-chat ai-chat-wide` for the chat, `oa-admin` for the backoffice. */
  bodyClass?: HTMLAttributes['class'];
}>();

const emit = defineEmits<{ (event: 'brand'): void }>();

/**
 * The brand stays an ordinary link.
 *
 * It has to: a button would not open in a new tab, would not show its
 * destination on hover and would not be copyable. So the modifier rules live
 * here, once, rather than in each of the three screens that host this header
 * — a plain left click is claimed and handed to the host, and everything else
 * is left to the browser to do what the reader asked.
 */
function onBrand(event: MouseEvent): void {
  if (event.defaultPrevented || event.button !== 0) return;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  emit('brand');
}

const body = ref<HTMLElement | null>(null);
providePanelHost(body);

/**
 * Set once and never cleared.
 *
 * Vue nulls a template ref while the tree is being torn down, and a panel
 * teleported into this element reads it: clearing it mid-unmount would flip
 * the panel's own `v-if` and schedule a render on a component whose setup
 * state has already gone. The element is dropped with the component either
 * way, so holding the last one costs nothing.
 */
function keepBody(node: unknown): void {
  if (node instanceof HTMLElement) body.value = node;
}

defineExpose({ body });
</script>

<template>
  <div class="oa-workspace">
    <div class="oa-header">
      <span class="oa-header-leading"><slot name="leading" /></span>
      <a class="oa-brand" href="/" :title="t('backToChat')" @click="onBrand">
        {{ siteInfo.name }}
      </a>
      <span class="oa-header-spacer" />
      <span class="oa-header-slot"><slot name="header" /></span>
      <!-- Only for somebody who has an account to have announcements read
           against; the sign-in page has its own corner. -->
      <AnnounceBell v-if="currentUser" />
      <OaThemeToggle />
      <AccountMenu v-if="currentUser" :account="currentUser" />
    </div>

    <slot name="banners" />

    <div :ref="keepBody" class="oa-chat-root" :class="props.bodyClass">
      <slot />
    </div>
  </div>
</template>
