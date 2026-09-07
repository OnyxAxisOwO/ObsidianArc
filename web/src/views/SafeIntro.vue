<script setup lang="ts">
// The operator's front page.
//
// The sanitiser hands back a fragment of real nodes, so this appends it
// rather than binding a string: `v-html` here would be the second innerHTML
// assignment in the project and would skip the allowlist entirely.

import { onMounted, ref, watch } from 'vue';
import { safeIntro } from '@/lib/safe-intro';

const props = defineProps<{ html: string }>();

const host = ref<HTMLElement | null>(null);

function paint(): void {
  const node = host.value;
  if (!node) return;
  node.textContent = '';
  node.appendChild(safeIntro(props.html));
}

onMounted(paint);
watch(() => props.html, paint);
</script>

<template>
  <div ref="host" />
</template>
