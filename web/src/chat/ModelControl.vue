<script setup lang="ts">
// The control that decides what answers, and how hard it thinks.
//
// It sits where the decision is acted on: in the composer, beside send.
// Opening it shows the model on top and the effort underneath, because that
// is the order they are chosen in — you pick what answers, then how much work
// it should do.
//
// The effort is a slider rather than three buttons and a switch. Off is the
// left end of it, which is what makes a separate toggle unnecessary: "how
// hard should it think" and "should it think at all" are the same question
// asked at different volumes.

import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useEventListener } from '@vueuse/core';
import { Search } from 'lucide-vue-next';
import OaMarquee from '@/components/OaMarquee.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import ModelTile from './ModelTile.vue';
import { t } from '@/composables/useI18n';
import { useLatestTimeout } from '@/composables/useLatestTimeout';
import { IconChevron, IconChevronRight, IconSpark } from '@/icons';
import { matchesSearch } from '@/lib/search';
import {
  currentModel, models, reasoning, selectModel, setReasoning, stopFor, stopsFor,
  type AvailableModel,
} from './useModels';

/** How many cells the effort meter is drawn with. */
const METER_CELLS = 32;

/** How long the popover takes to settle into a new height. */
const MORPH_MS = 220;

const chip = ref<HTMLButtonElement | null>(null);
const body = ref<HTMLElement | null>(null);

const open = ref(false);
const mounted = ref(false);
/** Which face the popover is showing. */
const view = ref<'effort' | 'models'>('effort');

const stops = computed(() => stopsFor(currentModel.value));
const position = computed(() => stopFor(reasoning.value, stops.value));

/** Where the thumb sits — a fraction while a finger is on it. */
const at = ref(0);
const dragging = ref(false);
let pressed = false;

const last = computed(() => stops.value.length - 1);
const nearest = computed(() => Math.round(at.value));
const effortUp = ref(true);
watch(nearest, (next, before) => { effortUp.value = next > before; });

const thinking = computed(() => !!currentModel.value?.supports_reasoning && reasoning.value.enabled);
const chipEffort = computed(() => (thinking.value ? stops.value[position.value]?.label ?? '' : ''));

// Each owns its own timer, newest call first: a close that is reopened before
// it unmounts the popover must not unmount it later, and an earlier morph must
// not clear the height a later one has just set. Both are cancelled on unmount.
const hideLater = useLatestTimeout();
const settleMorph = useLatestTimeout();

function toggle(next: boolean): void {
  if (next === open.value) return;
  open.value = next;

  if (next) {
    view.value = 'effort';
    at.value = position.value;
    // Measured each time it opens: the composer sits mid-column in an empty
    // conversation and at the foot of a long one, so the room above the chip
    // is different every time, and a fixed cap either wasted it or ran the
    // popover off the top of the window.
    // The ceiling is the chat column's top, not the window's: the bar above
    // the column is drawn over anything that reaches into it.
    const ceiling = chip.value?.closest('.ai-chat-main')?.getBoundingClientRect().top ?? 0;
    room.value = Math.max(0, (chip.value?.getBoundingClientRect().top ?? window.innerHeight) - ceiling - 16);
    mounted.value = true;
    void nextTick(() => {
      requestAnimationFrame(() => {
        if (open.value) shown.value = true;
      });
    });
    return;
  }
  shown.value = false;
  hideLater(() => {
    if (!open.value) mounted.value = false;
  }, MORPH_MS);
}

const shown = ref(false);
/** The height there is above the chip for the popover to grow into. */
const room = ref(0);

/**
 * Swaps the popover's contents, animating the height between the two.
 *
 * Measured rather than declared: the model list's height depends on how many
 * models this account has, which is not something a stylesheet can know.
 */
async function morph(to: 'effort' | 'models'): Promise<void> {
  const node = body.value;
  const from = node?.offsetHeight ?? 0;
  view.value = to;
  if (to === 'models') {
    query.value = '';
    highlight.value = Math.max(0, filtered.value.findIndex((entry) => entry.id === currentModel.value?.id));
  }
  await nextTick();
  if (to === 'models') search.value?.focus({ preventScroll: true });
  if (!node) return;
  const target = node.scrollHeight;
  if (from === 0 || from === target) {
    node.style.height = '';
    return;
  }
  node.style.height = `${from}px`;
  void node.offsetHeight;
  node.style.height = `${target}px`;
  settleMorph(() => { node.style.height = ''; }, MORPH_MS);
}

// Clicks inside never reach the closer below. Testing containment there would
// not do: a click that swaps the face detaches the element it landed on, so by
// the time the document sees the event its target is in no document at all,
// and "is it inside" answers no.
useEventListener(document, 'click', () => {
  if (open.value) toggle(false);
});
// Capture, and the default prevented, as OaMenu does: the chip sits under the
// composer beside a side panel that also closes on Escape, and that panel only
// yields to a key that has already been handled. Without this one press closed
// both.
useEventListener(document, 'keydown', (event: KeyboardEvent) => {
  if (!open.value || event.key !== 'Escape') return;
  event.preventDefault();
  toggle(false);
  chip.value?.focus();
}, { capture: true });

/**
 * Settles on a stop and tells the rest of the app.
 *
 * Only ever on release or a keypress, never per pointer move: this is
 * persisted to the account, so committing continuously would be a request for
 * every pixel dragged.
 */
function commit(next: number): void {
  const settled = Math.min(last.value, Math.max(0, next));
  at.value = settled;
  if (settled === position.value) return;
  const stop = stops.value[settled];
  if (stop) setReasoning({ enabled: stop.enabled, effort: stop.effort });
}

function onRangeInput(event: Event): void {
  const raw = Number((event.target as HTMLInputElement).value);
  // Under a finger the thumb goes where the finger is. Otherwise the input
  // moved by itself and there is a value to commit.
  if (pressed) at.value = raw;
  else commit(Math.round(raw));
}

/** Takes the window listeners of the drag in progress back off, if there is one. */
let endDrag: (() => void) | null = null;

function onPointerDown(): void {
  // A second finger on the track arrives while the first drag's window
  // listeners are still attached. Take them off before endDrag is replaced,
  // or the first release would outlive the component and commit later.
  endDrag?.();
  pressed = true;
  // The class comes on the first move, not the press: a click on the track
  // should glide to where it landed, and only a drag needs the easing out of
  // the way.
  const move = () => { dragging.value = true; };
  const release = () => {
    pressed = false;
    dragging.value = false;
    endDrag?.();
    // The easing is back on before this runs, so the thumb travels the last
    // fraction to its stop rather than teleporting.
    commit(Math.round(at.value));
  };
  endDrag = () => {
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', release);
    window.removeEventListener('pointercancel', release);
    endDrag = null;
  };
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', release);
  window.addEventListener('pointercancel', release);
}

// A continuous range would step by a thousandth on an arrow key. The stops are
// what the keyboard moves between.
function onRangeKey(event: KeyboardEvent): void {
  let next: number | null = null;
  if (event.key === 'ArrowRight' || event.key === 'ArrowUp') next = position.value + 1;
  else if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') next = position.value - 1;
  else if (event.key === 'Home') next = 0;
  else if (event.key === 'End') next = last.value;
  if (next === null) return;
  event.preventDefault();
  commit(next);
}

const search = ref<HTMLInputElement | null>(null);
const list = ref<HTMLElement | null>(null);
const query = ref('');
/** The row the keyboard is on, by its place in the filtered list. */
const highlight = ref(0);

const filtered = computed(() => models.value.filter(
  (entry) => matchesSearch(query.value, entry.display_name, entry.description ?? ''),
));

watch(query, () => { highlight.value = 0; });

// Arrows move through the rows without leaving the search field, the way a
// command palette does: typing and choosing are one motion, not two.
function onSearchKey(event: KeyboardEvent): void {
  if (event.isComposing) return;
  const count = filtered.value.length;
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    if (!count) return;
    event.preventDefault();
    const step = event.key === 'ArrowDown' ? 1 : -1;
    highlight.value = (highlight.value + step + count) % count;
    void nextTick(() => {
      list.value?.querySelector<HTMLElement>('.ai-pop-model-row.highlight')?.scrollIntoView?.({ block: 'nearest' });
    });
  } else if (event.key === 'Enter') {
    const model = filtered.value[highlight.value];
    if (!model) return;
    event.preventDefault();
    pick(model);
  }
}

/** A token count the way a spec sheet prints one: 128K, 1M, 8.2K. */
function tokens(count: number): string {
  if (count >= 1_000_000) return `${+(count / 1_000_000).toFixed(1)}M`;
  if (count >= 1000) return `${+(count / 1000).toFixed(count >= 10_000 ? 0 : 1)}K`;
  return String(count);
}

/**
 * The highlighted model's sheet, under the list: what it can hold, how much it
 * can say, how the API names it, what it can do. Always the same four rows, a dash where the
 * operator left one blank: the sheet changes as the pointer moves down the
 * list, and a sheet that changed height with it moved the list under the
 * pointer — the popover hangs from its bottom edge — so the next row was
 * lit, the sheet changed again, and the list shook.
 */
const specs = computed(() => {
  const model = filtered.value[highlight.value];
  if (!model) return [];
  const can = [
    model.supports_images && t('specVision'),
    model.supports_reasoning && t('specReasoning'),
    model.supports_tools && t('specTools'),
    model.supports_chat_image_gen && t('canImageGen'),
  ].filter(Boolean);
  return [
    { label: t('specContext'), value: model.context_window > 0 ? tokens(model.context_window) : '—' },
    { label: t('specOutput'), value: model.max_output_tokens > 0 ? tokens(model.max_output_tokens) : '—' },
    { label: t('apiNameLabel'), value: model.api_name || '—' },
    { label: t('specCan'), value: can.length ? can.join(' · ') : '—' },
  ];
});

function pick(model: AvailableModel): void {
  if (model.usable === false) return;
  selectModel(model.id);
  void morph('effort');
}

/**
 * The small figure beside a model's name.
 *
 * Only what the operator published: a number when they publish one, the word
 * alone when they only asked for a warning, and nothing at all otherwise —
 * which is every instance that has not turned this on.
 */
function uptimeTag(model: AvailableModel): { text: string; warn: boolean; title?: string } | null {
  if (model.uptime === undefined) {
    return model.unstable ? { text: t('modelUnstableTag'), warn: true } : null;
  }
  return {
    text: `${(model.uptime * 100).toFixed(model.uptime >= 0.995 ? 0 : 1)}%`,
    warn: !!model.unstable,
    title: t('modelUptimeTitle'),
  };
}

// The stop the state is on, whenever something other than a drag moved it —
// switching to a model with different tiers, or the settings panel changing
// the remembered effort while this is open.
watch(position, (next) => {
  if (!pressed) at.value = next;
});

onBeforeUnmount(() => {
  // A drag the unmount cut short is not a choice the reader made, so it must
  // not reach commit(). Its window listeners would otherwise stay attached and
  // fire on whatever pointerup comes next.
  endDrag?.();
  pressed = false;
});
</script>

<template>
  <div class="ai-model-control" @click.stop>
    <button
      ref="chip"
      type="button"
      class="ai-model-chip"
      :class="{ open, placeholder: !currentModel }"
      aria-haspopup="true"
      :aria-expanded="open ? 'true' : 'false'"
      :title="currentModel ? currentModel.display_name : t('modelNone')"
      @click.stop="toggle(!open)"
    >
      <!-- The effort rides on the chip so the state is legible without
           opening anything — which is the whole reason the two controls
           became one. -->
      <IconSpark v-if="thinking" class="ai-model-chip-spark" :size="13" />
      <span class="ai-model-chip-name">
        <OaMarquee :text="currentModel ? currentModel.display_name : t('modelNone')" />
      </span>
      <span v-if="thinking" class="ai-model-chip-effort">{{ chipEffort }}</span>
      <IconChevron :size="12" />
    </button>

    <div
      v-if="mounted"
      class="ai-model-pop"
      :class="{ open: shown }"
      :style="room ? { '--ai-pop-room': `${room}px` } : undefined"
    >
      <!-- The height is animated on this wrapper, so the content inside can be
           replaced wholesale without the popover jumping to its new size. -->
      <div ref="body" class="ai-model-pop-body">
        <div v-if="view === 'effort'" class="ai-pop-face">
          <!-- The model, on top, because it is the first half of the decision. -->
          <button type="button" class="ai-pop-model" @click="morph('models')">
            <ModelTile :avatar="currentModel?.avatar ?? ''" :name="currentModel?.display_name ?? '·'" />
            <span class="ai-pop-model-text">
              <span class="ai-pop-model-name">
                <OaMarquee :text="currentModel ? currentModel.display_name : t('modelNone')" />
              </span>
              <span class="ai-pop-model-sub">{{ currentModel?.description || t('chooseModel') }}</span>
            </span>
            <IconChevronRight class="ai-pop-model-go" :size="14" />
          </button>

          <!-- No model yet, so there is nothing whose thinking this could be
               about. Saying "this model has no thinking steps" about a model
               nobody has chosen answers a question that was not asked. -->
          <p v-if="!currentModel" class="ai-pop-note">{{ t('reasoningPickFirst') }}</p>
          <!-- Nothing to set: saying so beats a slider that would be ignored. -->
          <p v-else-if="!currentModel.supports_reasoning" class="ai-pop-note">
            {{ t('reasoningUnavailable') }}
          </p>

          <template v-else>
            <div class="ai-pop-effort-head">
              <span class="ai-pop-effort-title">{{ t('reasoningToggle') }}</span>
              <span class="oa-header-spacer" />
              <span class="ai-pop-effort-label" aria-hidden="true">
                <Transition :name="effortUp ? 'ai-effort-up' : 'ai-effort-down'">
                  <span :key="stops[nearest]?.label">{{ stops[nearest]?.label }}</span>
                </Transition>
              </span>
            </div>

            <div
              class="ai-effort"
              :class="{ off: nearest === 0, max: nearest === last, dragging }"
              :style="{ '--effort-ratio': String(last > 0 ? at / last : 0) }"
            >
              <!-- A meter of cells rather than a filled bar: the same dot
                   matrix the wait for an answer pulses with, lit up to where
                   the thumb is, each lit cell a little brighter than the one
                   before so the amount reads as rising, not as a colour. -->
              <div class="ai-effort-track">
                <i
                  v-for="n in METER_CELLS"
                  :key="n"
                  :class="{ on: nearest > 0 && (n - 1) / (METER_CELLS - 1) <= (last > 0 ? at / last : 0) }"
                  :style="{ '--cell': String((n - 1) / (METER_CELLS - 1)) }"
                />
                <span class="ai-effort-thumb" />
              </div>
              <!-- A real range input on top, invisible: the visual is ours, the
                   focus ring and the ARIA are the platform's. Its step is
                   continuous even though the setting has four values — a range
                   that steps in quarters jumps the thumb between four places
                   while your finger is somewhere else, which is what makes a
                   slider feel broken. -->
              <input
                class="ai-effort-range"
                type="range"
                min="0"
                :max="last"
                step="0.001"
                :value="at"
                :aria-label="t('reasoningToggle')"
                :aria-valuetext="stops[nearest]?.label"
                @input="onRangeInput"
                @pointerdown="onPointerDown"
                @keydown="onRangeKey"
              >
            </div>
            <!-- Every stop named under the track, the one it is on in full
                 weight: the slider reads as a scale with marks on it rather
                 than a bar whose meaning lives in a label somewhere else. -->
            <div class="ai-effort-scale" aria-hidden="true">
              <span
                v-for="(stop, index) in stops"
                :key="index"
                :class="{ on: index === nearest }"
              >{{ stop.label }}</span>
            </div>
          </template>
        </div>

        <div v-else class="ai-pop-face">
          <div class="ai-pop-search">
            <button type="button" class="ai-pop-back" :aria-label="t('back')" @click="morph('effort')">
              <IconChevron :size="14" />
            </button>
            <Search class="ai-pop-search-icon" :size="14" aria-hidden="true" />
            <input
              ref="search"
              v-model="query"
              type="text"
              class="ai-pop-search-input"
              :placeholder="t('modelSearch')"
              :aria-label="t('modelSearch')"
              autocomplete="off"
              spellcheck="false"
              @keydown="onSearchKey"
            >
          </div>

          <div v-if="models.length" class="ai-pop-section">
            <span>{{ t('modelListLabel') }}</span>
            <span class="ai-pop-count">{{ String(filtered.length).padStart(2, '0') }}</span>
          </div>
          <p v-if="!models.length" class="ai-pop-note">{{ t('modelsEmpty') }}</p>
          <p v-else-if="!filtered.length" class="ai-pop-note">{{ t('modelSearchNone') }}</p>
          <!-- An overlay track rather than the system's scrollbar: where the
               system draws one with a gutter, it took fifteen pixels off the
               right of every row and left the figures standing off the edge. -->
          <OaScrollArea v-else wrap-class="ai-pop-models-wrap" scroll-class="ai-pop-models">
            <div ref="list" class="ai-pop-models-inner">
              <button
                v-for="(model, index) in filtered"
                :key="model.id"
                type="button"
                class="ai-pop-model-row"
                :class="{ highlight: index === highlight, current: model.id === currentModel?.id }"
                :disabled="model.usable === false"
                @click="pick(model)"
                @pointermove="highlight = index"
              >
                <ModelTile :avatar="model.avatar" :name="model.display_name" />
                <span class="ai-pop-model-text">
                  <span class="ai-pop-model-title-row">
                    <span class="ai-pop-model-title">
                      <OaMarquee :text="model.display_name" />
                    </span>
                    <!-- Only when it draws in a conversation. A model that
                         generates in the image lab and talks here is a chat
                         model here, and a tag saying otherwise would promise
                         the composer something it cannot do. -->
                    <span
                      v-if="model.supports_chat_image_gen"
                      class="ai-pop-model-tag"
                    >{{ t('canImageGen') }}</span>
                    <span
                      v-if="uptimeTag(model)"
                      class="ai-pop-model-tag"
                      :class="{ warn: uptimeTag(model)!.warn }"
                      :title="uptimeTag(model)!.title"
                    >{{ uptimeTag(model)!.text }}</span>
                  </span>
                  <!-- No description means no description. Falling back to the
                       provider's name answered a question nobody asked. -->
                  <span
                    v-if="model.usable === false || model.description"
                    class="ai-pop-model-sub"
                  >{{ model.usable === false ? t('modelNotAllowedGroup') : model.description }}</span>
                </span>
                <span v-if="model.context_window > 0" class="ai-pop-model-ctx">{{ tokens(model.context_window) }}</span>
              </button>
            </div>
          </OaScrollArea>
          <dl v-if="specs.length" class="ai-pop-spec">
            <div v-for="row in specs" :key="row.label">
              <dt>{{ row.label }}</dt>
              <dd :title="row.value">{{ row.value }}</dd>
            </div>
          </dl>
          <!-- What the keys do, said once at the foot rather than learnt by
               accident. Only where there is a keyboard to press them. -->
          <div v-if="filtered.length" class="ai-pop-keys" aria-hidden="true">
            <span><kbd>↑</kbd><kbd>↓</kbd>{{ t('keyMove') }}</span>
            <span><kbd>↵</kbd>{{ t('keyPick') }}</span>
            <span><kbd>esc</kbd>{{ t('keyClose') }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
