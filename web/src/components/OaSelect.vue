<script setup lang="ts" generic="T extends string">
// The option list, drawn here instead of by the operating system.
//
// A native <select> hands its popup to the platform. That is why every
// dropdown here used to open a grey OS list with a blue bar through it, in
// the middle of a rounded translucent panel.
//
// What makes this harder than an ordinary menu: a select appears inside
// something that clips, every time. The settings body and the admin body
// scroll, `.oa-panel` and `.oa-admin-main` both carry a backdrop-filter, and
// `.oa-settings` keeps an identity transform left behind by its entry
// animation. A transform, a filter or a backdrop-filter makes an element the
// containing block for `position: fixed`, so a popup left inside the field is
// trapped by an ancestor whether it is absolute or fixed. The list is
// therefore teleported to <body>, out of every one of them, and placed from
// the trigger's rectangle.
//
// That teleport is also the cost: a node on <body> has no idea its trigger
// has moved. So while a list is open one frame callback watches the trigger —
// it closes when the trigger leaves the document or the window, and re-places
// when it moves. Unmounting is handled for us now that this is a component,
// which is the one part the hand-written version had to police by hand.

import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useEventListener, useRafFn } from '@vueuse/core';
import { placeList } from '@/lib/select-placement';
import OaScrollArea from './OaScrollArea.vue';
import type { Choice } from './choice';

defineOptions({
  inheritAttrs: false,
});

const props = defineProps<{
  choices: ReadonlyArray<Choice<T>>;
  modelValue: T;
  /** For a control with no <label> around it. */
  ariaLabel?: string;
}>();

const emit = defineEmits<{ (event: 'update:modelValue', value: T): void }>();

// Long enough for the transition in the stylesheet.
const CLOSE_MS = 160;

// aria-activedescendant needs an id to point at, and two selects on one
// screen must not mint the same one.
const id = `oa-select-${nextSequence()}`;

const trigger = ref<HTMLButtonElement | null>(null);
const list = ref<HTMLElement | null>(null);
const scrollArea = ref<InstanceType<typeof OaScrollArea> | null>(null);

const open = ref(false);
const mounted = ref(false);
const shown = ref(false);
const active = ref(0);

const top = ref(0);
const left = ref(0);
const maxHeight = ref(0);
const minWidth = ref(0);
const origin = ref<'top left' | 'bottom left'>('top left');

// The list's height and width with nothing capping them, measured once per
// build rather than once per frame: reading either forces a layout of the
// whole list, and the watch below runs sixty times a second.
let natural = 0;
let width = 0;
let anchor = '';

const label = computed(() => props.choices.find((choice) => choice.value === props.modelValue)?.label ?? '');

const listStyle = computed(() => ({
  top: `${top.value}px`,
  left: `${left.value}px`,
  maxHeight: `${maxHeight.value}px`,
  minWidth: `${minWidth.value}px`,
  transformOrigin: origin.value,
}));

function measure(): void {
  const node = list.value;
  if (!node) return;
  node.style.maxHeight = 'none';
  node.style.top = '0px';
  node.style.left = '0px';
  natural = node.offsetHeight;
  width = node.offsetWidth;
}

function place(): void {
  const button = trigger.value;
  if (!button) return;
  const box = button.getBoundingClientRect();
  // Never narrower than the control, so the list reads as belonging to it.
  minWidth.value = box.width;
  const spot = placeList(box, natural, width, { width: window.innerWidth, height: window.innerHeight });
  top.value = spot.top;
  left.value = spot.left;
  maxHeight.value = spot.maxHeight;
  origin.value = spot.origin;
  if (list.value) {
    list.value.style.maxHeight = `${spot.maxHeight}px`;
  }
}

function markActive(index: number): void {
  const count = props.choices.length;
  if (!count) return;
  active.value = Math.max(0, Math.min(index, count - 1));

  // The list's own scrollTop rather than scrollIntoView: the list is a child
  // of <body>, and scrollIntoView on one of those is entitled to scroll the
  // document under the reader to reveal it.
  const box = scrollArea.value?.scroller ?? list.value;
  if (!box) return;
  const node = box.children[active.value];
  if (!(node instanceof HTMLElement)) return;
  const rowTop = node.offsetTop;
  const rowBottom = rowTop + node.offsetHeight;
  if (rowTop < box.scrollTop) box.scrollTop = rowTop;
  else if (rowBottom > box.scrollTop + box.clientHeight) box.scrollTop = rowBottom - box.clientHeight;
  scrollArea.value?.update();
}

async function openList(): Promise<void> {
  if (open.value) return;
  currentlyOpen?.();
  window.clearTimeout(hideTimer);
  open.value = true;
  mounted.value = true;
  currentlyOpen = close;

  await nextTick();
  // Before anything is placed: a hidden node has no height.
  measure();
  place();
  markActive(Math.max(0, props.choices.findIndex((choice) => choice.value === props.modelValue)));
  scrollArea.value?.update();
  anchor = '';
  follow.resume();

  // WebKit does not focus a button on click, and every key this control reads
  // arrives on the trigger. Without this the arrows, Enter and Escape are all
  // inert for a list opened with the mouse — and Escape falls through to the
  // panel, which closes underneath the open list.
  trigger.value?.focus({ preventScroll: true });
  // One frame closed, so the transition has a state to move from.
  requestAnimationFrame(() => {
    if (open.value) shown.value = true;
  });
}

let hideTimer = 0;

function close(): void {
  if (!open.value) return;
  open.value = false;
  shown.value = false;
  typed = '';
  if (currentlyOpen === close) currentlyOpen = null;
  follow.pause();
  window.clearTimeout(hideTimer);
  hideTimer = window.setTimeout(() => {
    if (!open.value) mounted.value = false;
  }, CLOSE_MS);
}

/**
 * Shut first, then report. Callers rebuild the form they are in from a
 * change — the providers and usage screens both do — which destroys this
 * trigger underneath an open list. Closing first means the listeners are
 * already off and the node already going when that happens.
 */
function choose(value: T): void {
  close();
  if (value !== props.modelValue) emit('update:modelValue', value);
}

/**
 * One read a frame, and a write only when the trigger has actually moved.
 *
 * This is scroll, resize, an animating panel and a torn-down screen in one
 * mechanism, rather than a listener for each and nothing at all for the last.
 */
const follow = useRafFn(() => {
  const button = trigger.value;
  if (!button) return;
  if (!button.isConnected) {
    close();
    return;
  }
  const box = button.getBoundingClientRect();
  // Scrolled out of the window, usually because the panel behind it
  // scrolled. A list hanging in the middle of the screen with nothing to
  // belong to is worse than one that shuts.
  if (box.bottom < 0 || box.top > window.innerHeight) {
    close();
    return;
  }
  const key = `${box.top}|${box.left}|${box.width}`;
  if (key === anchor) return;
  // A trigger that changed width changes the list's width with it, which is
  // the one move that needs the size measured again.
  if (anchor.split('|')[2] !== String(box.width)) measure();
  anchor = key;
  place();
}, { immediate: false });

useEventListener(document, 'pointerdown', (event: Event) => {
  if (!open.value) return;
  const target = event.target;
  if (!(target instanceof Node)) return;
  if (list.value?.contains(target)) return;
  // The trigger sits inside a <label>, which forwards a click on its text to
  // the control. Treating that text as outside would close the list in the
  // same gesture that opened it.
  const button = trigger.value;
  if (!button) return;
  if (button.closest('label')?.contains(target) ?? button.contains(target)) return;
  close();
}, { capture: true });

// --- type-ahead ---------------------------------------------------------------
//
// The letters typed so far and when the last one arrived, so a pause starts a
// new word the way a native select does.
let typed = '';
let typedAt = 0;

function search(): void {
  const from = typed.length > 1 ? active.value - 1 : active.value;
  const at = props.choices.findIndex((choice, index) =>
    index > from && choice.label.toLowerCase().startsWith(typed));
  const found = at >= 0
    ? at
    : props.choices.findIndex((choice) => choice.label.toLowerCase().startsWith(typed));
  if (found < 0) return;
  if (open.value) markActive(found);
  else choose(props.choices[found]!.value);
}

function onKey(event: KeyboardEvent): void {
  switch (event.key) {
    case 'ArrowDown':
    case 'ArrowUp':
      event.preventDefault();
      if (!open.value) {
        void openList();
        return;
      }
      markActive(active.value + (event.key === 'ArrowDown' ? 1 : -1));
      return;
    case 'Home':
    case 'End':
      if (!open.value) return;
      event.preventDefault();
      markActive(event.key === 'Home' ? 0 : props.choices.length - 1);
      return;
    case 'Enter':
    case ' ': {
      event.preventDefault();
      if (!open.value) {
        void openList();
        return;
      }
      // The list can have been rebuilt from under the walk — the model picker
      // fills itself from a request — so there may be nothing at this index.
      const picked = props.choices[active.value];
      if (picked) choose(picked.value);
      else close();
      return;
    }
    case 'Escape':
      if (!open.value) return;
      event.preventDefault();
      close();
      return;
    case 'Tab':
      close();
      return;
    default:
      break;
  }

  // A single printable character, so a shortcut with a modifier is left alone.
  if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
  const now = Date.now();
  typed = now - typedAt > 900 ? event.key.toLowerCase() : typed + event.key.toLowerCase();
  typedAt = now;
  search();
}

// A rebuilt list has a new height, a new set of ids, and possibly fewer rows
// than the arrow keys had walked to.
watch(() => props.choices, async () => {
  if (!open.value) return;
  await nextTick();
  measure();
  place();
  markActive(Math.max(0, props.choices.findIndex((choice) => choice.value === props.modelValue)));
  scrollArea.value?.update();
});

onBeforeUnmount(() => {
  window.clearTimeout(hideTimer);
  follow.pause();
  if (currentlyOpen === close) currentlyOpen = null;
});

defineExpose({ focus: () => trigger.value?.focus() });
</script>

<script lang="ts">
/**
 * The list that is open, if any. Only one can be: opening a second closes the
 * first, which is what stops two lists overlapping when somebody clicks
 * straight from one trigger to another.
 */
let currentlyOpen: (() => void) | null = null;

let sequence = 0;
function nextSequence(): number {
  sequence += 1;
  return sequence;
}
</script>

<template>
  <button
    ref="trigger"
    v-bind="$attrs"
    type="button"
    class="oa-select"
    role="combobox"
    aria-haspopup="listbox"
    :aria-expanded="open ? 'true' : 'false'"
    :aria-controls="open ? id : undefined"
    :aria-activedescendant="open ? `${id}-${active}` : undefined"
    :aria-label="props.ariaLabel"
    @click="open ? close() : openList()"
    @keydown="onKey"
  >
    <span class="oa-select-label">{{ label }}</span>
  </button>

  <Teleport to="body">
    <div
      v-if="mounted"
      :id="id"
      ref="list"
      class="oa-menu oa-select-menu"
      :class="{ open: shown }"
      role="listbox"
      :style="listStyle"
    >
      <OaScrollArea
        ref="scrollArea"
        wrap-class="oa-select-scroll-wrap"
        scroll-class="oa-select-scroll"
      >
        <!-- A div, not a button: focus stays on the trigger and the active row
             is named by aria-activedescendant, which is the combobox pattern. A
             button here would take focus on mousedown and the trigger would
             lose the keydown handler mid-interaction. -->
        <div
          v-for="(choice, index) in props.choices"
          :id="`${id}-${index}`"
          :key="choice.value"
          class="oa-menu-item"
          :class="{ active: index === active }"
          role="option"
          :aria-selected="choice.value === props.modelValue"
          @mousedown.prevent
          @click="choose(choice.value)"
        >
          <span class="oa-menu-item-title">{{ choice.label }}</span>
        </div>
      </OaScrollArea>
    </div>
  </Teleport>
</template>
