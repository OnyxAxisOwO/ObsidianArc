<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaResizer from '@/components/OaResizer.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { matchesSearch } from '@/lib/search';
import { t } from '@/composables/useI18n';
import { useLatestTimeout } from '@/composables/useLatestTimeout';
import { usePanelHost } from '@/composables/usePanelHost';
import { IconPlus, IconTrash } from '@/icons';
import ConversationRow from './ConversationRow.vue';
import ProjectList from './ProjectList.vue';
import {
  activeID, archiveConversation, canDelete, clearEverything,
  conversations, openConversation, pendingID, RAIL_LIMIT, railFull, removeConversation, RISE_MS,
  startNewConversation,
} from './useChat';
import { useRowMenu } from './useRowMenu';

// The rail's width drives its own collapsed margin as well as its size, so
// the handle writes the custom property on the row rather than a width here.
const row = usePanelHost();

// Which list the rail shows. Local and unpersisted: it is a view of the
// rail, not a fact about the account, so it does not need to survive a
// reload the way the pending project in workspace.ts does.
const activeTab = ref<'history' | 'projects'>('history');
const query = ref('');
const filteredConversations = computed(() => conversations.value.filter((entry) =>
  matchesSearch(query.value, entry.title || t('newChat'))));

// The armed delete inside it is cleared with this list, so it is made here in
// setup rather than by each row.
const rowMenu = useRowMenu();

// The transcript takes file drops and the rail does not, but the browser's own
// default for a dropped file is to open it in this tab, which throws the chat
// and the draft away. Cancelling it here is what keeps a drop on the rail from
// doing that.
function cancelFileDrop(event: DragEvent): void {
  const types = event.dataTransfer?.types;
  if (!types || Array.prototype.indexOf.call(types, 'Files') === -1) return;
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'none';
}

// A short rise on the row that just became current, so switching reads as a
// different conversation rather than the list quietly repainting.
const switched = ref(false);
const settle = useLatestTimeout();
watch(activeID, () => {
  switched.value = true;
  settle(() => { switched.value = false; }, RISE_MS);
});

// Tests mount without a router, so either may be missing and the optional
// chains below are what keep them from failing.
const route = useRoute();
const router = useRouter();

function onSelectConversation(id: string): void {
  void openConversation(id);
  if (route?.path === '/terminal') void router?.push('/');
}

function onNewChat(): void {
  startNewConversation();
  if (route?.path === '/terminal') void router?.push('/');
}
</script>

<template>
  <div class="ai-chat-sidebar" @dragover="cancelFileDrop" @drop="cancelFileDrop">
    <div class="ai-chat-sidebar-head">
      <!-- Replaces the old static "History" label: the tab a reader is on
           already says what the list below is, so the label and the switch
           are one control instead of two that could disagree. -->
      <div class="oa-slide-tabs" role="tablist">
        <span
          class="oa-slide-pill"
          :class="{ second: activeTab === 'projects' }"
          aria-hidden="true"
        />
        <button
          type="button"
          class="oa-slide-tab ai-sidebar-tab"
          :class="{ active: activeTab === 'history' }"
          role="tab"
          :aria-selected="activeTab === 'history'"
          @click="activeTab = 'history'"
        >{{ t('tabHistory') }}</button>
        <button
          type="button"
          class="oa-slide-tab ai-sidebar-tab"
          :class="{ active: activeTab === 'projects' }"
          role="tab"
          :aria-selected="activeTab === 'projects'"
          @click="activeTab = 'projects'"
        >{{ t('tabProjects') }}</button>
      </div>
    </div>

    <template v-if="activeTab === 'history'">
      <div class="ai-history-search-row">
        <OaSearchField v-model="query" class="oa-history-search" :label="t('searchHistory')" />
        <OaIconButton class="ai-history-new-btn" :label="t('newChat')" @click="onNewChat">
          <IconPlus :size="16" />
        </OaIconButton>
      </div>

      <OaScrollArea wrap-class="ai-chat-list-wrap" scroll-class="ai-chat-list">
        <p v-if="!conversations.length" class="ai-chat-list-empty">{{ t('noHistory') }}</p>
        <p v-else-if="!filteredConversations.length" class="ai-chat-list-empty" role="status">{{ t('noSearchResults') }}</p>
        <ConversationRow
          v-for="conversation in filteredConversations"
          :key="conversation.id"
          :conversation="conversation"
          :menu="rowMenu"
          :active="conversation.id === activeID"
          :pending="conversation.id === pendingID"
          :switched="conversation.id === activeID && switched"
          @open="onSelectConversation(conversation.id)"
          @archive="archiveConversation(conversation)"
          @remove="removeConversation(conversation)"
        />
        <!-- The search filters the rail, and a full rail is only the latest
             conversations; without this an older one reads as not existing. -->
        <p v-if="railFull && query.trim()" class="ai-chat-list-empty" role="note">
          {{ t('searchRecentOnly', { count: RAIL_LIMIT }) }}
        </p>
      </OaScrollArea>

      <div class="ai-chat-sidebar-foot" :hidden="conversations.length === 0">
        <OaConfirmButton
          v-if="canDelete"
          class="ai-chat-clear-all"
          :armed-label="t('clearAllConfirm')"
          :armed-title="t('confirmClearAll')"
          @confirm="clearEverything"
        >
          <IconTrash :size="12" />
          <span>{{ t('clearAll') }}</span>
        </OaConfirmButton>
      </div>
    </template>

    <ProjectList v-else />

    <OaResizer
      edge="right"
      css-variable="--ai-rail-width"
      :style-target="row"
      storage-key="obsidian-arc-rail-width"
      :min="190"
      :max="460"
      :fallback="260"
      :label="t('resizeRail')"
    />
  </div>
</template>
