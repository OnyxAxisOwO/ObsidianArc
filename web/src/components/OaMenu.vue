<script setup lang="ts">
// The dropdown the bell, the account button, the rail's row menus and the
// composer's menu use. Not the model chip: its popover changes face and
// height while open, which this panel does not do, so it has its own.
//
// Only one menu is open at a time; opening a second closes the first, which
// is what stops two overlapping panels when a user clicks straight from one
// trigger to another.
//
// The contents are only in the document while it is open — the trigger slot
// gets `open` and `toggle`, the panel slot gets `close` — so a menu that has
// never been opened costs nothing and one that has been closed is not left
// holding a stale list.

import { nextTick, onBeforeUnmount, ref } from 'vue';
import { onClickOutside, useEventListener } from '@vueuse/core';

const props = defineProps<{
  /** Extra class on the positioning wrapper, for alignment overrides. */
  groupClass?: string;
  menuClass?: string;
}>();

// Long enough for the transition in the stylesheet; a few ms over, so the
// last frame is not cut off.
const CLOSE_MS = 160;

const group = ref<HTMLElement | null>(null);
const panel = ref<HTMLElement | null>(null);

// How close to the screen's edge a menu may come.
const GUTTER = 8;

/**
 * Keeps an open menu on screen. A menu hangs from its trigger's edge, and on
 * a phone a trigger in the middle of the header — the bell — sits closer to
 * the left edge than the menu is wide, so the menu ran off the screen with
 * half its text. Narrowed to the screen first, then slid back inside it.
 *
 * Positions using `left`/`right` offsets from the trigger group rather than
 * CSS `translate`: `translate` is a Level 2 property that fails silently in
 * older WebKit/Chromium shells and mobile webviews. Using standard `left`/`right`
 * positioning is portable to every engine and leaves `transform` entirely free
 * for the scale transition. `transform-origin` is pointed at the trigger button
 * so the menu scales up smoothly directly from the trigger.
 */
function fit(): void {
  const el = panel.value;
  if (!el) return;

  const viewWidth = window.innerWidth || document.documentElement.clientWidth || 360;
  const room = viewWidth - 2 * GUTTER;
  const menuWidth = el.offsetWidth;

  if (room < 320 || menuWidth > room) {
    const targetMax = `${room}px`;
    if (el.style.maxWidth !== targetMax) {
      el.style.maxWidth = targetMax;
      el.style.minWidth = '0';
    }
  } else if (el.style.maxWidth) {
    el.style.maxWidth = '';
    el.style.minWidth = '';
  }

  const effectiveWidth = Math.min(el.offsetWidth, room);
  const g = group.value;
  const isLeft = el.classList.contains('oa-menu-left');
  const isUp = el.classList.contains('oa-menu-up') || el.classList.contains('open-up');
  const originY = isUp ? 'bottom' : 'top';
  let shift = 0;

  if (g) {
    const gRect = g.getBoundingClientRect();
    const unscaledLeft = isLeft ? gRect.left : gRect.right - effectiveWidth;
    const unscaledRight = isLeft ? gRect.left + effectiveWidth : gRect.right;

    if (unscaledLeft < GUTTER) {
      shift = GUTTER - unscaledLeft;
    } else if (unscaledRight > viewWidth - GUTTER) {
      shift = (viewWidth - GUTTER) - unscaledRight;
    }

    if (shift) {
      if (isLeft) {
        el.style.left = `${Math.round(shift)}px`;
        el.style.right = 'auto';
      } else {
        el.style.right = `${Math.round(-shift)}px`;
        el.style.left = 'auto';
      }
      const triggerCenterX = gRect.left + gRect.width / 2;
      const menuLeft = unscaledLeft + shift;
      const originX = Math.round(triggerCenterX - menuLeft);
      el.style.transformOrigin = `${originX}px ${originY}`;
    } else {
      el.style.left = '';
      el.style.right = '';
      el.style.transformOrigin = '';
    }
  } else {
    el.style.left = '';
    el.style.right = '';
    el.style.transformOrigin = '';
  }

  if (el.style.translate) {
    el.style.translate = '';
  }
}

// Open is tracked separately from `mounted` because closing keeps the menu in
// the document until its transition has run, and it is shut as far as anyone
// clicking is concerned well before then.
const open = ref(false);
const mounted = ref(false);
const shown = ref(false);
let hideTimer = 0;
let resizeObserver: ResizeObserver | null = null;

function onResize(): void {
  if (open.value) fit();
}

async function show(): Promise<void> {
  for (const other of others) if (other !== close) other();
  others.add(close);
  window.clearTimeout(hideTimer);
  open.value = true;
  mounted.value = true;

  // Let Vue mount the element into the DOM first
  await nextTick();
  if (!open.value) return;

  const el = panel.value;
  if (typeof ResizeObserver !== 'undefined' && el) {
    resizeObserver?.disconnect();
    resizeObserver = new ResizeObserver(() => {
      if (open.value) fit();
    });
    resizeObserver.observe(el);
  }
  window.addEventListener('resize', onResize, { passive: true });

  // Resolve bounds/shift before transition starts to avoid layout thrashing during animation
  fit();

  // Commit transform in next animation frame for silky smooth hardware-composited 60/120fps
  requestAnimationFrame(() => {
    if (!open.value) return;
    fit();
    shown.value = true;
  });
}

function close(): void {
  if (!open.value) return;
  open.value = false;
  shown.value = false;
  others.delete(close);
  window.removeEventListener('resize', onResize);
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.clearTimeout(hideTimer);
  hideTimer = window.setTimeout(() => {
    if (!open.value) mounted.value = false;
  }, CLOSE_MS);
}

function toggle(): void {
  if (open.value) close();
  else show();
}

onClickOutside(group, () => close());
// Capture, and the default prevented, so a panel open beneath the menu does not
// close with it: the panel yields to a key that has already been handled.
useEventListener(document, 'keydown', (event: KeyboardEvent) => {
  if (event.key !== 'Escape' || !open.value) return;
  event.preventDefault();
  close();
}, { capture: true });

onBeforeUnmount(() => {
  // A show() still waiting on nextTick checks this before it attaches the
  // observer and the resize listener, which nothing would then remove.
  open.value = false;
  window.removeEventListener('resize', onResize);
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.clearTimeout(hideTimer);
  others.delete(close);
});

defineExpose({ isOpen: () => open.value, close, open: show, fit });
</script>

<script lang="ts">
/** Every menu currently open. In practice never more than one. */
const others = new Set<() => void>();
</script>

<template>
  <div ref="group" class="oa-chip-group" :class="props.groupClass">
    <slot name="trigger" :open="open" :toggle="toggle" />
    <div v-if="mounted" ref="panel" class="oa-menu" :class="[props.menuClass, { open: shown }]">
      <slot :close="close" />
    </div>
  </div>
</template>
