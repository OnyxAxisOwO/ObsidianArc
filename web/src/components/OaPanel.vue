<script lang="ts">
/**
 * The row a panel was removed from during the flush currently running.
 *
 * Module state rather than anything reactive: it exists for the width of one
 * synchronous patch, and is read by the panel that arrives inside it.
 */
let vacating: HTMLElement | null = null;
</script>

<script setup lang="ts">
// The right-hand panel.
//
// It is a column in the layout, not a sheet over it: the same rounded 18px
// card as the rail and the content beside it, sliding in by margin the way
// the conversation rail slides out. The list stays fully visible and simply
// narrows, which is what makes editing one row of forty feel like staying in
// the same place rather than leaving it.
//
// Below the breakpoint where three columns will not fit, CSS turns it into an
// overlay — the same fallback the conversation rail already makes.
//
// It teleports into the row it belongs to rather than being written there in
// the template, because it is opened from screens several components deep in
// that row and it has to be a flex sibling of them, not a descendant.

import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useEventListener } from '@vueuse/core';
import { t } from '@/composables/useI18n';
import { usePanelHost } from '@/composables/usePanelHost';
import { storedWidth } from '@/composables/useStoredWidth';
import { IconChevron, IconClose } from '@/icons';
import OaIconButton from './OaIconButton.vue';
import OaResizer from './OaResizer.vue';
import OaScrollArea from './OaScrollArea.vue';

const props = withDefaults(defineProps<{
  title: string;
  /** Drops the footer entirely, for a panel whose sections each save themselves. */
  footer?: boolean;
  confirmLabel?: string | undefined;
  cancelLabel?: string | undefined;
  /** Far left of the footer — "Delete", usually. */
  destructiveLabel?: string | undefined;
  /** The question to put in the footer before the action runs. */
  destructiveConfirm?: string | undefined;
  /** Whether the footer's confirm button exists at all. */
  confirmable?: boolean;
  busy?: boolean;
  error?: string;
  /** Replaces the close button with a back arrow, for a panel opened from another. */
  back?: boolean;
  width?: number;
  /** An extra class on the scrolling body, for a panel that lays itself out. */
  bodyClass?: string;
}>(), { footer: true, confirmable: true, width: 400 });

const emit = defineEmits<{
  (event: 'close'): void;
  (event: 'confirm'): void;
  (event: 'destructive'): void;
  (event: 'back'): void;
}>();

const PANEL_WIDTH_KEY = 'obsidian-arc-panel-width';
/** How long the frame takes to travel, matching the stylesheet. */
const ZOOM_MS = 340;
/**
 * When the interior swaps layouts, partway through the frame's travel.
 *
 * The panel's contents are laid out by `.fullscreen` — drawer gets tabs
 * across the top, full screen gets a rail down the side — and a class cannot
 * be interpolated, so that swap is always one frame. Doing it at the end
 * meant the frame glided open and then the inside of it jumped, which is the
 * worst place to put it: the eye has just finished following a smooth
 * movement and is looking straight at the thing that snaps. So it happens
 * early, while the frame is still visibly moving and the contents are faded
 * out for it.
 */
const SWAP_AT_MS = 130;

const host = usePanelHost();
const panel = ref<HTMLElement | null>(null);
const scroll = ref<InstanceType<typeof OaScrollArea> | null>(null);

/**
 * Open from the first frame when this panel is taking over from another that
 * is leaving in the same breath.
 *
 * /settings and /keys are sibling routes, so moving between them unmounts one
 * panel and mounts the next inside a single flush. Entering normally would
 * remove the column, let the conversation beside it reflow to full width for
 * the one frame before the new panel began sliding in, and then narrow it
 * again — a flash that says "a column left and another arrived" about what is
 * really one record being swapped for another.
 */
const shown = ref(vacating !== null && vacating === host.value);
/** The state callers read, which flips the moment the zoom is asked for. */
const fullscreen = ref(false);
/**
 * The class, which flips partway through the travel. Separate from the state
 * above on purpose: the contents have to change over while the frame is still
 * moving, so "is it full screen" and "is it drawn full screen" are two
 * different answers for the length of one animation.
 */
const zoomed = ref(false);
const zooming = ref(false);
const swapping = ref(false);
const confirming = ref(false);

let closed = false;
let closeTimer = 0;

const width = storedWidth(PANEL_WIDTH_KEY, props.width);

onMounted(async () => {
  // Already in place: it took the previous panel's column, at its width.
  if (shown.value) return;
  await nextTick();
  // Force reflow so the starting state is committed before the transition begins.
  void panel.value?.offsetWidth;
  requestAnimationFrame(() => { shown.value = true; });
});

/**
 * Slides out before telling the caller, so the column is seen leaving rather
 * than vanishing and snapping the content wider.
 *
 * The caller navigates in that callback — every one of them does — and a
 * navigation replaces the whole screen, which used to take the panel with it
 * before it had moved a pixel.
 */
function close(): void {
  if (closed) return;
  closed = true;
  shown.value = false;
  closeTimer = window.setTimeout(() => emit('close'), ZOOM_MS);
}

// Escape closes, unless something inside wants the key first — a select that
// is open, for instance.
useEventListener(document, 'keydown', (event: KeyboardEvent) => {
  if (event.key === 'Escape' && !event.defaultPrevented) close();
});

// --- full screen --------------------------------------------------------------
//
// Full screen means absolute over the row, expanding smoothly from the right
// edge of the host. The right boundary stays anchored while the width
// transitions between the drawer width and 100%, so nothing drifts sideways.

let zoomTimer = 0;
let swapTimer = 0;

/**
 * Pins the other columns at the width they have while the panel is one of
 * them. The panel leaves the flex flow the instant it goes full screen, and
 * without this the content behind it snaps wider before the panel has grown
 * far enough to cover it.
 */
function freezeSiblings(): () => void {
  const row = host.value;
  const self = panel.value;
  if (!row || !self) return () => {};
  const pinned: Array<[HTMLElement, string]> = [];
  for (const child of Array.from(row.children)) {
    if (child === self || !(child instanceof HTMLElement)) continue;
    pinned.push([child, child.style.flex]);
    child.style.flex = `0 0 ${child.getBoundingClientRect().width}px`;
  }
  return () => {
    for (const [node, previous] of pinned) node.style.flex = previous;
  };
}

/**
 * Freezes siblings at their target drawer width while the panel shrinks back
 * to a column, so the content behind it is already at its final width and
 * does not reflow when the animation finishes.
 */
function freezeSiblingsForDrawer(drawerWidth: number): () => void {
  const row = host.value;
  const self = panel.value;
  if (!row || !self) return () => {};

  const children = Array.from(row.children).filter(
    (child): child is HTMLElement => child !== self && child instanceof HTMLElement,
  );
  if (!children.length) return () => {};

  const pinned: Array<[HTMLElement, string]> = [];
  const rowStyle = window.getComputedStyle(row);
  const gap = parseFloat(rowStyle.gap) || 0;
  const rowWidth = row.getBoundingClientRect().width;

  let fixedTotal = 0;
  const flexible: HTMLElement[] = [];
  for (const child of children) {
    pinned.push([child, child.style.flex]);
    if (parseFloat(window.getComputedStyle(child).flexGrow) > 0) flexible.push(child);
    else fixedTotal += child.getBoundingClientRect().width;
  }

  const remaining = Math.max(0, rowWidth - drawerWidth - fixedTotal - children.length * gap);
  const perFlex = flexible.length > 0 ? remaining / flexible.length : remaining;

  for (const child of children) {
    child.style.flex = flexible.includes(child)
      ? `0 0 ${perFlex}px`
      : `0 0 ${child.getBoundingClientRect().width}px`;
  }

  return () => {
    for (const [node, previous] of pinned) node.style.flex = previous;
  };
}

function toggleFullscreen(onDone?: () => void): boolean {
  const self = panel.value;
  if (!self) return fullscreen.value;

  window.clearTimeout(zoomTimer);
  window.clearTimeout(swapTimer);
  const drawerWidth = storedWidth(PANEL_WIDTH_KEY, props.width);
  const next = !fullscreen.value;

  // The frame's own geometry is pinned inline for the whole animation, so
  // adding or removing `.fullscreen` partway through cannot disturb it:
  // inline styles outrank the class either way. That is what lets the
  // contents change over while the frame is still travelling.
  const pin = (value: string): void => {
    self.style.top = '0';
    self.style.bottom = '0';
    self.style.right = '0';
    self.style.left = 'auto';
    self.style.width = value;
  };
  const unpin = (): void => {
    self.style.top = '';
    self.style.bottom = '';
    self.style.right = '';
    self.style.left = '';
    self.style.width = '';
  };

  const release = next ? freezeSiblings() : freezeSiblingsForDrawer(drawerWidth);
  zooming.value = true;
  swapping.value = true;
  pin(next ? `${self.getBoundingClientRect().width || drawerWidth}px` : '100%');

  void self.offsetWidth; // Commit the starting width before changing it.
  self.style.width = next ? '100%' : `${drawerWidth}px`;
  fullscreen.value = next;

  swapTimer = window.setTimeout(() => {
    zoomed.value = next;
    swapping.value = false;
  }, SWAP_AT_MS);
  zoomTimer = window.setTimeout(() => {
    zooming.value = false;
    unpin();
    release();
    onDone?.();
  }, ZOOM_MS + 20);

  return fullscreen.value;
}

onBeforeUnmount(() => {
  window.clearTimeout(zoomTimer);
  window.clearTimeout(swapTimer);
  // The slide-out was interrupted by something else taking the screen. Every
  // caller navigates in that callback, so letting it fire now would drag the
  // reader back to wherever this panel thought they should go next.
  window.clearTimeout(closeTimer);

  // Vue unmounts the outgoing component before it mounts the incoming one,
  // both inside one synchronous flush — so a panel being replaced can only be
  // recognised by what just left. The microtask below runs after that flush
  // and before any paint, which is exactly the window a replacement lands in.
  vacating = host.value;
  queueMicrotask(() => { vacating = null; });
});

/**
 * Asks in the footer rather than through window.confirm.
 *
 * The native dialog was the one piece of interface here that could not be
 * styled, and worse, a browser is free to suppress it — which turns Delete
 * into a button that silently does nothing.
 */
function armDestructive(): void {
  if (props.destructiveConfirm) confirming.value = true;
  else emit('destructive');
}

defineExpose({
  toggleFullscreen,
  isFullscreen: () => fullscreen.value,
  close,
  body: () => scroll.value?.scroller ?? null,
});
</script>

<template>
  <Teleport v-if="host" :to="host">
    <aside
      ref="panel"
      class="oa-panel"
      :class="{ open: shown, fullscreen: zoomed, zooming, swapping, 'no-footer': !props.footer }"
      :style="{ '--oa-panel-width': `${width}px` }"
    >
      <div class="oa-panel-head">
        <OaIconButton v-if="props.back" class="oa-icon-btn" :label="t('back')" @click="emit('back')">
          <IconChevron :size="16" />
        </OaIconButton>
        <h2 class="oa-panel-title">{{ props.title }}</h2>
        <slot name="actions" />
        <OaIconButton class="oa-icon-btn" :label="t('close')" @click="close">
          <IconClose :size="16" />
        </OaIconButton>
      </div>

      <OaScrollArea
        ref="scroll"
        wrap-class="oa-panel-body-wrap"
        :scroll-class="props.bodyClass ? `oa-panel-body ${props.bodyClass}` : 'oa-panel-body'"
      >
        <slot />
        <!-- Last in the body, where the hand-written panel appended it: an
             error is about what the reader just tried, not about the form. -->
        <p class="oa-drawer-flash" :class="{ visible: !!props.error }">{{ props.error }}</p>
      </OaScrollArea>

      <div v-if="props.footer" class="oa-panel-foot" :class="{ confirming }">
        <template v-if="confirming">
          <p class="oa-panel-confirm">{{ props.destructiveConfirm }}</p>
          <div class="oa-panel-confirm-row">
            <span class="oa-drawer-foot-spacer" />
            <button type="button" class="oa-btn" @click="confirming = false">{{ t('cancel') }}</button>
            <button
              type="button"
              class="oa-btn oa-btn-danger-solid"
              :disabled="props.busy"
              @click="emit('destructive')"
            >
              {{ props.busy ? '…' : props.destructiveLabel }}
            </button>
          </div>
        </template>
        <template v-else>
          <button
            v-if="props.destructiveLabel"
            type="button"
            class="oa-btn oa-btn-danger"
            @click="armDestructive"
          >
            {{ props.destructiveLabel }}
          </button>
          <span class="oa-drawer-foot-spacer" />
          <button type="button" class="oa-btn" :disabled="props.busy" @click="close">
            {{ props.cancelLabel ?? t('cancel') }}
          </button>
          <button
            v-if="props.confirmable"
            type="button"
            class="oa-btn primary"
            :disabled="props.busy"
            @click="emit('confirm')"
          >
            {{ props.busy ? '…' : (props.confirmLabel ?? t('save')) }}
          </button>
        </template>
      </div>

      <OaResizer
        edge="left"
        css-variable="--oa-panel-width"
        :style-target="panel"
        storage-key="obsidian-arc-panel-width"
        :min="320"
        :max="720"
        :fallback="props.width"
        :label="t('resizePanel')"
      />
    </aside>
  </Teleport>
</template>
