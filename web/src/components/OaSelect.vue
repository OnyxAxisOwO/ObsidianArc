<script setup lang="ts" generic="T extends string">
// The option list, drawn here instead of by the operating system.
//
// A native <select> hands its popup to the platform. That is why every
// dropdown here would open a grey OS list with a blue bar through it, in the
// middle of a rounded translucent panel, and no theme could restyle it.
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
// when it moves. Unmounting takes the watcher down with the component, so the
// frame callback cannot outlive the trigger it was watching.

import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useEventListener, useRafFn } from '@vueuse/core';
import { placeList } from '@/lib/select-placement';
import { t } from '@/composables/useI18n';
import { Search } from 'lucide-vue-next';
import OaScrollArea from './OaScrollArea.vue';
import type { Choice } from './choice';

defineOptions({
  inheritAttrs: false,
});

const props = withDefaults(defineProps<{
  choices: ReadonlyArray<Choice<T>>;
  modelValue: T;
  /** For a control with no <label> around it. */
  ariaLabel?: string;
  searchable?: boolean;
}>(), { searchable: true });

const emit = defineEmits<{
  (event: 'update:modelValue', value: T): void;
  (event: 'select', value: T): void;
}>();

// Long enough for the transition in the stylesheet.
const CLOSE_MS = 160;
// A pause longer than this between keys starts a new type-ahead word, about
// where a native select starts one.
const TYPE_AHEAD_MS = 900;

// aria-activedescendant needs an id to point at, and two selects on one
// screen must not mint the same one.
const id = `oa-select-${nextSequence()}`;

const trigger = ref<HTMLButtonElement | null>(null);
const list = ref<HTMLElement | null>(null);
const scrollArea = ref<InstanceType<typeof OaScrollArea> | null>(null);
const searchInput = ref<HTMLInputElement | null>(null);
const query = ref('');
const choices = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase();
  return props.searchable && needle
    ? props.choices.filter((choice) => choice.label.toLocaleLowerCase().includes(needle))
    : props.choices;
});

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
// Where the trigger was when the list was last placed; null makes the next
// frame place it whatever the trigger did.
let anchor: { top: number; left: number; width: number } | null = null;
let hideTimer = 0;
// The type-ahead's letters so far and when the last one arrived (see search).
let typed = '';
let typedAt = 0;

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
  const previous = { maxHeight: node.style.maxHeight, top: node.style.top, left: node.style.left };
  node.style.maxHeight = 'none';
  node.style.top = '0px';
  node.style.left = '0px';
  natural = node.offsetHeight;
  width = node.offsetWidth;
  // Filtering often leaves the placement unchanged, so Vue will not rewrite
  // these inline styles unless measurement restores them itself.
  Object.assign(node.style, previous);
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
  const count = choices.value.length;
  if (!count) return;
  active.value = Math.max(0, Math.min(index, count - 1));

  // The list's own scrollTop rather than scrollIntoView: the list is a child
  // of <body>, and scrollIntoView on one of those is entitled to scroll the
  // document under the reader to reveal it.
  const box = scrollArea.value?.scroller ?? list.value;
  if (!box) return;
  const node = box.querySelectorAll('[role="option"]')[active.value];
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
  query.value = '';
  mounted.value = true;
  currentlyOpen = close;

  await nextTick();
  if (!open.value) return;
  settle();
  anchor = null;
  follow.resume();

  // WebKit does not focus a button on click, and every key this control reads
  // arrives on the trigger. Without this the arrows, Enter and Escape are all
  // inert for a list opened with the mouse — and Escape falls through to the
  // panel, which closes underneath the open list.
  //
  // On touch/mobile devices, focusing the search input forces the on-screen
  // keyboard up, which shrinks the viewport and dismisses the menu. Focus the
  // trigger instead so the keyboard stays down until the user taps the field.
  const isCoarse = typeof window !== 'undefined' && window.matchMedia?.('(pointer: coarse)').matches;
  if (isCoarse || !searchInput.value) {
    trigger.value?.focus({ preventScroll: true });
  } else {
    searchInput.value.focus({ preventScroll: true });
  }
  // One frame closed, so the transition has a state to move from.
  requestAnimationFrame(() => {
    if (open.value) shown.value = true;
  });
}

/**
 * Sizes and places a list that has just been built, and marks the row the
 * reader starts on: the chosen value's, or the first match once a search has
 * narrowed the list. Measured before anything is placed, because a hidden
 * node has no height.
 */
function settle(): void {
  measure();
  place();
  active.value = 0;
  markActive(query.value ? 0 : Math.max(0, choices.value.findIndex((choice) => choice.value === props.modelValue)));
  scrollArea.value?.update();
}

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
  if (props.searchable) trigger.value?.focus({ preventScroll: true });
  close();
  emit('select', value);
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
  if (anchor && anchor.top === box.top && anchor.left === box.left && anchor.width === box.width) return;
  // A trigger that changed width changes the list's width with it, which is
  // the one move that needs the size measured again.
  if (anchor?.width !== box.width) measure();
  anchor = { top: box.top, left: box.left, width: box.width };
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
// new word the way a native select does. Lower-cased the way the search box
// lower-cases, so the two agree on what a label starts with.
function search(): void {
  const from = typed.length > 1 ? active.value - 1 : active.value;
  const at = props.choices.findIndex((choice, index) =>
    index > from && choice.label.toLocaleLowerCase().startsWith(typed));
  const found = at >= 0
    ? at
    : props.choices.findIndex((choice) => choice.label.toLocaleLowerCase().startsWith(typed));
  if (found < 0) return;
  if (open.value) markActive(found);
  else choose(props.choices[found]!.value);
}

function onKey(event: KeyboardEvent): void {
  if (event.isComposing || event.keyCode === 229) return;
  const editing = event.target === searchInput.value;
  // Spaces and caret navigation belong to the search text, not the options.
  if (editing && [' ', 'Home', 'End', 'ArrowLeft', 'ArrowRight'].includes(event.key)) return;
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
      markActive(event.key === 'Home' ? 0 : choices.value.length - 1);
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
      const picked = choices.value[active.value];
      if (picked) choose(picked.value);
      else if (!props.searchable) close();
      return;
    }
    case 'Escape':
      if (!open.value) return;
      event.preventDefault();
      if (editing) trigger.value?.focus({ preventScroll: true });
      close();
      return;
    case 'Tab':
      // The input is teleported to the end of body; resume tab order from
      // the original field so keyboard users stay inside their form.
      if (editing) trigger.value?.focus({ preventScroll: true });
      close();
      return;
    default:
      break;
  }

  // A single printable character, so a shortcut with a modifier is left alone.
  if (editing || event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) return;
  const now = Date.now();
  const letter = event.key.toLocaleLowerCase();
  typed = now - typedAt > TYPE_AHEAD_MS ? letter : typed + letter;
  typedAt = now;
  search();
}

// A rebuilt list has a new height, a new set of ids, and possibly fewer rows
// than the arrow keys had walked to.
watch(choices, async () => {
  if (!open.value) return;
  await nextTick();
  if (!open.value) return;
  settle();
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
    :aria-activedescendant="open && !props.searchable && choices.length ? `${id}-${active}` : undefined"
    :aria-label="props.ariaLabel"
    @click="open ? close() : openList()"
    @keydown="onKey"
  >
    <span class="oa-select-label">{{ label }}</span>
  </button>

  <Teleport to="body">
    <div
      v-if="mounted"
      :id="props.searchable ? undefined : id"
      ref="list"
      class="oa-menu oa-select-menu"
      :class="{ open: shown, searchable: props.searchable }"
      :role="props.searchable ? undefined : 'listbox'"
      :style="listStyle"
    >
      <div v-if="props.searchable" class="oa-search oa-select-search">
        <Search :size="14" aria-hidden="true" />
        <input
          ref="searchInput"
          v-model="query"
          type="search"
          role="combobox"
          :aria-label="t('searchOptions')"
          :placeholder="t('searchOptions')"
          :aria-expanded="open"
          :aria-controls="id"
          :aria-activedescendant="open && choices.length ? `${id}-${active}` : undefined"
          aria-autocomplete="list"
          autocomplete="off"
          @keydown="onKey"
        >
      </div>
      <OaScrollArea
        ref="scrollArea"
        :id="props.searchable ? id : undefined"
        :role="props.searchable ? 'listbox' : undefined"
        wrap-class="oa-select-scroll-wrap"
        scroll-class="oa-select-scroll"
      >
        <!-- A div, not a button: focus stays on the trigger and the active row
             is named by aria-activedescendant, which is the combobox pattern. A
             button here would take focus on mousedown and the trigger would
             lose the keydown handler mid-interaction. -->
        <div
          v-for="(choice, index) in choices"
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
        <p v-if="!choices.length" class="oa-menu-empty" role="status">{{ t('noSearchResults') }}</p>
      </OaScrollArea>
    </div>
  </Teleport>
</template>
