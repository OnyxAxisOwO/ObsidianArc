<script setup lang="ts">
// Model output, rendered.
//
// `chat/markdown.ts` is carried over untouched, and this is the whole bridge
// to it: a div, and `renderInto` on every change. That renderer builds nodes
// and never assigns innerHTML — which is exactly why it is not replaced with
// `v-html` here. A single string-building path anywhere in the transcript
// would make the rest of the care pointless.
//
// It also means the transcript is not diffed by Vue, which is deliberate:
// handing the same subtree to the virtual DOM would be two reconcilers
// arguing over one tree. What it does not mean is that the update is cheap.
// `renderInto` replaces the subtree outright — it re-parses the whole answer
// and rebuilds every node, so this component repaints all of it on every
// change. That is affordable only because the store coalesces deltas into one
// paint per animation frame (`chat/useChat.ts`); without that bound this is
// per-token work over a growing answer. Patching in place instead was
// considered and rejected: a diff of a token tree whose tail is by definition
// half-written would put the escaping boundary this renderer exists to own
// into the hardest code in the file.

import { onMounted, ref, watch } from 'vue';
import { renderInto } from '@/chat/markdown';

const props = defineProps<{ text: string }>();

const host = ref<HTMLElement | null>(null);

function paint(): void {
  if (host.value) renderInto(host.value, props.text);
}

onMounted(paint);
watch(() => props.text, paint);

defineExpose({ host, paint });
</script>

<template>
  <div ref="host" />
</template>
