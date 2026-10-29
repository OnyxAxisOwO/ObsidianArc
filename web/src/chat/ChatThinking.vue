<script setup lang="ts">
// What the model said to itself on the way to the answer.
//
// Open while it is being written and closed once it is done: it is worth
// watching arrive and is rarely worth re-reading, and a transcript of ten
// answers each with their reasoning expanded is unreadable.

import { nextTick, ref, watch } from 'vue';
import { t } from '@/composables/useI18n';

const props = defineProps<{
  text: string;
  live?: boolean;
  initialOpen?: boolean;
}>();

const isOpen = ref(!!props.live || !!props.initialOpen);
const body = ref<HTMLElement | null>(null);

watch(() => props.live, (live) => {
  if (live) isOpen.value = true;
});

// Follows the tail while it streams, but only from the tail: somebody who has
// scrolled up inside the box is reading, and yanking them back is worse than
// letting the new text run off the bottom.
watch(() => props.text, () => {
  if (!props.live) return;
  const node = body.value;
  if (!node) return;
  const atEnd = node.scrollHeight - node.scrollTop - node.clientHeight < 24;
  if (atEnd) void nextTick(() => { node.scrollTop = node.scrollHeight; });
}, { immediate: true });

function toggle(): void {
  isOpen.value = !isOpen.value;
}
</script>

<template>
  <div class="ai-thinking" :class="{ open: isOpen }">
    <button
      type="button"
      class="ai-thinking-head"
      :aria-expanded="isOpen"
      @click="toggle"
    >
      <span :class="{ 'ai-shimmer': props.live }">{{ t(props.live ? 'reasoningLive' : 'reasoning') }}</span>
    </button>
    <div class="ai-thinking-content">
      <div ref="body" class="ai-thinking-body">{{ props.text }}</div>
    </div>
  </div>
</template>
