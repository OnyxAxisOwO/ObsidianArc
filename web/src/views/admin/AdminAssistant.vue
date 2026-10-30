<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useResizeObserver } from '@vueuse/core';
import { assistant, type AssistantAction, type WorkMode } from '@/admin/assistant';
import ChatThinking from '@/chat/ChatThinking.vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import { copyToClipboard } from '@/chat/markdown';
import ModelControl from '@/chat/ModelControl.vue';
import { currentModel, loadModels, reasoning, restorePreferences } from '@/chat/useModels';
import { asciiFieldWanted } from '@/chat/useChat';
import { isSendKey } from '@/chat/send-shortcut';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconChevron, IconPlus, IconSend, IconShield, IconSpark, IconStop } from '@/icons';
import { useAdminView } from './adminView';
import RuneField from './RuneField.vue';
import RuneResult from './RuneResult.vue';

const view = useAdminView();
view.setTitle(() => t('navAssistant'));
const { threads, activeID, messages, draft, busy, error, pending, mode } = assistant;
const empty = computed(() => messages.value.length === 0);
const scroll = ref<InstanceType<typeof OaScrollArea> | null>(null);
const transcript = ref<HTMLElement | null>(null);
const input = ref<HTMLTextAreaElement | null>(null);
const follow = ref(true);
const ready = computed(() => !!currentModel.value && !busy.value && !pending.value.length);
const MODES: Array<{ id: WorkMode; label: StringKey; hint: StringKey }> = [
  { id: 'ask', label: 'assistantModeAsk', hint: 'assistantModeAskHint' },
  { id: 'assist', label: 'assistantModeAssist', hint: 'assistantModeAssistHint' },
  { id: 'full', label: 'assistantModeFull', hint: 'assistantModeFullHint' },
];
const currentMode = computed(() => MODES.find((entry) => entry.id === mode.value)!);
const suggestions: StringKey[] = ['assistantSuggestOverview', 'assistantSuggestModels', 'assistantSuggestSettings'];
const statuses: Record<AssistantAction['status'], StringKey> = {
  pending: 'assistantAwaiting', running: 'assistantRunning', done: 'assistantDone',
  failed: 'assistantFailed', skipped: 'assistantSkipped', unknown: 'assistantUnknown',
};

function bottom(): void {
  const node = scroll.value?.scroller;
  if (node && follow.value) node.scrollTop = node.scrollHeight;
}
function onScroll(): void {
  const node = scroll.value?.scroller;
  if (node) follow.value = node.scrollHeight - node.scrollTop - node.clientHeight < 60;
}
useResizeObserver(transcript, bottom);
watch(messages, () => void nextTick(bottom), { deep: true });
watch(activeID, () => { follow.value = true; void nextTick(bottom); });
watch(draft, () => void nextTick(() => {
  if (!input.value) return;
  input.value.style.height = 'auto';
  input.value.style.height = `${Math.min(200, Math.max(28, input.value.scrollHeight))}px`;
}));

function submit(): void {
  if (!ready.value || !currentModel.value) return;
  follow.value = true;
  void assistant.submit(currentModel.value.id, reasoning.value);
}
function suggest(key: StringKey): void { draft.value = t(key); submit(); }
function onKey(event: KeyboardEvent): void {
  if (!isSendKey(event)) return;
  event.preventDefault();
  submit();
}
function chooseMode(next: WorkMode, close: () => void): void { close(); assistant.setMode(next); }

onMounted(() => {
  restorePreferences();
  void loadModels().catch(() => { error.value = t('failed'); });
  input.value?.focus({ preventScroll: true });
});
// A verification gate temporarily unmounts the page. Keep approval cards so
// the administrator can review and retry them after entering their own code.
onBeforeUnmount(assistant.pause);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <OaMenu v-if="threads.length" menu-class="oa-assistant-history">
      <template #trigger="{ toggle }">
        <button type="button" class="oa-btn" @click="toggle">{{ t('history') }}</button>
      </template>
      <template #default="{ close }">
        <OaMenuItem v-for="thread in threads" :key="thread.id" :title="thread.title" :class="{ active: thread.id === activeID }" @click="assistant.open(thread.id); close()" />
      </template>
    </OaMenu>
    <button type="button" class="oa-btn" @click="assistant.newThread()"><IconPlus :size="14" />{{ t('newChat') }}</button>
  </Teleport>

  <div class="oa-assistant ai-chat-wide" :class="{ 'is-empty': empty }">
    <div class="ai-chat-main">
      <RuneField v-if="empty && asciiFieldWanted" />
      <OaScrollArea ref="scroll" wrap-class="ai-chat-scroll-wrap" scroll-class="ai-chat-scroll" @scroll="onScroll">
        <div v-if="empty" class="ai-chat-empty">
          <IconSpark :size="28" class="oa-assistant-mark" />
          <h3 class="ai-chat-empty-title">{{ t('assistantWelcome') }}</h3>
          <p class="ai-chat-empty-body">{{ t('assistantWelcomeHint') }}</p>
          <div class="ai-chat-suggestions">
            <button v-for="key in suggestions" :key="key" type="button" class="ai-chat-suggestion" :disabled="!ready" @click="suggest(key)">{{ t(key) }}</button>
          </div>
        </div>
        <div ref="transcript" class="ai-chat-transcript">
          <template v-for="message in messages" :key="message.id">
            <!-- Management messages are immutable: editing or regenerating an old
                 answer could suggest replaying an operation already applied. -->
            <div class="ai-msg" :class="message.role === 'user' ? 'ai-msg-user' : 'ai-msg-assistant'">
              <div v-if="message.role === 'user'" class="ai-bubble">{{ message.content }}</div>
              <template v-else>
                <ChatThinking v-if="message.reasoning" :text="message.reasoning" :live="busy && message === messages.at(-1)" />
                <OaMarkdown v-if="message.content" class="ai-answer" :text="message.content" />
              </template>
              <div v-if="message.content" class="ai-msg-actions"><button type="button" class="ai-chat-mini-btn" @click="copyToClipboard(message.content)">{{ t('copy') }}</button></div>
            </div>
            <div v-for="action in message.actions" :key="action.id" class="oa-assistant-action ai-msg">
              <div class="oa-assistant-action-head"><IconShield :size="15" /><span>{{ t(statuses[action.status]) }}</span></div>
              <pre class="oa-assistant-command">{{ action.line }}</pre>
              <p v-if="action.status === 'pending'" class="oa-field-hint">{{ t(action.readOnly ? 'assistantQueryHint' : 'assistantChangeHint') }}</p>
              <RuneResult v-if="action.output" :id="action.id" :text="action.output" />
              <div v-if="action.status === 'pending'" class="oa-assistant-action-buttons">
                <OaConfirmButton class="oa-btn primary" :label="t('assistantApprove')" :armed-title="t('assistantApproveConfirm')" :disabled="busy || pending[0]?.id !== action.id" @confirm="assistant.decide(action, true)" />
                <button type="button" class="oa-btn" :disabled="busy || pending[0]?.id !== action.id" @click="assistant.decide(action, false)">{{ t('assistantReject') }}</button>
              </div>
            </div>
          </template>
          <div v-if="busy && !messages.at(-1)?.content && !messages.at(-1)?.reasoning" class="ai-msg ai-msg-assistant ai-chat-pending" role="status">
            <span class="ai-dots" aria-hidden="true"><i v-for="n in 9" :key="n" :style="{ '--d': `${((n - 1) % 3 + Math.floor((n - 1) / 3)) * 0.12}s` }" /></span>
            <span class="ai-shimmer">{{ t('thinking') }}</span>
          </div>
        </div>
      </OaScrollArea>

      <div class="ai-chat-flash" :class="{ visible: !!error }" role="status" aria-live="polite">{{ error }}</div>
      <form class="ai-chat-composer" @submit.prevent="submit">
        <textarea ref="input" v-model="draft" class="ai-chat-input" rows="1" spellcheck="false" :maxlength="32000" :placeholder="t(currentModel ? 'assistantPlaceholder' : 'setupTitle')" :disabled="!ready" @keydown="onKey" />
        <div class="ai-chat-composer-row">
          <OaMenu group-class="oa-composer-menu" menu-class="oa-menu-up oa-menu-left oa-assistant-modes">
            <template #trigger="{ toggle }">
              <button type="button" class="oa-assistant-mode ai-chat-mini-btn" :title="t(currentMode.hint)" @click="toggle"><IconShield :size="14" />{{ t(currentMode.label) }}<IconChevron :size="12" /></button>
            </template>
            <template #default="{ close }">
              <div class="oa-assistant-modes-title">{{ t('assistantWorkMode') }}</div>
              <OaMenuItem v-for="entry in MODES" :key="entry.id" :title="t(entry.label)" :sub="t(entry.hint)" :class="{ active: mode === entry.id }" @click="chooseMode(entry.id, close)" />
            </template>
          </OaMenu>
          <div class="ai-chat-composer-tools">
            <ModelControl />
            <OaIconButton class="ai-chat-send" :class="{ stop: busy || pending.length }" :label="t(busy || pending.length ? 'stop' : 'send')" :disabled="!busy && !pending.length && !ready" @click="busy || pending.length ? assistant.stop() : submit()">
              <IconStop v-if="busy || pending.length" :size="14" /><IconSend v-else :size="16" />
            </OaIconButton>
          </div>
        </div>
      </form>
      <p class="oa-assistant-session-hint">{{ t('assistantSessionHint') }}</p>
    </div>
  </div>
</template>
