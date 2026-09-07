<script setup lang="ts">
// The answer while it is still being written.
//
// A component of its own rather than a branch inside the transcript, because
// it is the one thing on screen that changes many times a second: keeping it
// separate is what confines a delta to re-rendering this subtree instead of
// the whole conversation.

import { onBeforeUnmount, ref } from 'vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import { t } from '@/composables/useI18n';
import ChatThinking from './ChatThinking.vue';
import { statsWanted, type Pending } from './useChat';

const props = defineProps<{ pending: Pending }>();

/**
 * The seconds ticking up under an answer that is still being written.
 *
 * Ten times a second: fast enough that it reads as running, slow enough that
 * it is not competing with the stream for frames. It stops by this component
 * going away — the turn ends, the server's copy is rendered in its place, and
 * that finished line carries the same elapsed time measured server-side.
 */
const elapsed = ref('0.0');

// Only when there is a line to write it into. Reading the clock ten times a
// second re-renders this component ten times a second, and doing that for an
// account that has the timing line switched off spends a frame budget on
// nothing — during a stream, which is the one moment that budget is tight.
const timer = statsWanted.value
  ? window.setInterval(() => {
      const ms = Math.max(0, Date.now() - props.pending.startedAt);
      elapsed.value = (Math.round(ms / 100) / 10).toFixed(1);
    }, 100)
  : 0;

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer);
});
</script>

<template>
  <div class="ai-msg ai-msg-assistant">
    <ChatThinking v-if="props.pending.reasoning" :text="props.pending.reasoning" live />

    <OaMarkdown
      v-if="props.pending.answer"
      class="ai-answer ai-answer-streaming"
      :text="props.pending.answer"
    />
    <!-- Still nothing to read, whether or not it is thinking out loud. -->
    <div v-else class="ai-chat-pending">
      <span class="ai-chat-spinner" />
      <span>{{ t('thinking') }}</span>
    </div>

    <!-- Under the answer, and under the spinner before there is one: the clock
         is running either way, and the wait before the first token is the part
         of it worth watching. -->
    <div v-if="statsWanted" class="ai-msg-stats ai-msg-timer">{{ elapsed }}s</div>
  </div>
</template>
