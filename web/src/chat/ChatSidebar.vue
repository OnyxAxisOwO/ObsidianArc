<script setup lang="ts">
// The rail: one list, folders above and the loose conversations below. It
// used to be two tabs, history and projects, and a conversation filed in a
// project then appeared in both — once in the history, once under its
// project — while nothing on either tab could move it from one to the other.
// One list makes each conversation appear once, where it is filed, and the
// filing a drag.

import { computed, onMounted, ref, shallowRef, watch } from 'vue';
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
import { IconFolderPlus, IconPlus, IconTrash } from '@/icons';
import { loadProjects, projectList, projectMax } from '@/stores/projects';
import ConversationRow from './ConversationRow.vue';
import {
  activeID, archiveConversation, canDelete, clearEverything,
  conversations, dropConversation, dropTarget, offerConversationDrop, openConversation, pendingID,
  RAIL_LIMIT, railFull, removeConversation, RISE_MS, startNewConversation,
} from './useChat';
import { useRowMenu } from './useRowMenu';

// The rail's width drives its own collapsed margin as well as its size, so
// the handle writes the custom property on the row rather than a width here.
const row = usePanelHost();

const query = ref('');
const searching = computed(() => query.value.trim() !== '');
// A search is a find, not a look at the filing: its results come from every
// folder at once, flat, so a conversation is found whichever folder it is in.
const filteredConversations = computed(() => conversations.value.filter((entry) =>
  matchesSearch(query.value, entry.title || t('newChat'))));
// The rail's list holds filed conversations too; they are drawn under their
// folders, which read their own lists, so only the loose ones are drawn here.
// refreshList reads the loose ones separately, so filed conversations filling
// the newest of the list cannot crowd them out of it.
const looseConversations = computed(() => conversations.value.filter((entry) => !entry.project_id));

// The create form lives with the folders it adds to, and its button in the
// head beside the new chat, where it can be reached whether or not any
// folder exists yet.
const creatingFolder = ref(false);

// The folders are hidden while a search shows its flat results, and a form
// opened inside them would be opened out of sight, with nowhere for focus to
// land. Starting a folder is not a search, so it ends the one in progress.
function toggleCreatingFolder(): void {
  if (!creatingFolder.value) query.value = '';
  creatingFolder.value = !creatingFolder.value;
}
// Folder management is needed only by accounts with folders, or when one is
// being made. Load it alongside the list rather than on every first paint.
const folderContent = shallowRef<typeof import('./ProjectList.vue')['default'] | null>(null);
const folderLoading = ref(false);
const folderFailed = ref(false);
async function loadFolders(): Promise<void> {
  if (folderContent.value || folderLoading.value) return;
  folderLoading.value = true;
  folderFailed.value = false;
  try {
    folderContent.value = (await import('./ProjectList.vue')).default;
  } catch {
    folderFailed.value = true;
  } finally {
    folderLoading.value = false;
  }
}
onMounted(() => void loadProjects().catch(() => {}));
watch(() => projectList.value.length > 0 || creatingFolder.value, (needed) => {
  if (needed) void loadFolders();
}, { immediate: true });

const atFolderMax = computed(() => projectMax.value > 0 && projectList.value.length >= projectMax.value);
const blank = computed(() => !conversations.value.length && !projectList.value.length && !creatingFolder.value);

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

// A dragged row over the rail but off every place it could be dropped lights
// nothing. The targets mark themselves on their own dragover, which reaches
// them before it bubbles here.
function onRailDragOver(event: DragEvent): void {
  cancelFileDrop(event);
  if (dropTarget.value === null) return;
  const over = event.target as Element | null;
  if (!over?.closest?.('.ai-drop-zone')) dropTarget.value = null;
}

// Leaving the rail altogether: a leave into one of its own children is only
// the pointer crossing a row, and the next dragover says where it is.
function onRailDragLeave(event: DragEvent): void {
  const into = event.relatedTarget as Node | null;
  if (into && (event.currentTarget as Node).contains(into)) return;
  dropTarget.value = null;
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

function returnToChat(): void {
  if (route?.path === '/terminal') void router?.push('/');
}

function onSelectConversation(id: string): void {
  void openConversation(id);
  returnToChat();
}

function onNewChat(): void {
  startNewConversation();
  returnToChat();
}
</script>

<template>
  <div class="ai-chat-sidebar" @dragover="onRailDragOver" @dragleave="onRailDragLeave" @drop="cancelFileDrop">
    <div class="ai-chat-sidebar-head">
      <div class="ai-rail-head-row">
        <button type="button" class="ai-chat-new ai-history-new-btn" @click="onNewChat">
          <span class="ai-chat-new-mark"><IconPlus :size="13" /></span>
          <span>{{ t('newChat') }}</span>
        </button>
        <OaIconButton
          class="ai-rail-folder-btn"
          :class="{ active: creatingFolder }"
          :label="t('newProject')"
          :disabled="atFolderMax && !creatingFolder"
          :aria-pressed="creatingFolder ? 'true' : 'false'"
          @click="toggleCreatingFolder"
        ><IconFolderPlus :size="15" /></OaIconButton>
      </div>
      <div v-if="conversations.length" class="ai-history-search-row">
        <OaSearchField v-model="query" class="oa-history-search" :label="t('searchHistory')" />
      </div>
    </div>

    <OaScrollArea wrap-class="ai-chat-list-wrap" scroll-class="ai-chat-list">
      <!-- Nothing yet is a moment, not an error: the matrix the wait for an
           answer pulses with, at rest, and a line on what will fill this. -->
      <div v-if="blank && !searching" class="ai-chat-list-blank">
        <span class="ai-dots ai-dots-rest" aria-hidden="true"><i v-for="n in 15" :key="n" /></span>
        <p class="ai-chat-list-blank-title">{{ t('noHistory') }}</p>
        <p class="ai-chat-list-blank-body">{{ t('noHistoryHint') }}</p>
      </div>

      <component
        :is="folderContent"
        v-if="folderContent"
        v-model:creating="creatingFolder"
        :searching="searching"
        @navigate="returnToChat"
      />
      <button v-else-if="folderFailed" type="button" class="oa-btn" @click="loadFolders">{{ t('retry') }}</button>

      <template v-if="searching">
        <p v-if="!filteredConversations.length" class="ai-chat-list-empty" role="status">{{ t('noSearchResults') }}</p>
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
        <!-- The search filters the rail, and a full read is only the latest
             conversations; without this an older one reads as not existing. -->
        <p v-if="railFull" class="ai-chat-list-empty" role="note">
          {{ t('searchRecentOnly', { count: RAIL_LIMIT }) }}
        </p>
      </template>

      <!-- The loose conversations, and the place a filed one is dropped to
           take it out of its folder. It runs to the foot of the list, so the
           whole of the room under the folders takes the drop. -->
      <section
        v-else-if="!blank"
        class="ai-rail-section ai-rail-loose ai-drop-zone"
        :class="{ 'drop-target': dropTarget === '' }"
        :aria-label="t('conversationsHeading')"
        @dragover="offerConversationDrop($event, '')"
        @drop="dropConversation($event, '')"
      >
        <!-- Only beside folders: over the one list there is, a heading says
             nothing the list does not. -->
        <div v-if="projectList.length" class="ai-rail-section-head">
          <span>{{ t('conversationsHeading') }}</span>
          <span class="ai-rail-count">{{ looseConversations.length }}</span>
        </div>
        <ConversationRow
          v-for="conversation in looseConversations"
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
      </section>
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
