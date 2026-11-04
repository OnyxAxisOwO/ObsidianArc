<script setup lang="ts">
// One scrollback block, rendered as spans.
//
// `block.tokens` never carries a `kind: 'clear'` entry — `session.ts`
// intercepts those itself and wipes the scrollback instead of pushing a
// block — so every token here is text, and this component never needs to
// special-case ANSI's screen-clear codes.

import type { ScrollbackBlock } from './session';

const props = defineProps<{
  block: ScrollbackBlock;
  /** A dim clock next to the line, for `echo` blocks only. */
  timestamps: boolean;
}>();

function formatClock(ms: number): string {
  const at = new Date(ms);
  const two = (n: number): string => String(n).padStart(2, '0');
  return `${two(at.getHours())}:${two(at.getMinutes())}:${two(at.getSeconds())}`;
}
</script>

<template>
  <div class="oa-terminal-block" :class="`oa-terminal-block-${props.block.kind}`">
    <span v-if="props.timestamps && props.block.kind === 'echo'" class="oa-terminal-timestamp">{{ formatClock(props.block.timestamp) }}</span>
    <span v-if="props.block.kind === 'echo'" class="oa-terminal-prompt-glyph" aria-hidden="true">&gt;</span>
    <!-- No innerHTML/v-html anywhere: every span comes from the ANSI
         tokeniser's own class list, and every span's content is a Vue text
         interpolation, which escapes what it prints. -->
    <span class="oa-terminal-text"><span
      v-for="(token, index) in props.block.tokens"
      :key="index"
      :class="token.cls"
    >{{ token.text }}</span></span>
  </div>
</template>
