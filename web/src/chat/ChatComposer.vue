<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import { t } from '@/composables/useI18n';
import { IconClose, IconSend, IconStop } from '@/icons';
import ComposerMenu from './ComposerMenu.vue';
import ModelControl from './ModelControl.vue';
import {
  MAX_MESSAGE_CHARS, TEXT_FILE_ACCEPT, addImages, addTextFiles, attachments, busy,
  draft, messages, removeAttachment, status, stoppable, submit, stop,
} from './useChat';

const input = ref<HTMLTextAreaElement | null>(null);
const imagePicker = ref<HTMLInputElement | null>(null);
const filePicker = ref<HTMLInputElement | null>(null);

const placeholder = computed(() => t(messages.value.length ? 'placeholder' : 'placeholderFirst'));

function resize(): void {
  const node = input.value;
  if (!node) return;
  node.style.height = 'auto';
  node.style.height = `${Math.min(200, node.scrollHeight)}px`;
}

watch(draft, () => void nextTick(resize));

function onKeyDown(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  void submit();
}

function onPaste(event: ClipboardEvent): void {
  const files = Array.from(event.clipboardData?.files ?? []);
  if (!files.length) return;
  event.preventDefault();
  void addImages(files);
}

function onSend(event: MouseEvent): void {
  event.preventDefault();
  if (busy.value) {
    stop();
    return;
  }
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

defineExpose({ focus: () => input.value?.focus({ preventScroll: true }) });
</script>

<template>
  <form class="ai-chat-composer" @submit.prevent="submit">
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

    <div class="ai-chat-composer-row">
      <ComposerMenu
        :disabled="busy || !status.configured"
        @pick-images="imagePicker?.click()"
        @pick-files="filePicker?.click()"
      />
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
      <!-- What answers and how hard it thinks, beside the button that sends
           it — the decision and the act in the same place. -->
      <ModelControl />
      <button
        type="button"
        class="ai-chat-send"
        :class="{ stop: stoppable }"
        :disabled="busy ? !stoppable : !status.configured"
        :title="t(stoppable ? 'stop' : 'send')"
        :aria-label="t(stoppable ? 'stop' : 'send')"
        @click="onSend"
      >
        <IconStop v-if="stoppable" :size="14" />
        <IconSend v-else :size="16" />
      </button>
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
