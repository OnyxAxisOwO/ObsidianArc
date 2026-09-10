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
import { t } from '@/composables/useI18n';
import { IconCheck, IconChevron, IconChevronRight, IconSpark } from '@/icons';
import {
  currentModel, models, reasoning, selectModel, setReasoning, stopFor, stopsFor,
  type AvailableModel,
} from './useModels';

/** How long the popover takes to settle into a new height. */
const MORPH_MS = 220;

const group = ref<HTMLElement | null>(null);
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

const thinking = computed(() => !!currentModel.value?.supports_reasoning && reasoning.value.enabled);
const chipEffort = computed(() => (thinking.value ? stops.value[position.value]?.label ?? '' : ''));

let hideTimer = 0;

function toggle(next: boolean): void {
  if (next === open.value) return;
  open.value = next;

  if (next) {
    view.value = 'effort';
    at.value = position.value;
    mounted.value = true;
    void nextTick(() => {
      // Reading a layout property commits the closed state, so the class
      // added next has something to transition away from. A frame callback
      // would do the same, except in a tab the browser is not painting —
      // where it never arrives, and the popover would stay invisible.
      void body.value?.offsetHeight;
      shown.value = true;
    });
    return;
  }
  shown.value = false;
  window.clearTimeout(hideTimer);
  hideTimer = window.setTimeout(() => {
    if (!open.value) mounted.value = false;
  }, MORPH_MS);
}

const shown = ref(false);

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
  await nextTick();
  if (!node) return;
  const target = node.scrollHeight;
  if (from === 0 || from === target) {
    node.style.height = '';
    return;
  }
  node.style.height = `${from}px`;
  void node.offsetHeight;
  node.style.height = `${target}px`;
  window.setTimeout(() => { node.style.height = ''; }, MORPH_MS);
}

// Clicks inside never reach the closer below. Testing containment there would
// not do: a click that swaps the face detaches the element it landed on, so by
// the time the document sees the event its target is in no document at all,
// and "is it inside" answers no.
useEventListener(document, 'click', () => {
  if (open.value) toggle(false);
});
useEventListener(document, 'keydown', (event: KeyboardEvent) => {
  if (open.value && event.key === 'Escape') {
    toggle(false);
    chip.value?.focus();
  }
});

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

function onPointerDown(): void {
  pressed = true;
  // The class comes on the first move, not the press: a click on the track
  // should glide to where it landed, and only a drag needs the easing out of
  // the way.
  const move = () => { dragging.value = true; };
  const release = () => {
    pressed = false;
    dragging.value = false;
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', release);
    window.removeEventListener('pointercancel', release);
    // The easing is back on before this runs, so the thumb travels the last
    // fraction to its stop rather than teleporting.
    commit(Math.round(at.value));
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

onBeforeUnmount(() => window.clearTimeout(hideTimer));
</script>

<template>
  <div ref="group" class="ai-model-control" @click.stop>
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
        {{ currentModel ? currentModel.display_name : t('modelNone') }}
      </span>
      <span v-if="thinking" class="ai-model-chip-effort">{{ chipEffort }}</span>
      <IconChevron :size="12" />
    </button>

    <div v-if="mounted" class="ai-model-pop" :class="{ open: shown }">
      <!-- The height is animated on this wrapper, so the content inside can be
           replaced wholesale without the popover jumping to its new size. -->
      <div ref="body" class="ai-model-pop-body">
        <div v-if="view === 'effort'" class="ai-pop-face">
          <!-- The model, on top, because it is the first half of the decision. -->
          <button type="button" class="ai-pop-model" @click="morph('models')">
            <IconSpark class="ai-pop-model-mark" :size="13" />
            <span class="ai-pop-model-name">
              {{ currentModel ? currentModel.display_name : t('modelNone') }}
            </span>
            <IconChevronRight :size="13" />
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
              <span class="ai-pop-effort-label">{{ stops[nearest]?.label }}</span>
            </div>

            <div
              class="ai-effort"
              :class="{ off: nearest === 0, max: nearest === last, dragging }"
              :style="{ '--effort-ratio': String(last > 0 ? at / last : 0) }"
            >
              <div class="ai-effort-track">
                <!-- The sparks live inside the fill and are clipped by it, so
                     they only ever appear over the part that is "on". -->
                <div class="ai-effort-fill"><span class="ai-effort-sparks" /></div>
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
          </template>
        </div>

        <div v-else class="ai-pop-face">
          <button type="button" class="ai-pop-back" @click="morph('effort')">
            <IconChevron :size="13" />
            <span>{{ t('chooseModel') }}</span>
          </button>

          <p v-if="!models.length" class="ai-pop-note">{{ t('modelsEmpty') }}</p>
          <div v-else class="ai-pop-models">
            <button
              v-for="model in models"
              :key="model.id"
              type="button"
              class="ai-pop-model-row"
              :disabled="model.usable === false"
              @click="pick(model)"
            >
              <span class="ai-pop-model-text">
                <span class="ai-pop-model-title-row">
                  <span class="ai-pop-model-title">{{ model.display_name }}</span>
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
              <IconCheck v-if="model.id === currentModel?.id" :size="14" />
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
