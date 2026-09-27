<script setup lang="ts">
// The chat surface: the rail beside the transcript, the composer under it.
//
// Both are columns of the row this is rendered into, which is also where a
// side panel arrives — so /settings and /keys narrow the conversation rather
// than covering it.

import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useResizeObserver } from '@vueuse/core';
import OaIconButton from '@/components/OaIconButton.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { isWork, pendingMode } from '@/stores/workspace';
import { IconMenu, IconPlus } from '@/icons';
import ChatComposer from './ChatComposer.vue';
import ChatChallenge from './ChatChallenge.vue';
import ChatMessage from './ChatMessage.vue';
import ChatModeSwitch from './ChatModeSwitch.vue';
import ChatPending from './ChatPending.vue';
import ChatSidebar from './ChatSidebar.vue';
import {
  active, addImages, busy, chatChallenge, dragging, draft, flash, historyOpen, isEmpty, justSentID, messages, pending,
  scrollTick, showPending, startNewConversation, status, submit, suggestions, switchTick,
} from './useChat';
import {
  cancelAllFlights, captureComposerRect, lastComposerRect, markFlying, playSendAnimation,
} from './useSendAnimation';
import { currentUser, isAdmin } from '@/stores/session';

let route: ReturnType<typeof useRoute> | undefined;
try {
  route = useRoute();
} catch {
  // outside router context in tests
}
const isTerminal = computed(() => route?.path === '/terminal');

const emit = defineEmits<{ (event: 'open-setup'): void }>();

const mainRef = ref<HTMLElement | null>(null);
const scroll = ref<InstanceType<typeof OaScrollArea> | null>(null);
const composer = ref<InstanceType<typeof ChatComposer> | null>(null);
const transcriptRef = ref<HTMLElement | null>(null);
const autoScroll = ref(true);
const rising = ref(false);

function scroller(): HTMLElement | null {
  return scroll.value?.scroller ?? null;
}

function scrollToBottom(): void {
  const node = scroller();
  if (!node) return;
  node.scrollTop = node.scrollHeight;
}

// Wired as a plain `@scroll`, never `@scroll.passive`: on a component the
// modifier becomes part of the listener's name, emit never finds it, and this
// silently never runs — which leaves the view following the answer no matter
// where the reader has scrolled. The scroll area's own listener is already
// passive; that is the one the browser cares about.
function onScroll(): void {
  const node = scroller();
  if (!node) return;
  autoScroll.value = node.scrollHeight - node.scrollTop - node.clientHeight < 60;
  if (!autoScroll.value) {
    cancelAllFlights();
  }
}

// Auto-follow when content grows (streaming deltas, MathML equations upgraded, images loaded).
useResizeObserver(transcriptRef, () => {
  if (autoScroll.value) {
    scrollToBottom();
  }
});

/**
 * Auto-scroll when the reader is following at the bottom or when
 * completing a turn: yanking the view back while somebody is reading an earlier
 * part of the answer is avoided.
 */
watch(scrollTick, () => {
  if (autoScroll.value || !busy.value) {
    void nextTick(scrollToBottom);
  }
});

// A short rise says "a different conversation" instead of leaving the
// transcript to flicker into something else within one frame.
watch(switchTick, () => {
  cancelAllFlights();
  autoScroll.value = true;
  rising.value = false;
  void nextTick(() => {
    scrollToBottom();
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
  captureComposerRect(composer.value?.getInputRect?.() ?? (composer.value?.$el as HTMLElement | undefined));
  draft.value = t(key);
  void submit();
}

/**
 * The empty conversation greets the person by the part of the day it is and
 * the name they chose. A function rather than a computed, so it is worked out
 * each time the greeting is drawn instead of cached: a tab left open over
 * lunch should not still say good morning the next time it is looked at.
 */
function greeting(): string {
  const hour = new Date().getHours();
  const part: StringKey = hour < 5 ? 'greetNight' : hour < 11 ? 'greetMorning' : hour < 13 ? 'greetNoon'
    : hour < 18 ? 'greetAfternoon' : hour < 23 ? 'greetEvening' : 'greetNight';
  const name = currentUser.value?.nickname || currentUser.value?.username;
  return name ? t('greetNamed', { greeting: t(part), name }) : t('emptyTitle');
}

// The first message sends the composer from the middle of the empty column
// down to the bottom, where a conversation keeps it. It glides rather than
// jumps: the reader was looking straight at it, and a control that vanishes
// from under the eye and reappears elsewhere reads as a glitch. Measured
// before the layout changes and animated from there — the change itself is a
// class on the row, which no transition can interpolate.
watch(isEmpty, (empty, was) => {
  const node = composer.value?.$el as HTMLElement | undefined;
  if (empty || !was || !node || typeof node.animate !== 'function') return;
  if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;
  const before = node.getBoundingClientRect().top;
  void nextTick(() => {
    const delta = before - node.getBoundingClientRect().top;
    if (Math.abs(delta) < 2) return;
    node.animate(
      [{ transform: `translateY(${delta}px)` }, { transform: 'none' }],
      { duration: 340, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
    );
  });
}, { flush: 'pre' });

// Ensure message is marked flying and composer rect is recorded before DOM layout shifts (in flush: 'pre')
watch(justSentID, (id) => {
  if (id) {
    autoScroll.value = true;
    markFlying(id);
    if (!lastComposerRect.value) {
      captureComposerRect(composer.value?.getInputRect?.() ?? (composer.value?.$el as HTMLElement | undefined));
    }
  }
}, { flush: 'pre' });

// Play Telegram-style send flight animation: bubble emerges from composer,
// scales down, and smoothly moves to its target position in the transcript.
watch(justSentID, (id) => {
  if (!id) return;
  const mainEl = mainRef.value;
  const transcriptEl = transcriptRef.value;
  if (!mainEl || !transcriptEl) return;

  void playSendAnimation({
    messageID: id,
    mainEl,
    transcriptEl,
    composerInputEl: (composer.value?.inputElement as HTMLElement | undefined) ?? (composer.value?.$el as HTMLElement | undefined) ?? null,
    scrollToBottom,
  });
});

onBeforeUnmount(() => {
  cancelAllFlights();
});

defineExpose({ focus: () => composer.value?.focus() });
</script>

<template>
  <ChatSidebar v-if="!isTerminal" />

  <div
    ref="mainRef"
    v-show="!isTerminal"
    class="ai-chat-main"
    @dragenter="onDragOver"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDrop"
  >
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
      @scroll="onScroll"
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

      <div ref="transcriptRef" class="ai-chat-transcript">
        <ChatMessage v-for="message in messages" :key="message.id" :message="message" />
        <ChatPending v-if="showPending && pending" :pending="pending" />
      </div>

      <div v-if="!messages.length && status.configured" class="ai-chat-empty">
        <!-- Only on the empty state, because that is the only moment the
             choice is still open: once a conversation exists it carries its
             own mode, and a toggle over a running thread would offer to
             change something it cannot. Its own component so the drag's window
             listeners are torn down when this v-if drops it — see the file. -->
        <ChatModeSwitch />

        <div class="ai-chat-empty-intro-wrap">
          <Transition name="ai-mode-text" mode="out-in">
            <div :key="pendingMode" class="ai-chat-empty-intro">
              <h3 class="ai-chat-empty-title">{{ isWork ? t('workGreeting') : greeting() }}</h3>
              <p class="ai-chat-empty-body">{{ isWork ? t('workBlurb') : t('emptyBody') }}</p>
            </div>
          </Transition>
        </div>

        <div class="ai-chat-suggestions-accordion" :class="{ open: !isWork }">
          <div class="ai-chat-suggestions-inner">
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
        </div>
      </div>
    </OaScrollArea>

    <div class="ai-chat-flash" :class="{ visible: !!flash }" role="status" aria-live="polite">
      {{ flash }}
    </div>

    <ChatComposer ref="composer" />

    <div class="ai-chat-drop">{{ t('dropHint') }}</div>
  </div>

  <ChatChallenge v-if="chatChallenge" />
</template>
