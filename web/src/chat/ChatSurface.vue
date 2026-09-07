<script setup lang="ts">
// The chat surface: the rail beside the transcript, the composer under it.
//
// Both are columns of the row this is rendered into, which is also where a
// side panel arrives — so /settings and /keys narrow the conversation rather
// than covering it.

import { nextTick, ref, watch } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import { t } from '@/composables/useI18n';
import { IconMenu, IconPlus } from '@/icons';
import ChatComposer from './ChatComposer.vue';
import ChatMessage from './ChatMessage.vue';
import ChatPending from './ChatPending.vue';
import ChatSidebar from './ChatSidebar.vue';
import {
  active, addImages, busy, dragging, draft, flash, historyOpen, messages, pending,
  scrollTick, startNewConversation, status, submit, suggestions, switchTick,
} from './useChat';
import { isAdmin } from '@/stores/session';

const emit = defineEmits<{ (event: 'open-setup'): void }>();

const scroll = ref<InstanceType<typeof OaScrollArea> | null>(null);
const composer = ref<InstanceType<typeof ChatComposer> | null>(null);
const rising = ref(false);

function scroller(): HTMLElement | null {
  return scroll.value?.scroller ?? null;
}

/**
 * Only auto-scroll when the reader is already at the bottom: yanking the view
 * back while somebody is reading an earlier part of the answer is worse than
 * letting it run off screen.
 */
watch(scrollTick, () => {
  const node = scroller();
  if (!node) return;
  const atEnd = node.scrollHeight - node.scrollTop - node.clientHeight < 40;
  if (!atEnd && busy.value) return;
  void nextTick(() => { node.scrollTop = node.scrollHeight; });
});

// A short rise says "a different conversation" instead of leaving the
// transcript to flicker into something else within one frame.
watch(switchTick, () => {
  rising.value = false;
  void nextTick(() => {
    rising.value = true;
    window.setTimeout(() => { rising.value = false; }, 400);
  });
});

function carriesFiles(event: DragEvent): boolean {
  if (!status.value.vision) return false;
  const types = event.dataTransfer?.types;
  return !!types && Array.prototype.indexOf.call(types, 'Files') !== -1;
}

function onDragOver(event: DragEvent): void {
  if (!carriesFiles(event)) return;
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
  dragging.value = true;
}

function onDragLeave(event: DragEvent): void {
  const main = event.currentTarget as HTMLElement;
  if (event.target === main || !main.contains(event.relatedTarget as Node | null)) {
    dragging.value = false;
  }
}

function onDrop(event: DragEvent): void {
  if (!carriesFiles(event)) return;
  event.preventDefault();
  dragging.value = false;
  void addImages(event.dataTransfer?.files ?? null);
}

function ask(key: (typeof suggestions.value)[number]): void {
  draft.value = t(key);
  void submit();
}

defineExpose({ focus: () => composer.value?.focus() });
</script>

<template>
  <ChatSidebar />

  <div class="ai-chat-main" @dragenter="onDragOver" @dragover="onDragOver" @dragleave="onDragLeave" @drop="onDrop">
    <!-- The wide skin hides this bar in favour of the workspace header; it is
         the navigation on a narrow screen, where the rail is an overlay. -->
    <div class="ai-chat-bar">
      <OaIconButton
        class="ai-chat-bar-btn"
        :label="t('history')"
        @click="historyOpen = !historyOpen"
      ><IconMenu :size="16" /></OaIconButton>
      <span class="ai-chat-bar-title">{{ active?.title || t('brand') }}</span>
      <OaIconButton class="ai-chat-bar-btn" :label="t('newChat')" @click="startNewConversation">
        <IconPlus :size="15" />
      </OaIconButton>
    </div>

    <OaScrollArea
      ref="scroll"
      wrap-class="ai-chat-scroll-wrap"
      :scroll-class="rising ? 'ai-chat-scroll ai-chat-switching' : 'ai-chat-scroll'"
    >
      <div v-if="!status.configured" class="ai-chat-setup">
        <h3 class="ai-chat-setup-title">{{ t('setupTitle') }}</h3>
        <p class="ai-chat-setup-body">{{ isAdmin ? t('setupBody') : t('setupBodyUser') }}</p>
        <button
          v-if="isAdmin"
          type="button"
          class="ai-chat-setup-action"
          @click="emit('open-setup')"
        >{{ t('setupAction') }}</button>
      </div>

      <ChatMessage v-for="message in messages" :key="message.id" :message="message" />

      <div v-if="!messages.length && status.configured" class="ai-chat-empty">
        <h3 class="ai-chat-empty-title">{{ t('emptyTitle') }}</h3>
        <p class="ai-chat-empty-body">{{ t('emptyBody') }}</p>
        <div class="ai-chat-suggestions">
          <button
            v-for="key in suggestions"
            :key="key"
            type="button"
            class="ai-chat-suggestion"
            @click="ask(key)"
          >{{ t(key) }}</button>
        </div>
      </div>

      <ChatPending v-if="busy && pending" :pending="pending" />
    </OaScrollArea>

    <div class="ai-chat-flash" :class="{ visible: !!flash }" role="status" aria-live="polite">
      {{ flash }}
    </div>

    <ChatComposer ref="composer" />

    <div class="ai-chat-drop">{{ t('dropHint') }}</div>
  </div>
</template>
