<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import { t } from '@/composables/useI18n';
import { IconClose, IconGlobe, IconSend, IconStop } from '@/icons';
import ComposerMenu from './ComposerMenu.vue';
import ModelControl from './ModelControl.vue';
import {
  MAX_MESSAGE_CHARS, TEXT_FILE_ACCEPT, addImages, addTextFiles, attachments, busy,
  draft, messages, opening, removeAttachment, setFlash, status, stoppable, submit, stop,
  webSearchAvailable, webSearchOn,
} from './useChat';
import { captureComposerRect } from './useSendAnimation';

const input = ref<HTMLTextAreaElement | null>(null);
const imagePicker = ref<HTMLInputElement | null>(null);
const filePicker = ref<HTMLInputElement | null>(null);

const placeholder = computed(() => t(messages.value.length ? 'placeholder' : 'placeholderFirst'));

function resize(): void {
  const node = input.value;
  if (!node) return;
  node.style.height = 'auto';
  node.style.height = `${Math.min(200, Math.max(28, node.scrollHeight))}px`;
}

watch(draft, () => void nextTick(resize));
onMounted(resize);

function onKeyDown(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  captureComposerRect(input.value);
  void submit();
}

function onPaste(event: ClipboardEvent): void {
  const files = Array.from(event.clipboardData?.files ?? []);
  if (!files.length) return;
  // Without vision there is nothing to take the pictures, so the paste is left
  // to the browser; addImages says why they were refused.
  if (status.value.vision) event.preventDefault();
  void addImages(files);
}

// The item stays on the menu for a model without vision, and says why when it
// is chosen, rather than opening a picker whose choice would then vanish.
function pickImages(): void {
  if (!status.value.vision) {
    setFlash(t('imagesUnsupported'));
    return;
  }
  imagePicker.value?.click();
}

function onSend(event: MouseEvent): void {
  event.preventDefault();
  if (busy.value) {
    stop();
    return;
  }
  // The rows on screen still belong to the previous conversation while the
  // next one is read; submit would refuse, so the button is disabled and a
  // keyboard submit has nothing to send either.
  if (opening.value) return;
  captureComposerRect(input.value);
  void submit();
}

function onFormSubmit(): void {
  captureComposerRect(input.value);
  void submit();
}

function takeImages(): void {
  const node = imagePicker.value;
  const files = Array.from(node?.files ?? []);
  if (node) node.value = '';
  void addImages(files);
}

function takeFiles(): void {
  const node = filePicker.value;
  const files = Array.from(node?.files ?? []);
  if (node) node.value = '';
  void addTextFiles(files);
}

function toggleWebSearch(): void {
  webSearchOn.value = !webSearchOn.value;
}

defineExpose({
  focus: () => input.value?.focus({ preventScroll: true }),
  getInputRect: () => input.value?.getBoundingClientRect() ?? null,
  inputElement: input,
});
</script>

<template>
  <form class="ai-chat-composer" @submit.prevent="onFormSubmit">
    <!-- Above the box rather than over the transcript: it is about what is
         about to be sent, and it should be read while typing, not after. -->
    <p
      class="ai-chat-unstable"
      role="status"
      :hidden="!status.modelUnstable || !status.configured"
    >{{ t('modelUnstableNotice') }}</p>

    <div class="ai-chat-attachments">
      <div v-for="(entry, index) in attachments" :key="entry.ref.id" class="ai-chat-attachment">
        <img class="ai-chat-attachment-img" :src="entry.preview" alt="" :draggable="false">
        <OaIconButton
          class="ai-chat-attachment-remove"
          :label="t('removeImage')"
          @click="removeAttachment(index)"
        >
          <IconClose :size="11" />
        </OaIconButton>
      </div>
    </div>

    <!-- The textarea takes the full width across the top of the composer card.
         Placing it above the controls lets multi-line prompts expand cleanly
         without being squeezed horizontally by the model chip, and keeps the
         action controls stably anchored at the bottom row. -->
    <textarea
      ref="input"
      v-model="draft"
      class="ai-chat-input"
      rows="1"
      spellcheck="false"
      :maxlength="MAX_MESSAGE_CHARS"
      :placeholder="placeholder"
      :disabled="busy || !status.configured"
      @input="resize"
      @keydown="onKeyDown"
      @paste="onPaste"
    />

    <div class="ai-chat-composer-row">
      <ComposerMenu
        :disabled="busy || !status.configured"
        @pick-images="pickImages"
        @pick-files="filePicker?.click()"
      />

      <div class="ai-chat-composer-tools">
        <!-- A per-message choice rather than a stored preference: the switch
             is on the message that needs a search, and off again after it. -->
        <OaIconButton
          v-if="webSearchAvailable"
          class="ai-chat-websearch"
          :class="{ on: webSearchOn }"
          :label="t(webSearchOn ? 'webSearchOn' : 'webSearchOff')"
          :aria-pressed="webSearchOn"
          :disabled="busy || !status.configured"
          @click="toggleWebSearch"
        >
          <IconGlobe :size="16" />
        </OaIconButton>
        <!-- What answers and how hard it thinks, beside the button that sends
             it — the decision and the act in the same place. -->
        <ModelControl />
        <OaIconButton
          class="ai-chat-send"
          :class="{ stop: stoppable }"
          :disabled="busy ? !stoppable : opening || !status.configured"
          :label="t(stoppable ? 'stop' : 'send')"
          @click="onSend"
        >
          <IconStop v-if="stoppable" :size="14" />
          <IconSend v-else :size="16" />
        </OaIconButton>
      </div>
    </div>

    <input
      ref="imagePicker"
      class="ai-chat-file"
      type="file"
      accept="image/*"
      multiple
      hidden
      @change="takeImages"
    >
    <!-- Text and code only. Anything a model can read as characters is pasted
         into the message; a PDF or a spreadsheet is not something either
         provider protocol accepts, and pretending otherwise would fail at the
         far end. -->
    <input
      ref="filePicker"
      class="ai-chat-file"
      type="file"
      :accept="TEXT_FILE_ACCEPT"
      multiple
      hidden
      @change="takeFiles"
    >
  </form>
</template>
