<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaResizer from '@/components/OaResizer.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { matchesSearch } from '@/lib/search';
import { t } from '@/composables/useI18n';
import { usePanelHost } from '@/composables/usePanelHost';
import { IconArchive, IconEdit, IconMoreVertical, IconPlus, IconTrash } from '@/icons';
import ProjectList from './ProjectList.vue';
import {
  activeID, archiveConversation, canArchive, canDelete, clearEverything,
  conversations, openConversation, pendingID, removeConversation, rename,
  startNewConversation,
} from './useChat';

// The rail's width drives its own collapsed margin as well as its size, so
// the handle writes the custom property on the row rather than a width here.
const row = usePanelHost();

// Which list the rail shows. Local and unpersisted: it is a view of the
// rail, not a fact about the account, so it does not need to survive a
// reload the way the pending mode and project in workspace.ts do.
const activeTab = ref<'history' | 'projects'>('history');
const query = ref('');
const filteredConversations = computed(() => conversations.value.filter((entry) =>
  matchesSearch(query.value, entry.title || t('newChat'))));

const openUpMap = ref<Record<string, boolean>>({});

// Roughly three rows and their padding; the menu is measured against the list
// it hangs in, not the window, because that list clips it.
const MENU_HEIGHT = 124;

function handleMenuTrigger(event: MouseEvent, id: string, toggle: () => void): void {
  const target = event.currentTarget as HTMLElement | null;
  if (target) {
    const rect = target.getBoundingClientRect();
    const box = target.closest('.ai-chat-list')?.getBoundingClientRect();
    const below = (box ? box.bottom : window.innerHeight) - rect.bottom;
    const above = rect.top - (box ? box.top : 0);
    openUpMap.value[id] = below < MENU_HEIGHT && above > below;
    // A short list has room in neither direction, and the menu would sit
    // half under the list's edge. It does extend the list's scrollable area,
    // so bring it into view once its opening transition has sized it.
    if (below < MENU_HEIGHT && above < MENU_HEIGHT) {
      window.setTimeout(() => {
        target.closest('.ai-chat-list-item')
          ?.querySelector('.ai-chat-context-menu')
          ?.scrollIntoView({ block: 'nearest' });
      }, 180);
    }
  }
  toggle();
}

function onRowContextMenu(event: MouseEvent): void {
  const rowEl = event.currentTarget as HTMLElement | null;
  const moreBtn = rowEl?.querySelector<HTMLButtonElement>('.ai-chat-list-more');
  moreBtn?.click();
}

// A short rise on the row that just became current, so switching reads as a
// different conversation rather than the list quietly repainting.
const switched = ref(false);
let timer = 0;
watch(activeID, () => {
  switched.value = true;
  window.clearTimeout(timer);
  timer = window.setTimeout(() => { switched.value = false; }, 400);
});

let route: ReturnType<typeof useRoute> | undefined;
let router: ReturnType<typeof useRouter> | undefined;
try {
  route = useRoute();
  router = useRouter();
} catch {
  // outside router context in tests
}

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
  <div class="ai-chat-sidebar">
    <div class="ai-chat-sidebar-head">
      <!-- Replaces the old static "History" label: the tab a reader is on
           already says what the list below is, so the label and the switch
           are one control instead of two that could disagree. -->
      <div class="ai-sidebar-tabs" role="tablist">
        <span
          class="ai-sidebar-pill"
          :class="{ 'tab-projects': activeTab === 'projects' }"
          aria-hidden="true"
        />
        <button
          type="button"
          class="ai-sidebar-tab"
          :class="{ active: activeTab === 'history' }"
          role="tab"
          :aria-selected="activeTab === 'history'"
          @click="activeTab = 'history'"
        >{{ t('tabHistory') }}</button>
        <button
          type="button"
          class="ai-sidebar-tab"
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
        <button
          type="button"
          class="ai-history-new-btn"
          :title="t('newChat')"
          :aria-label="t('newChat')"
          @click="onNewChat"
        >
          <IconPlus :size="16" />
        </button>
      </div>

      <OaScrollArea wrap-class="ai-chat-list-wrap" scroll-class="ai-chat-list">
        <p v-if="!conversations.length" class="ai-chat-list-empty">{{ t('noHistory') }}</p>
        <p v-else-if="!filteredConversations.length" class="ai-chat-list-empty" role="status">{{ t('noSearchResults') }}</p>
        <div
          v-for="conversation in filteredConversations"
          :key="conversation.id"
          class="ai-chat-list-item"
          :class="{
            active: conversation.id === activeID,
            switched: conversation.id === activeID && switched,
          }"
          @contextmenu.prevent="onRowContextMenu($event)"
        >
          <button
            type="button"
            class="ai-chat-list-open"
            @click="onSelectConversation(conversation.id)"
            @dblclick="rename(conversation)"
          >
            <span class="ai-chat-list-title">{{ conversation.title || t('newChat') }}</span>
          </button>
          <!-- The row that is still being written into, so a composer that is
               busy while the reader is somewhere else has a visible reason. -->
          <span
            v-if="conversation.id === pendingID"
            class="ai-chat-list-live"
            :title="t('thinking')"
          ><span class="ai-chat-spinner" /></span>
          <OaMenu group-class="ai-chat-item-menu" :menu-class="'ai-chat-context-menu' + (openUpMap[conversation.id] ? ' open-up' : '')">
            <template #trigger="{ open: menuOpen, toggle }">
              <button
                type="button"
                class="ai-chat-list-more"
                :class="{ active: menuOpen }"
                :title="t('moreOptions')"
                :aria-label="t('moreOptions')"
                aria-haspopup="menu"
                :aria-expanded="menuOpen ? 'true' : 'false'"
                @click.stop="handleMenuTrigger($event, conversation.id, toggle)"
              >
                <IconMoreVertical :size="13" />
              </button>
            </template>
            <template #default="{ close }">
              <OaMenuItem :title="t('rename')" @click.stop="close(); rename(conversation)">
                <template #leading><IconEdit :size="13" /></template>
              </OaMenuItem>
              <OaMenuItem v-if="canArchive" :title="t('archive')" @click.stop="close(); archiveConversation(conversation)">
                <template #leading><IconArchive :size="13" /></template>
              </OaMenuItem>
              <OaMenuItem v-if="canDelete" :title="t('deleteChat')" @click.stop="close(); removeConversation(conversation)">
                <template #leading><IconTrash :size="13" /></template>
              </OaMenuItem>
            </template>
          </OaMenu>
        </div>
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
