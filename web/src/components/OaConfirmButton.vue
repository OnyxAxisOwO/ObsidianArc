<script setup lang="ts">
// A button that is its own confirmation.
//
// The first click arms it, the second acts, and it disarms itself after a few
// seconds or as soon as focus leaves.
//
// These used window.confirm, which was the one piece of interface here that
// could not be styled — and which a browser is free to suppress. An embedded
// view, or one where somebody has ticked "prevent this page from creating
// more dialogs", returns false without showing anything, turning a
// destructive button into one that silently does nothing.

import { onBeforeUnmount, ref } from 'vue';

const props = withDefaults(defineProps<{
  /** What the button says at rest, when it is a word rather than an icon. */
  label?: string;
  /** What it says once armed. */
  armedLabel?: string;
  /** The question, shown as the title while armed. */
  armedTitle: string;
  restingTitle?: string;
  disabled?: boolean;
  /** How long an armed button waits before going back to being a safe one. */
  armMs?: number;
}>(), { armMs: 4000 });

const emit = defineEmits<{ (event: 'confirm'): void }>();

const armed = ref(false);
let timer = 0;

function disarm(): void {
  if (!timer) return;
  window.clearTimeout(timer);
  timer = 0;
  armed.value = false;
}

function onClick(event: MouseEvent): void {
  event.stopPropagation();
  if (timer) {
    disarm();
    emit('confirm');
    return;
  }
  armed.value = true;
  timer = window.setTimeout(disarm, props.armMs);
}

onBeforeUnmount(disarm);
</script>

<template>
  <button
    type="button"
    :class="{ armed }"
    :disabled="props.disabled"
    :title="armed ? props.armedTitle : props.restingTitle"
    :aria-label="armed ? props.armedTitle : props.restingTitle"
    @click="onClick"
    @blur="disarm"
  >
    <template v-if="armed">
      <slot name="armed">{{ props.armedLabel ?? props.armedTitle }}</slot>
    </template>
    <template v-else>
      <slot>{{ props.label }}</slot>
    </template>
  </button>
</template>
