<script setup lang="ts">
// The answer while it is still being written.
//
// A component of its own rather than a branch inside the transcript, because
// it is the one thing on screen that changes many times a second: keeping it
// separate is what confines a delta to re-rendering this subtree instead of
// the whole conversation. It reads the pending turn from the store itself
// rather than taking it as a prop: a prop is read by the parent's render, and
// the parent here is the transcript's whole list, which then ran again on
// every frame. The transcript only reads whether to show this at all.
//
// The answer is drawn again in full on every frame that brings text (see
// OaMarkdown), so a selection made in it does not survive the next frame. It is
// whole again once the turn ends and the server's copy takes its place.

import { onBeforeUnmount, ref, watch } from 'vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import { t } from '@/composables/useI18n';
import ChatThinking from './ChatThinking.vue';
import { pending, statsWanted } from './useChat';

/**
 * The seconds ticking up under an answer that is still being written.
 *
 * Ten times a second: fast enough that it reads as running, slow enough that
 * it is not competing with the stream for frames. It stops by this component
 * going away — the turn ends, the server's copy is rendered in its place, and
 * that finished line carries the same elapsed time measured server-side.
 */
const elapsed = ref('0.0');

function tick(): void {
  // The turn's end clears busy and pending one after the other, and a tick can
  // land between the two, while this is still mounted.
  if (!pending.value) return;
  const ms = Math.max(0, Date.now() - pending.value.startedAt);
  elapsed.value = (Math.round(ms / 100) / 10).toFixed(1);
}

// Only while there is a line to write it into. Reading the clock ten times a
// second re-renders this component ten times a second, and doing that for an
// account that has the timing line switched off spends a frame budget on
// nothing — during a stream, which is the one moment that budget is tight.
// It follows the setting, which can be flipped mid-answer: a clock decided once
// at setup would freeze at 0.0s, or keep running after it was turned off.
let timer = 0;
watch(statsWanted, (wanted) => {
  if (timer) window.clearInterval(timer);
  timer = 0;
  if (!wanted) return;
  tick();
  timer = window.setInterval(tick, 100);
}, { immediate: true });

/**
 * A 3×3 matrix lit by a wave running from one corner to the other, so the
 * delay is the cell's distance along that diagonal.
 */
function dotDelay(i: number): string {
  return `${((i % 3) + Math.floor(i / 3)) * 0.12}s`;
}

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer);
});
</script>

<template>
  <div class="ai-msg ai-msg-assistant">
    <ChatThinking v-if="pending?.reasoning" :text="pending.reasoning" live />

    <OaMarkdown
      v-if="pending?.answer"
      class="ai-answer ai-answer-streaming"
      :text="pending.answer"
    />
    <!-- Still nothing to read, whether or not it is thinking out loud. -->
    <div v-else class="ai-chat-pending">
      <span class="ai-dots" aria-hidden="true">
        <i v-for="n in 9" :key="n" :style="{ '--d': dotDelay(n - 1) }" />
      </span>
      <span class="ai-shimmer">{{ t('thinking') }}</span>
    </div>

    <!-- Under the answer, and under the spinner before there is one: the clock
         is running either way, and the wait before the first token is the part
         of it worth watching. -->
    <div v-if="statsWanted" class="ai-msg-stats ai-msg-timer">{{ elapsed }}s</div>
  </div>
</template>
