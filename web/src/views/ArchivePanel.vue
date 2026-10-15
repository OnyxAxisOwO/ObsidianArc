<script setup lang="ts">
// The archive panel: view, search, restore and delete archived conversations.
//
// Matches the settings/keys design: opens as a right-side column panel,
// can be maximized to fullscreen, searchable, with immediate unarchive/delete.

import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { deleteConversation, listConversations, type Conversation } from '@/api/chat';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { IconArchive, IconCheck, IconCollapse, IconExpand, IconTrash } from '@/icons';
import { relativeTime } from '@/lib/format';
import { matchesSearch } from '@/lib/search';
import { activeID, canDelete, failureText, messages, openConversation, setFlash, unarchiveConversation } from '@/chat/useChat';

const router = useRouter();
const panels = usePanelExit();

const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const fullscreen = ref(false);

const PAGE_SIZE = 100;

const archivedList = ref<Conversation[]>([]);
const loading = ref(false);
const hasMore = ref(false);
const query = ref('');

// Archived rows the server has handed over and still holds as archived: the
// next page starts after them. Restoring or deleting a row moves the rest up,
// so the count goes down with it.
let listed = 0;

const filteredArchived = computed(() =>
  archivedList.value.filter((entry) =>
    matchesSearch(query.value, entry.title || t('newChat')),
  ),
);

// The server answers a page against the archive as it stands when the page
// runs. A restore or delete that lands first shifts the rows under the offset,
// so the page would skip one row and repeat another. Paging and changes wait
// for each other: neither starts while the other is pending.
const changing = ref(0);

async function loadArchived(): Promise<void> {
  if (loading.value || changing.value > 0) return;
  loading.value = true;
  try {
    const { conversations: list } = await listConversations({
      archived: true,
      limit: PAGE_SIZE,
      offset: listed,
    });
    archivedList.value = [...archivedList.value, ...list];
    listed += list.length;
    // A short page is the end of the archive; a full one may have more behind it.
    hasMore.value = list.length === PAGE_SIZE;
  } catch (error) {
    setFlash(failureText(error));
  } finally {
    loading.value = false;
  }
}

function dropRow(id: string): void {
  if (!archivedList.value.some((entry) => entry.id === id)) return;
  archivedList.value = archivedList.value.filter((entry) => entry.id !== id);
  listed -= 1;
}

async function restore(conversation: Conversation): Promise<void> {
  if (loading.value) return;
  changing.value += 1;
  try {
    // A conversation that failed to leave the archive is still archived, so it
    // stays listed here too.
    if (!(await unarchiveConversation(conversation.id))) return;
    dropRow(conversation.id);
  } finally {
    changing.value -= 1;
  }
}

async function remove(conversation: Conversation): Promise<void> {
  if (loading.value) return;
  changing.value += 1;
  try {
    await deleteConversation(conversation.id);
    dropRow(conversation.id);
    // Without clearing the transcript, the next send would append to the
    // deleted conversation's messages, as the chat store's own removal avoids.
    if (activeID.value === conversation.id) {
      activeID.value = '';
      messages.value = [];
    }
  } catch (error) {
    setFlash(failureText(error));
  } finally {
    changing.value -= 1;
  }
}

// Leaving for a conversation is a destination rather than a close, so it goes
// to the chat even from the backoffice: the conversation is only there.
async function openInChat(conversation: Conversation): Promise<void> {
  await openConversation(conversation.id);
  void router.push('/');
}

function toggleFullscreen(): void {
  fullscreen.value = panel.value?.toggleFullscreen() ?? false;
}

onMounted(() => {
  void loadArchived();
});
</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('archivedConversations')"
    :footer="false"
    :width="460"
    body-class="oa-settings-body"
    @close="panels.close('replace')"
  >
    <template #actions>
      <OaIconButton
        class="oa-icon-btn"
        :label="t(fullscreen ? 'exitFullscreen' : 'fullscreen')"
        @click="toggleFullscreen"
      >
        <IconCollapse v-if="fullscreen" :size="16" />
        <IconExpand v-else :size="16" />
      </OaIconButton>
    </template>

    <div class="oa-archive-search-bar">
      <OaSearchField v-model="query" class="oa-settings-search" :label="t('searchArchived')" />
    </div>

    <OaScrollArea wrap-class="oa-settings-scroll-wrap" scroll-class="oa-settings-scroll">
      <div class="oa-archive-content">
        <p v-if="loading && !archivedList.length" class="oa-search-empty">{{ t('thinking') }}</p>
        <p v-else-if="!archivedList.length" class="oa-search-empty">{{ t('noArchivedConversations') }}</p>
        <p v-else-if="!filteredArchived.length" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>

        <div v-else class="oa-card-list">
          <div
            v-for="conv in filteredArchived"
            :key="conv.id"
            class="oa-card-row oa-archive-row"
          >
            <button
              type="button"
              class="oa-archive-item-main"
              @click="openInChat(conv)"
            >
              <span class="oa-card-title">{{ conv.title || t('newChat') }}</span>
              <span class="oa-card-sub">{{ relativeTime(conv.updated_at) }}</span>
            </button>

            <div class="oa-archive-item-actions">
              <button
                type="button"
                class="oa-icon-btn"
                :title="t('unarchive')"
                :aria-label="t('unarchive')"
                :disabled="loading"
                @click.stop="restore(conv)"
              >
                <IconArchive :size="15" />
              </button>
              <OaConfirmButton
                v-if="canDelete"
                class="oa-icon-btn danger"
                :resting-title="t('deleteChat')"
                :armed-title="t('confirmDelete')"
                @confirm="remove(conv)"
              >
                <IconTrash :size="15" />
                <template #armed><IconCheck :size="15" /></template>
              </OaConfirmButton>
            </div>
          </div>
        </div>

        <div v-if="hasMore" class="oa-archive-load-more">
          <button type="button" class="oa-btn" :disabled="loading || changing > 0" @click="loadArchived">
            {{ loading ? t('thinking') : t('loadMoreArchived') }}
          </button>
        </div>
      </div>
    </OaScrollArea>
  </OaPanel>
</template>
