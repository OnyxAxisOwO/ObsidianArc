<script setup lang="ts">
// A conversation's rename, in place of its row. The project rows already edit
// this way, and window.prompt is not an option: some browsers and the preview
// pane return null from it without showing anything, the same reason nothing
// destructive here may rest on window.confirm.
//
// No buttons. Leaving the field is a save, so a Cancel button would have
// saved on its way to being pressed; Escape is the way out.

import { nextTick, onMounted, ref } from 'vue';
import type { Conversation } from '@/api/chat';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { rename } from './useChat';

const props = defineProps<{ conversation: Conversation }>();
const emit = defineEmits<{ (event: 'done'): void }>();

const draft = ref(props.conversation.title);
const host = ref<HTMLElement | null>(null);

// Enter is followed by the blur of the field it unmounts, and Escape by the
// same; whichever ends the edit first is the only one that counts.
let finished = false;

function commit(): void {
  if (finished) return;
  finished = true;
  emit('done');
  void rename(props.conversation, draft.value);
}

function cancel(): void {
  if (finished) return;
  finished = true;
  emit('done');
}

// An Enter or Escape that a Chinese input method is still composing with
// belongs to the candidate window, not to this edit.
function onKeydown(event: KeyboardEvent): void {
  if (event.isComposing) return;
  if (event.key === 'Enter') {
    event.preventDefault();
    commit();
  } else if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    cancel();
  }
}

onMounted(() => {
  void nextTick(() => {
    const input = host.value?.querySelector('input');
    input?.focus();
    input?.select();
  });
});
</script>

<template>
  <!-- keydown and focusout bubble from the input, so they are caught here
       rather than threaded through OaTextField, which forwards neither. -->
  <!-- An Escape stops here: it ends this edit, not whatever else on the page
       listens for it. -->
  <div
    ref="host"
    class="ai-project-form"
    @keydown="onKeydown"
    @focusout="commit"
  >
    <!-- 120 is the server's MaxTitleChars, which would cut a longer one
         silently. -->
    <OaTextField
      v-model="draft"
      :label="t('renamePrompt')"
      :hint="t('renameHint')"
      :placeholder="t('newChat')"
      :max-length="120"
    />
  </div>
</template>
