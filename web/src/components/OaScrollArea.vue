<script setup lang="ts">
// Overlay scrollbar.
//
// Windows Chromium takes 15-17px for native scrollbars, which shrinks the
// layout and causes horizontal jitter when switching tabs between short and
// long content. This overlay floats inside the element's padding, keeping
// clientWidth completely stable while still giving a visible, draggable bar.
//
// The imperative version created the track and thumb with document.createElement
// and drove them through a handle the caller had to remember to destroy. Here
// they are two elements in a template and the listeners are VueUse's, so the
// teardown is the component going away — which is the whole class of leak the
// old `destroy()` existed to avoid and could be forgotten.

import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useEventListener, useMutationObserver, useResizeObserver } from '@vueuse/core';

const props = defineProps<{
  /** The class on the positioned container the track floats inside. */
  wrapClass?: string;
  /** The class on the element that actually scrolls. */
  scrollClass?: string;
}>();

const container = ref<HTMLElement | null>(null);
const scroller = ref<HTMLElement | null>(null);
const track = ref<HTMLElement | null>(null);

const visible = ref(false);
const scrolling = ref(false);
const dragging = ref(false);
const thumbHeight = ref(28);
const thumbTop = ref(0);

const thumbStyle = computed(() => ({
  height: `${thumbHeight.value}px`,
  transform: `translateY(${thumbTop.value}px)`,
}));

// The thumb is never shorter than this, or a very long transcript gives you
// a two-pixel target nobody can hit.
const MIN_THUMB = 28;

function metrics(): { client: number; scroll: number; trackHeight: number; thumb: number } | null {
  const element = scroller.value;
  if (!element) return null;
  const client = element.clientHeight;
  const scroll = element.scrollHeight;
  if (client <= 0 || scroll <= client + 1) return null;
  const trackHeight = track.value?.clientHeight || client;
  return {
    client,
    scroll,
    trackHeight,
    thumb: Math.max(MIN_THUMB, Math.round((client / scroll) * trackHeight)),
  };
}

function update(): void {
  const box = metrics();
  if (!box) {
    visible.value = false;
    return;
  }
  visible.value = true;
  const element = scroller.value!;
  const maxScroll = box.scroll - box.client;
  const ratio = maxScroll > 0 ? element.scrollTop / maxScroll : 0;
  thumbHeight.value = box.thumb;
  thumbTop.value = Math.round(ratio * (box.trackHeight - box.thumb));
}

let idleTimer = 0;
function onScroll(): void {
  update();
  scrolling.value = true;
  window.clearTimeout(idleTimer);
  idleTimer = window.setTimeout(() => { scrolling.value = false; }, 800);
}

// --- dragging the thumb ------------------------------------------------------

let startY = 0;
let startTop = 0;

function onThumbDown(event: MouseEvent): void {
  if (event.button !== 0) return;
  event.preventDefault();
  event.stopPropagation();
  dragging.value = true;
  startY = event.clientY;
  startTop = scroller.value?.scrollTop ?? 0;
  document.body.style.userSelect = 'none';
}

useEventListener(window, 'mousemove', (event: MouseEvent) => {
  if (!dragging.value) return;
  event.preventDefault();
  const box = metrics();
  const element = scroller.value;
  if (!box || !element) return;
  const travel = box.trackHeight - box.thumb;
  if (travel <= 0) return;
  element.scrollTop = startTop + ((event.clientY - startY) / travel) * (box.scroll - box.client);
}, { capture: true });

useEventListener(window, 'mouseup', () => {
  if (!dragging.value) return;
  dragging.value = false;
  document.body.style.userSelect = '';
}, { capture: true });

// A press on the empty part of the track jumps there, centred on the pointer.
function onTrackDown(event: MouseEvent): void {
  if (event.button !== 0) return;
  const box = metrics();
  const element = scroller.value;
  const rail = track.value;
  if (!box || !element || !rail) return;
  event.preventDefault();
  const offset = event.clientY - rail.getBoundingClientRect().top;
  const travel = box.trackHeight - box.thumb;
  const ratio = travel > 0 ? (offset - box.thumb / 2) / travel : 0;
  element.scrollTop = Math.max(0, Math.min(1, ratio)) * (box.scroll - box.client);
}

// --- keeping up with the content ---------------------------------------------

useResizeObserver(scroller, () => update());
// Search hides mounted forms with v-show to retain drafts. Those style changes
// resize the content without changing either the scroller or its child list.
useMutationObserver(scroller, () => void nextTick(update), {
  childList: true, subtree: true, attributes: true, attributeFilter: ['style', 'hidden'],
});

onMounted(() => {
  requestAnimationFrame(update);
});

// The chat and the admin body both replace their contents wholesale; the
// scroll position going back to nothing is the signal that the thumb has to
// be redrawn from scratch rather than eased.
watch(scroller, () => void nextTick(update));

defineExpose({ scroller, update });
</script>

<template>
  <div ref="container" :class="props.wrapClass">
    <div ref="scroller" :class="props.scrollClass" @scroll.passive="onScroll">
      <slot />
    </div>
    <div
      ref="track"
      class="oa-overlay-track"
      :class="{ scrolling, dragging }"
      :style="{ display: visible ? 'block' : 'none' }"
      @mousedown="onTrackDown"
    >
      <div class="oa-overlay-thumb" :style="thumbStyle" @mousedown="onThumbDown" />
    </div>
  </div>
</template>
