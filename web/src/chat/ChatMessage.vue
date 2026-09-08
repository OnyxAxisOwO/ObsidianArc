<script setup lang="ts">
// One turn of the transcript, in either voice.
//
// The two roles share enough — the attachments strip, the editor, the action
// row, the copy behaviour — that splitting them into two components meant
// four places to keep in step. What differs is stated as three branches here
// rather than as two files that look alike.

import { computed, nextTick, ref } from 'vue';
import { updateMessage, attachmentURL, type Message, type MessageStats } from '@/api/chat';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import { t } from '@/composables/useI18n';
import { copyToClipboard } from '@/chat/markdown';
import { IconDownload } from '@/icons';
import ChatAttachments from './ChatAttachments.vue';
import ChatThinking from './ChatThinking.vue';
import {
  MAX_MESSAGE_CHARS, activeID, busy, editingID, justSentID, messages,
  runTurn, setFlash, statsWanted,
} from './useChat';

const props = defineProps<{ message: Message }>();

const editor = ref<HTMLTextAreaElement | null>(null);
const draft = ref('');
const saving = ref(false);
const zoomedImage = ref<{ url: string; alt?: string } | null>(null);

const editing = computed(() => editingID.value === props.message.id);
const images = computed(() => props.message.attachments ?? []);

function beginEdit(): void {
  draft.value = props.message.content;
  editingID.value = props.message.id;
  void nextTick(() => editor.value?.focus());
}

function cancelEdit(): void {
  editingID.value = '';
}

/**
 * Editing a question rewrites history from that point: everything after it
 * was an answer to a question that no longer exists.
 */
function resend(): void {
  const text = draft.value.trim();
  editingID.value = '';
  void runTurn({
    content: text,
    truncateFrom: props.message.id,
    attachmentIDs: images.value.map((image) => image.id),
  });
}

/** Editing an answer only corrects the record; nothing is re-generated. */
async function saveAnswer(): Promise<void> {
  const text = draft.value.trim();
  if (!text) return;
  saving.value = true;
  try {
    await updateMessage(activeID.value, props.message.id, text);
    messages.value = messages.value.map((entry) =>
      (entry.id === props.message.id ? { ...entry, content: text } : entry));
    editingID.value = '';
  } catch (error) {
    setFlash(error instanceof Error ? error.message : t('failed'));
  } finally {
    saving.value = false;
  }
}

// Shared with the copy button on every fenced code block, so both reach the
// clipboard the same way. A refusal stays silent: the clipboard is not
// guaranteed in every browser, and a failed copy is not worth an error state
// in the transcript.
async function copy(): Promise<void> {
  if (await copyToClipboard(props.message.content)) setFlash(t('copied'));
}

const rows = computed(() => {
  const lines = draft.value.split('\n').length + 1;
  return props.message.role === 'user'
    ? Math.min(8, Math.max(2, lines))
    : Math.min(16, Math.max(3, lines));
});

function onEditorKey(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault();
    cancelEdit();
    return;
  }
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault();
    if (props.message.role === 'user') resend();
    else void saveAnswer();
  }
}

const stats = computed(() => {
  const value = props.message.stats;
  if (!value || !statsWanted.value) return '';
  return describe(value);
});

function seconds(ms: number): string {
  return ms >= 10000 ? String(Math.round(ms / 1000)) : (Math.round(ms / 100) / 10).toFixed(1);
}

function describe(value: MessageStats): string {
  const parts = [`${seconds(value.ms)}s`];
  parts.push(t(value.streamed ? 'statsStreamed' : 'statsOneShot'));
  if (value.first_token_ms !== undefined) {
    parts.push(t('statsFirstToken', { seconds: seconds(value.first_token_ms) }));
  }
  if (value.output_tokens !== undefined) {
    parts.push(value.input_tokens === undefined
      ? t('statsOutputOnly', { output: value.output_tokens })
      : t('statsTokens', { input: value.input_tokens, output: value.output_tokens }));
  }
  if (value.tps !== undefined) parts.push(t('statsSpeed', { tps: value.tps }));
  return parts.join(' · ');
}
</script>

<template>
  <div
    class="ai-msg"
    :class="[
      props.message.role === 'user' ? 'ai-msg-user' : 'ai-msg-assistant',
      { 'ai-msg-sent': props.message.id === justSentID },
    ]"
  >
    <template v-if="props.message.role === 'user'">
      <ChatAttachments v-if="images.length" :images="images" />

      <div v-if="editing" class="ai-msg-editor">
        <textarea
          ref="editor"
          v-model="draft"
          class="ai-chat-input ai-msg-edit-input"
          :maxlength="MAX_MESSAGE_CHARS"
          :rows="rows"
          @keydown="onEditorKey"
        />
        <div class="ai-msg-editor-actions">
          <button type="button" class="ai-chat-mini-btn" @click="cancelEdit">{{ t('cancel') }}</button>
          <button type="button" class="ai-chat-mini-btn primary" @click="resend">
            {{ t('saveAndResend') }}
          </button>
        </div>
      </div>

      <template v-else>
        <div v-if="props.message.content || !images.length" class="ai-bubble">
          {{ props.message.content }}
        </div>
        <div class="ai-msg-actions">
          <button type="button" class="ai-chat-mini-btn" @click="beginEdit">{{ t('edit') }}</button>
          <button type="button" class="ai-chat-mini-btn" @click="copy">{{ t('copy') }}</button>
        </div>
      </template>
    </template>

    <template v-else>
      <div v-if="props.message.error" class="ai-chat-error">
        <span class="ai-chat-error-text">{{ props.message.error }}</span>
        <button
          type="button"
          class="ai-chat-mini-btn"
          @click="runTurn({ truncateFrom: props.message.id })"
        >{{ t('retry') }}</button>
      </div>

      <template v-else>
        <ChatThinking v-if="props.message.reasoning" :text="props.message.reasoning" />

        <div v-if="editing" class="ai-msg-editor">
          <textarea
            ref="editor"
            v-model="draft"
            class="ai-chat-input ai-msg-edit-input"
            :maxlength="MAX_MESSAGE_CHARS"
            :rows="rows"
            @keydown="onEditorKey"
          />
          <div class="ai-msg-editor-actions">
            <button type="button" class="ai-chat-mini-btn" @click="cancelEdit">{{ t('cancel') }}</button>
            <button
              type="button"
              class="ai-chat-mini-btn primary"
              :disabled="saving"
              @click="saveAnswer"
            >{{ t('save') }}</button>
          </div>
        </div>

        <template v-else>
          <div v-if="images.length" class="ai-chat-generated-images">
            <div v-for="img in images" :key="img.id" class="ai-chat-generated-card">
              <img
                :src="attachmentURL(img.id)"
                class="ai-chat-generated-img"
                :alt="props.message.content || 'Generated image'"
                @click="zoomedImage = { url: attachmentURL(img.id), alt: props.message.content || '' }"
              />
              <div class="ai-chat-generated-bar">
                <a
                  :href="attachmentURL(img.id)"
                  :download="`image-${img.id}.png`"
                  target="_blank"
                  rel="noopener"
                  class="ai-chat-mini-btn"
                  style="text-decoration: none; display: inline-flex; align-items: center; gap: 4px;"
                >
                  <IconDownload :size="12" />
                  <span>{{ t('downloadImage') }}</span>
                </a>
              </div>
            </div>
          </div>
          <OaMarkdown v-if="props.message.content" class="ai-answer" :text="props.message.content" />
          <div class="ai-msg-actions">
            <button v-if="!busy" type="button" class="ai-chat-mini-btn" @click="beginEdit">
              {{ t('edit') }}
            </button>
            <button
              type="button"
              class="ai-chat-mini-btn"
              @click="runTurn({ truncateFrom: props.message.id })"
            >{{ t('regenerate') }}</button>
            <button type="button" class="ai-chat-mini-btn" @click="copy">{{ t('copy') }}</button>
          </div>
          <div v-if="stats" class="ai-msg-stats">{{ stats }}</div>
        </template>
      </template>
    </template>

    <OaImageLightbox
      v-if="zoomedImage"
      :src="zoomedImage.url"
      :alt="zoomedImage.alt"
      @close="zoomedImage = null"
    />
  </div>
</template>
