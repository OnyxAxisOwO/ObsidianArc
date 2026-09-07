<script setup lang="ts">
// A draggable column edge.
//
// The rail and the side panel are fixed widths that suit a 1280px window and
// nothing else: someone on a wide monitor wants a wider conversation list,
// someone editing a long model form wants a wider panel. This makes the edge
// between two columns a handle.
//
// The width is written to a CSS custom property rather than to `style.width`,
// because both columns already derive other things from it — the rail's
// collapsed margin, the panel's slide-out — and those have to move with it.
//
// It is a `separator` with arrow-key support, not only a drag target: a
// column you can resize with a mouse and not with a keyboard is a column half
// the people cannot resize.

import { onMounted, ref } from 'vue';
import { rememberWidth, storedWidth } from '@/composables/useStoredWidth';

const props = defineProps<{
  /** Which of the parent column's edges this handle sits on. */
  edge: 'left' | 'right';
  /** The custom property the width is written to. */
  cssVariable: string;
  /** Where that property is set — often the column, sometimes its row. */
  styleTarget: HTMLElement | null;
  storageKey: string;
  min: number;
  max: number;
  /** Used when nothing is stored, and restored on a double-click. */
  fallback: number;
  label: string;
}>();

const handle = ref<HTMLElement | null>(null);
const active = ref(false);

const clamp = (value: number) => Math.min(props.max, Math.max(props.min, Math.round(value)));

function apply(value: number): void {
  props.styleTarget?.style.setProperty(props.cssVariable, `${clamp(value)}px`);
}

function currentWidth(): number {
  const target = props.styleTarget;
  if (!target) return props.fallback;
  const parsed = parseFloat(getComputedStyle(target).getPropertyValue(props.cssVariable));
  return Number.isFinite(parsed) ? parsed : props.fallback;
}

let startX = 0;
let startWidth = 0;

function onDown(event: PointerEvent): void {
  // Only the primary button; a right-click here is a context menu.
  if (event.button !== 0) return;
  event.preventDefault();
  startX = event.clientX;
  startWidth = currentWidth();
  handle.value?.setPointerCapture(event.pointerId);
  active.value = true;
  // On the body, so the col-resize cursor and the text-selection block apply
  // everywhere the pointer travels — not only over the handle.
  document.body.classList.add('oa-resizing');
}

function onMove(event: PointerEvent): void {
  if (!handle.value?.hasPointerCapture(event.pointerId)) return;
  const delta = event.clientX - startX;
  // Dragging the left edge of a right-hand column makes it wider when the
  // pointer moves left, so the sign flips.
  apply(startWidth + (props.edge === 'right' ? delta : -delta));
}

function onRelease(event: PointerEvent): void {
  if (!handle.value?.hasPointerCapture(event.pointerId)) return;
  handle.value.releasePointerCapture(event.pointerId);
  active.value = false;
  document.body.classList.remove('oa-resizing');
  rememberWidth(props.storageKey, clamp(currentWidth()));
}

function onKey(event: KeyboardEvent): void {
  const step = event.shiftKey ? 64 : 16;
  let next: number | null = null;
  if (event.key === 'ArrowLeft') next = currentWidth() + (props.edge === 'right' ? -step : step);
  else if (event.key === 'ArrowRight') next = currentWidth() + (props.edge === 'right' ? step : -step);
  else if (event.key === 'Home') next = props.min;
  else if (event.key === 'End') next = props.max;
  if (next === null) return;
  event.preventDefault();
  apply(next);
  rememberWidth(props.storageKey, clamp(currentWidth()));
}

// Back to the width it shipped with, for anyone who has dragged themselves
// somewhere they do not want to be.
function onReset(): void {
  apply(props.fallback);
  rememberWidth(props.storageKey, props.fallback);
}

onMounted(() => apply(storedWidth(props.storageKey, props.fallback)));
</script>

<template>
  <div
    ref="handle"
    class="oa-resizer"
    :class="[`on-${props.edge}`, { active }]"
    role="separator"
    aria-orientation="vertical"
    :aria-label="props.label"
    tabindex="0"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onRelease"
    @pointercancel="onRelease"
    @keydown="onKey"
    @dblclick="onReset"
  />
</template>
