<script setup lang="ts">
// A sheet over the page.
//
// For the two things here that are genuinely modal: a refusal the reader has
// to acknowledge, and an announcement shown unprompted. Everything else in
// this interface is a column rather than a sheet, on purpose.
//
// It teleports to <body> so no ancestor's transform or backdrop-filter can
// trap it, and it fades itself in and out — `close` is emitted after the
// fade, so the caller's `v-if` does not cut the animation off.

import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useEventListener } from '@vueuse/core';
import { closeModal, isTopModal, openModal } from '@/lib/modal-stack';

const props = withDefaults(defineProps<{
  overlayClass: string;
  /** False while a countdown is still running on the dismiss button. */
  dismissible?: boolean;
  /** The sheet's name for a screen reader arriving on it. */
  label?: string | undefined;
}>(), { dismissible: true });

const emit = defineEmits<{ (event: 'close'): void }>();

const root = ref<HTMLElement | null>(null);
const shown = ref(false);
let closing = false;
let closeTimer = 0;
let pressedOnBackdrop = false;
// Read now, before anything inside the sheet takes focus, so it can be given
// back when the sheet goes.
const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;

const token = {};
openModal(token);

const FOCUSABLE = [
  'a[href]', 'button:not([disabled])', 'input:not([disabled])', 'select:not([disabled])',
  'textarea:not([disabled])', '[tabindex]:not([tabindex="-1"])',
].join(', ');

function focusables(): HTMLElement[] {
  return root.value ? [...root.value.querySelectorAll<HTMLElement>(FOCUSABLE)] : [];
}

// A field that focuses itself on mount has already taken focus; the sheet
// only moves it in when nothing inside has it.
function focusInside(): void {
  const el = root.value;
  if (!el || el.contains(document.activeElement)) return;
  focusables()[0]?.focus({ preventScroll: true });
}

onMounted(() => requestAnimationFrame(() => {
  shown.value = true;
  focusInside();
}));

function close(): void {
  if (closing || !props.dismissible) return;
  closing = true;
  shown.value = false;
  closeTimer = window.setTimeout(() => emit('close'), 200);
}

// Tab cycles inside the sheet. Left alone, the next Tab from the last control
// goes to the page behind the scrim, where a screen-reader user is acting on
// controls they cannot see.
function trapTab(event: KeyboardEvent): void {
  if (closing || !root.value) return;
  const items = focusables();
  const first = items[0];
  const last = items[items.length - 1];
  if (!first || !last) return;
  const inside = root.value.contains(document.activeElement);
  const wrapToLast = event.shiftKey && (!inside || document.activeElement === first);
  const wrapToFirst = !event.shiftKey && (!inside || document.activeElement === last);
  if (!wrapToLast && !wrapToFirst) return;
  event.preventDefault();
  (wrapToLast ? last : first).focus({ preventScroll: true });
}

// Only the sheet on top answers Escape, and it says so with preventDefault:
// the panel beneath checks for that before closing, and a panel listens on the
// same document. A key something inside already handled (an open select) is
// left alone.
useEventListener(document, 'keydown', (event: KeyboardEvent) => {
  // Only the top sheet traps Tab. A sheet beneath would see focus outside itself
  // and pull it to its own first control, and the top sheet would then pull it
  // back, so Tab from anywhere in the top sheet never reached its next control.
  if (event.key === 'Tab') {
    if (isTopModal(token)) trapTab(event);
    return;
  }
  if (event.key !== 'Escape' || event.defaultPrevented) return;
  if (!isTopModal(token)) return;
  event.preventDefault();
  close();
});

// A click goes to the nearest element that holds both the press and the
// release. A drag that starts inside the card and ends on the scrim is
// therefore a click on the overlay, and the sheet closed under the reader's
// selection; only a press that began on the scrim may close it.
function onPress(event: PointerEvent): void {
  pressedOnBackdrop = event.target === event.currentTarget;
}

function onBackdropClick(event: MouseEvent): void {
  if (pressedOnBackdrop && event.target === event.currentTarget) close();
  pressedOnBackdrop = false;
}

// A sheet taken away during its fade leaves nothing running. Vue already drops
// the late close for an unmounted component, but the timer itself would outlive
// the sheet, and the close it carries is usually a navigation.
onBeforeUnmount(() => {
  closing = true;
  window.clearTimeout(closeTimer);
  closeModal(token);
  // Focus that was inside the sheet would otherwise drop to the body, and the
  // keyboard reader would start again from the top of the page. Focus that has
  // already moved on, to a field an action opened during the fade, stays there.
  const focusWasInside = !!root.value && root.value.contains(document.activeElement);
  const focusLost = !document.activeElement || document.activeElement === document.body;
  if ((focusWasInside || focusLost) && opener?.isConnected && opener !== document.body) {
    opener.focus({ preventScroll: true });
  }
});

defineExpose({ close });
</script>

<template>
  <Teleport to="body">
    <div
      ref="root"
      :class="[props.overlayClass, { open: shown }]"
      role="dialog"
      aria-modal="true"
      :aria-label="props.label"
      @pointerdown="onPress"
      @click="onBackdropClick"
    >
      <slot :close="close" />
    </div>
  </Teleport>
</template>
