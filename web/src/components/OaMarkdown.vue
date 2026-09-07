<script setup lang="ts">
// Model output, rendered.
//
// `chat/markdown.ts` is carried over untouched, and this is the whole bridge
// to it: a div, and `renderInto` on every change. That renderer builds nodes
// and never assigns innerHTML — which is exactly why it is not replaced with
// `v-html` here. A single string-building path anywhere in the transcript
// would make the rest of the care pointless.
//
// It also means the transcript is not diffed by Vue. That is deliberate:
// `renderInto` already patches in place against the DOM it built last time,
// which is what keeps a streaming answer from rebuilding thirty times a
// second, and handing the same job to the virtual DOM would be two
// reconcilers arguing over one subtree.

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
