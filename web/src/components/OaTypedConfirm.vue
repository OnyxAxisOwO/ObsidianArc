<script setup lang="ts">
// A warning that has to be answered by typing.
//
// The word to type is drawn by the stylesheet from a data attribute
// (`.oa-typed-word::before`), so it is not text in the page: a selection that
// sweeps across it copies nothing, and it is not there for a find-in-page or
// a reader's "copy text" either. The input refuses a paste and a drop for the
// same reason — a word that can be carried in from somewhere else has not been
// read. The point of the thing is that it is read and typed; it is friction on
// purpose, not a defence, and anyone determined enough to get round it was
// going to.
//
// The word is also in the input's accessible name, where a screen reader finds
// it and nothing can select it.

import { computed, nextTick, onMounted, ref } from 'vue';
import OaField from '@/components/OaField.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import { t } from '@/composables/useI18n';
import { IconClose } from '@/icons';

const props = defineProps<{
  title: string;
  body: string;
  prompt: string;
  word: string;
  proceed: string;
}>();

const emit = defineEmits<{ (event: 'answer', confirmed: boolean): void }>();

const typed = ref('');
const field = ref<HTMLInputElement | null>(null);
// Whether the answer has been given. The overlay fades out before it says it
// has closed, and a second click in that time must not answer twice.
let answered = false;

const matches = computed(() => typed.value.trim().toLowerCase() === props.word.trim().toLowerCase());

onMounted(() => void nextTick(() => field.value?.focus()));

function answer(confirmed: boolean, close: () => void): void {
  if (answered || (confirmed && !matches.value)) return;
  answered = true;
  emit('answer', confirmed);
  close();
}

// Escape, the backdrop and the close button all come back through `close`
// without having chosen; that is a no.
function onClosed(): void {
  if (answered) return;
  answered = true;
  emit('answer', false);
}
</script>

<template>
  <OaOverlay v-slot="{ close }" overlay-class="oa-modal-overlay" @close="onClosed">
    <div class="oa-auth-card oa-modal-card oa-typed-confirm" role="alertdialog" aria-modal="true" :aria-label="title">
      <OaIconButton class="oa-icon-btn oa-modal-close" :label="t('close')" @click="close">
        <IconClose :size="16" />
      </OaIconButton>

      <h1 class="oa-auth-title">{{ title }}</h1>
      <p class="oa-auth-sub">{{ body }}</p>

      <form class="oa-auth-form" @submit.prevent="answer(true, close)">
        <OaField :label="prompt">
          <span class="oa-typed-word" :data-word="word" aria-hidden="true" />
          <input
            ref="field"
            v-model="typed"
            type="text"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :aria-label="`${prompt} ${word}`"
            @paste.prevent
            @drop.prevent
            @dragover.prevent
          >
        </OaField>
        <div class="oa-typed-actions">
          <button type="button" class="oa-btn" @click="answer(false, close)">{{ t('cancel') }}</button>
          <button type="submit" class="oa-btn primary" :disabled="!matches">{{ proceed }}</button>
        </div>
      </form>
    </div>
  </OaOverlay>
</template>
