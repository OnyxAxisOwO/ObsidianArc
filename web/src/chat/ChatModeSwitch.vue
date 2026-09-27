<script setup lang="ts">
// The chat/work toggle, and the gesture that drags its pill across.
//
// A component of its own, not a block inside ChatSurface, for one reason: the
// switch is shown only on the empty state and dropped by v-if the instant a
// conversation fills the transcript. The drag hangs pointermove/up listeners
// on window, and those must come off when the switch goes away. Living here,
// this component's onBeforeUnmount fires exactly then — a hook on the parent
// ChatSurface, which stays mounted throughout, never would.

import { onBeforeUnmount, ref } from 'vue';
import { t } from '@/composables/useI18n';
import { pendingMode, setMode, type Mode } from '@/stores/workspace';

// The two surfaces, in the order they are offered. A list rather than two
// hand-written buttons so the strip cannot drift out of step with the store
// that holds which one is chosen.
const MODES: Mode[] = ['chat', 'work'];

const strip = ref<HTMLElement | null>(null);
const dragging = ref(false);
const dragX = ref(0);
// Set on a release that was a real drag, read once by the click that the same
// release fires, so a drag that ends over a button does not also toggle it.
let dragged = false;
// Removes the in-flight drag's window listeners. Held so onBeforeUnmount can
// run it — the listeners are on window and would otherwise outlive the switch.
let detach: (() => void) | null = null;

// Dragging the pill directly, the same gesture as the thinking-level slider:
// while a finger is down the pill leaves its class-driven position and rides
// under the pointer; on release the transition comes back and glides it to
// whichever side it was nearer. A plain tap on a side still toggles, so the
// drag never takes the click away from someone who did not mean to drag.
function onPointerDown(event: PointerEvent): void {
  // Left button only; a right-click or middle-click has no business dragging.
  if (event.button !== 0) return;
  const root = strip.value;
  const pill = root?.querySelector<HTMLElement>('.ai-mode-pill');
  if (!root || !pill) return;

  const rect = root.getBoundingClientRect();
  const pillWidth = pill.offsetWidth;
  const pad = 3; // .ai-mode-switch padding, where the pill's travel starts.
  const gap = 2; // the pill's work-side offset past its own width.
  const travel = pillWidth + gap;
  const startX = event.clientX;
  let moved = false;

  const move = (e: PointerEvent) => {
    // A few pixels of slack so a click that trembles is still a click.
    if (!moved && Math.abs(e.clientX - startX) <= 3) return;
    moved = true;
    dragging.value = true;
    const centred = e.clientX - rect.left - pad - pillWidth / 2;
    dragX.value = Math.min(travel, Math.max(0, centred));
  };
  detach = () => {
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', release);
    window.removeEventListener('pointercancel', release);
    detach = null;
  };
  const release = () => {
    detach?.();
    // Handing the pill back to its class transform, which now transitions to
    // the settled side rather than teleporting.
    dragging.value = false;
    if (!moved) return;
    dragged = true;
    setMode(dragX.value > travel / 2 ? 'work' : 'chat');
    // The click this release fires reads the flag and swallows itself. If it
    // released over the strip's padding no click fires at all, so clear the
    // flag on the next tick too — otherwise a stale true would eat the
    // reader's next genuine tap.
    setTimeout(() => { dragged = false; }, 0);
  };
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', release);
  window.addEventListener('pointercancel', release);
}

function onChoice(option: Mode): void {
  // The click a drag's release also fires: the drag already committed the
  // mode, so swallow this one instead of toggling to wherever it landed.
  if (dragged) {
    dragged = false;
    return;
  }
  setMode(option);
}

// Unmounted with a drag still held — the transcript filled and v-if dropped
// the switch. Drop the window listeners without committing a side: an unmount
// is not a release the reader chose, the way OaResizer does not remember a
// width the drag never settled on.
onBeforeUnmount(() => detach?.());
</script>

<template>
  <div
    ref="strip"
    class="ai-mode-switch"
    :class="{ dragging }"
    role="tablist"
    :aria-label="t('modeChat')"
    @pointerdown="onPointerDown"
  >
    <span
      class="ai-mode-pill"
      :class="{ 'mode-work': pendingMode === 'work' }"
      :style="dragging ? { transform: `translateX(${dragX}px)` } : undefined"
      aria-hidden="true"
    />
    <button
      v-for="option in MODES"
      :key="option"
      type="button"
      role="tab"
      class="ai-mode-choice"
      :class="{ active: pendingMode === option }"
      :aria-selected="pendingMode === option"
      @click="onChoice(option)"
    >{{ t(option === 'work' ? 'modeWork' : 'modeChat') }}</button>
  </div>
</template>
