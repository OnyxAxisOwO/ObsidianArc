<script setup lang="ts">
// A button that has to be held down.
//
// Resetting everybody's usage is the largest thing the backoffice does and it
// was a small red word in a footer, the same size and shape as Cancel. A hold
// cannot be hit by accident, it shows how far along it is while it fills, and
// letting go early leaves nothing changed — which is the difference between a
// confirmation somebody read and one they clicked through.
//
// A real button on a plate, rather than a pill that happens to be red. The
// rest of the language here — flat fills, no outlines, nothing skeuomorphic —
// is right for controls somebody uses all day and wrong for this one. A thing
// you press and hold should look like a thing you press and hold, and should
// be obviously not the button you meant to click on the way past.

import { onBeforeUnmount, ref } from 'vue';

const props = withDefaults(defineProps<{
  label: string;
  holdingLabel: string;
  holdMs?: number;
}>(), { holdMs: 1200 });

const emit = defineEmits<{ (event: 'fire'): void }>();

const holding = ref(false);
const fired = ref(false);
const ratio = ref(0);

let timer = 0;
let frame = 0;
let started = 0;

function paint(): void {
  ratio.value = Math.min(1, (Date.now() - started) / props.holdMs);
  if (ratio.value < 1) frame = requestAnimationFrame(paint);
}

function stop(): void {
  window.clearTimeout(timer);
  cancelAnimationFrame(frame);
  timer = 0;
  holding.value = false;
  ratio.value = 0;
}

function begin(event: Event): void {
  event.preventDefault();
  if (timer) return;
  started = Date.now();
  holding.value = true;
  frame = requestAnimationFrame(paint);
  timer = window.setTimeout(() => {
    stop();
    // The cap comes back up and the plate flashes once, so the moment the
    // thing actually happened has a mark of its own rather than being the
    // absence of a press.
    fired.value = true;
    window.setTimeout(() => { fired.value = false; }, 420);
    emit('fire');
  }, props.holdMs);
}

// The keyboard has no press-and-hold, so it gets the same delay from the key
// going down to the key coming up rather than being locked out of the one
// action on the screen.
function onKeyDown(event: KeyboardEvent): void {
  if (event.key === 'Enter' || event.key === ' ') begin(event);
}

onBeforeUnmount(stop);
</script>

<template>
  <div class="oa-bigbutton" :class="{ holding, fired }">
    <!-- The hold, drawn as a ring closing around the cap. A conic gradient
         with a radial mask rather than an SVG arc: one custom property to
         update per frame, and no second element to keep in sync with the
         first. Outermost, because the ring goes around the whole assembly
         rather than inside the plate — it is the light on the housing, and
         the only thing that travels is the cap two circles in from it. -->
    <div class="oa-bigbutton-ring" :style="{ '--hold': String(ratio) }" />
    <div class="oa-bigbutton-base">
      <button
        type="button"
        class="oa-bigbutton-cap"
        @pointerdown="begin"
        @pointerup="stop"
        @pointerleave="stop"
        @pointercancel="stop"
        @keydown="onKeyDown"
        @keyup="stop"
        @blur="stop"
      >
        <span class="oa-bigbutton-label">{{ holding ? props.holdingLabel : props.label }}</span>
      </button>
    </div>
  </div>
</template>
