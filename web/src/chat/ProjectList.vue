<script setup lang="ts">
// The projects tab's contents: create, open, edit and delete, all as one
// list rather than a screen of its own — a project is a name and a brief,
// small enough that a side panel for it would be more chrome than content.

import { computed, nextTick, onMounted, ref, watch } from 'vue';
import type { Project } from '@/api/projects';
import { ApiError } from '@/api/client';
import { listConversations, type Conversation } from '@/api/chat';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import {
  IconArchive, IconCheck, IconChevronRight, IconEdit, IconGear,
  IconMoreVertical, IconPlus, IconTrash,
} from '@/icons';
import {
  createProject, deleteProject, loadProjects, projectList, projectMax,
  renameProject, updateProjectInstructions,
} from '@/stores/projects';
import { pendingProjectID, setProject } from '@/stores/workspace';
// Shared with the rest of the chat rather than a second banner of its own:
// a failed rename and a failed send are the same kind of interruption, and
// the reader is already watching this one spot for it.
import {
  activeID, archiveConversation, canArchive, canDelete, conversations,
  flash, openConversation, pendingID, removeConversation, rename,
  startProjectConversation,
} from './useChat';

onMounted(() => void loadProjects());

const atMax = computed(() => projectMax.value > 0 && projectList.value.length >= projectMax.value);
const busy = ref(false);

function reportFailure(error: unknown): void {
  flash.value = error instanceof ApiError ? error.message : t('failed');
}

// --- project expansion and conversation list ---------------------------------

const expandedProjects = ref<Record<string, boolean>>({});
const projectConversations = ref<Record<string, Conversation[]>>({});
const loadingProjects = ref<Record<string, boolean>>({});
const openUpMap = ref<Record<string, boolean>>({});

async function loadProjectConversations(projectID: string): Promise<void> {
  loadingProjects.value[projectID] = true;
  try {
    const { conversations: list } = await listConversations({ project_id: projectID, archived: false });
    projectConversations.value[projectID] = list;
  } catch (error) {
    reportFailure(error);
  } finally {
    loadingProjects.value[projectID] = false;
  }
}

function toggleExpand(project: Project): void {
  setProject(project.id);
  expandedProjects.value[project.id] = !expandedProjects.value[project.id];
  if (expandedProjects.value[project.id]) {
    void loadProjectConversations(project.id);
  }
}

function createProjectChat(projectID: string): void {
  setProject(projectID);
  startProjectConversation(projectID);
}

function openProjectConv(projectID: string, convID: string): void {
  setProject(projectID);
  void openConversation(convID);
}

async function archiveProjectConv(project: Project, conv: Conversation): Promise<void> {
  await archiveConversation(conv);
  if (projectConversations.value[project.id]) {
    projectConversations.value[project.id] = projectConversations.value[project.id]!.filter(
      (entry) => entry.id !== conv.id,
    );
  }
  project.conversations = Math.max(0, project.conversations - 1);
}

async function removeProjectConv(project: Project, conv: Conversation): Promise<void> {
  await removeConversation(conv);
  if (projectConversations.value[project.id]) {
    projectConversations.value[project.id] = projectConversations.value[project.id]!.filter(
      (entry) => entry.id !== conv.id,
    );
  }
  project.conversations = Math.max(0, project.conversations - 1);
}

function handleMenuTrigger(event: MouseEvent, id: string, toggle: () => void): void {
  const target = event.currentTarget as HTMLElement | null;
  if (target) {
    const rect = target.getBoundingClientRect();
    openUpMap.value[id] = (window.innerHeight - rect.bottom) < 160;
  }
  toggle();
}

function onRowContextMenu(event: MouseEvent): void {
  const rowEl = event.currentTarget as HTMLElement | null;
  const moreBtn = rowEl?.querySelector<HTMLButtonElement>('.ai-chat-list-more');
  moreBtn?.click();
}

watch(conversations, () => {
  for (const pID of Object.keys(expandedProjects.value)) {
    if (expandedProjects.value[pID]) {
      void loadProjectConversations(pID);
    }
  }
}, { deep: true });

// --- create ------------------------------------------------------------------

const creating = ref(false);
const createName = ref('');
const createInstructions = ref('');
const createNameField = ref<InstanceType<typeof OaTextField> | null>(null);

function toggleCreate(): void {
  creating.value = !creating.value;
  if (!creating.value) return;
  editingID.value = '';
  createName.value = '';
  createInstructions.value = '';
  void nextTick(() => createNameField.value?.focus());
}

async function submitCreate(): Promise<void> {
  const name = createName.value.trim();
  if (!name) return;
  busy.value = true;
  try {
    // Taken straight into the project just made: creating one and then
    // having to find and click it again would be the same gesture twice for
    // one intention.
    const record = await createProject(name, createInstructions.value.trim());
    creating.value = false;
    setProject(record.id);
    expandedProjects.value[record.id] = true;
    void loadProjectConversations(record.id);
  } catch (error) {
    reportFailure(error);
  } finally {
    busy.value = false;
  }
}

// --- edit: rename and the standing instructions share one form, the way
// KeysPanel treats an edit as the same row looked at more closely rather
// than a second column ---------------------------------------------------

const editingID = ref('');
const editName = ref('');
const editInstructions = ref('');
const editNameField = ref<InstanceType<typeof OaTextField> | null>(null);

function beginEdit(project: Project): void {
  creating.value = false;
  editingID.value = project.id;
  editName.value = project.name;
  editInstructions.value = project.instructions;
  void nextTick(() => editNameField.value?.focus());
}

function cancelEdit(): void {
  editingID.value = '';
}

async function submitEdit(project: Project): Promise<void> {
  const name = editName.value.trim();
  if (!name) return;
  busy.value = true;
  try {
    // Two PATCHes, not one, because the store exposes what the server does:
    // a name change and an instructions change are separate requests, and
    // sending the one that did not change would just be a no-op write.
    if (name !== project.name) await renameProject(project.id, name);
    if (editInstructions.value !== project.instructions) {
      await updateProjectInstructions(project.id, editInstructions.value);
    }
    editingID.value = '';
  } catch (error) {
    reportFailure(error);
  } finally {
    busy.value = false;
  }
}

async function remove(project: Project): Promise<void> {
  try {
    await deleteProject(project.id);
    delete expandedProjects.value[project.id];
    delete projectConversations.value[project.id];
    if (pendingProjectID.value === project.id) setProject('');
  } catch (error) {
    reportFailure(error);
  }
}
</script>

<template>
  <div class="ai-project-panel">
    <button
      type="button"
      class="ai-chat-new"
      :disabled="atMax && !creating"
      @click="toggleCreate"
    >
      <IconPlus :size="14" />
      <span>{{ t('newProject') }}</span>
    </button>

    <p v-if="atMax" class="ai-chat-list-empty">{{ t('projectsFull') }}</p>

    <div v-if="creating" class="ai-project-form">
      <OaTextField
        ref="createNameField"
        v-model="createName"
        :label="t('projectName')"
        :placeholder="t('projectNamePlaceholder')"
        :max-length="80"
      />
      <OaTextArea
        v-model="createInstructions"
        :label="t('projectInstructions')"
        :hint="t('projectInstructionsHint')"
        :rows="4"
      />
      <div class="oa-key-edit-actions">
        <button type="button" class="oa-btn" @click="creating = false">{{ t('cancel') }}</button>
        <button type="button" class="oa-btn primary" :disabled="busy || !createName.trim()" @click="submitCreate">
          {{ t('saveProject') }}
        </button>
      </div>
    </div>

    <OaScrollArea wrap-class="ai-chat-list-wrap" scroll-class="ai-chat-list">
      <p v-if="!projectList.length" class="ai-chat-list-empty">{{ t('noProjects') }}</p>
      <template v-for="project in projectList" :key="project.id">
        <div
          v-if="editingID !== project.id"
          class="ai-chat-list-item"
          :class="{ active: project.id === pendingProjectID }"
        >
          <button type="button" class="ai-chat-list-open" :title="t('openProject')" @click="toggleExpand(project)">
            <span class="ai-project-title-row">
              <IconChevronRight
                :size="12"
                class="ai-project-chevron"
                :class="{ expanded: expandedProjects[project.id] }"
              />
              <span class="ai-chat-list-title">{{ project.name }}</span>
            </span>
            <span class="ai-project-meta">{{ project.conversations }} {{ t('projectConversations') }}</span>
          </button>
          <button
            type="button"
            class="ai-project-action-btn"
            :title="t('editProject')"
            :aria-label="t('editProject')"
            @click="beginEdit(project)"
          ><IconGear :size="13" /></button>
          <OaConfirmButton
            class="ai-project-action-btn danger"
            :resting-title="t('deleteProject')"
            :armed-title="t('deleteProjectConfirm')"
            @confirm="remove(project)"
          >
            <IconTrash :size="13" />
            <template #armed><IconCheck :size="13" /></template>
          </OaConfirmButton>
        </div>

        <div v-else class="ai-project-form">
          <OaTextField
            ref="editNameField"
            v-model="editName"
            :label="t('projectName')"
            :placeholder="t('projectNamePlaceholder')"
            :max-length="80"
          />
          <OaTextArea
            v-model="editInstructions"
            :label="t('projectInstructions')"
            :hint="t('projectInstructionsHint')"
            :rows="4"
          />
          <div class="oa-key-edit-actions">
            <button type="button" class="oa-btn" @click="cancelEdit">{{ t('cancel') }}</button>
            <button type="button" class="oa-btn primary" :disabled="busy || !editName.trim()" @click="submitEdit(project)">
              {{ t('saveProject') }}
            </button>
          </div>
        </div>

        <!-- Expanded project conversations -->
        <div
          v-if="editingID !== project.id"
          class="ai-project-convs-collapse"
          :class="{ open: !!expandedProjects[project.id] }"
        >
          <div class="ai-project-convs-inner">
            <div class="ai-project-convs-wrap">
              <button
                type="button"
                class="ai-project-new-chat"
                :title="t('newConversationInProject')"
                @click.stop="createProjectChat(project.id)"
              >
                <IconPlus :size="12" />
                <span>{{ t('newConversationInProject') }}</span>
              </button>

              <span v-if="loadingProjects[project.id]" class="ai-chat-list-live">
                <span class="ai-chat-spinner" />
              </span>

              <template v-else-if="projectConversations[project.id]?.length">
                <div
                  v-for="conv in projectConversations[project.id]"
                  :key="conv.id"
                  class="ai-chat-list-item ai-project-conv-item"
                  :class="{ active: conv.id === activeID }"
                  @contextmenu.prevent="onRowContextMenu($event)"
                >
                  <button
                    type="button"
                    class="ai-chat-list-open"
                    @click="openProjectConv(project.id, conv.id)"
                    @dblclick="rename(conv)"
                  >
                    <span class="ai-chat-list-title">{{ conv.title || t('newChat') }}</span>
                  </button>
                  <span
                    v-if="conv.id === pendingID"
                    class="ai-chat-list-live"
                    :title="t('thinking')"
                  ><span class="ai-chat-spinner" /></span>
                  <OaMenu group-class="ai-chat-item-menu" :menu-class="'ai-chat-context-menu' + (openUpMap[conv.id] ? ' open-up' : '')">
                    <template #trigger="{ open: menuOpen, toggle }">
                      <button
                        type="button"
                        class="ai-chat-list-more"
                        :class="{ active: menuOpen }"
                        :title="t('moreOptions')"
                        :aria-label="t('moreOptions')"
                        aria-haspopup="menu"
                        :aria-expanded="menuOpen ? 'true' : 'false'"
                        @click.stop="handleMenuTrigger($event, conv.id, toggle)"
                      >
                        <IconMoreVertical :size="13" />
                      </button>
                    </template>
                    <template #default="{ close }">
                      <OaMenuItem :title="t('rename')" @click.stop="close(); rename(conv)">
                        <template #leading><IconEdit :size="13" /></template>
                      </OaMenuItem>
                      <OaMenuItem v-if="canArchive" :title="t('archive')" @click.stop="close(); archiveProjectConv(project, conv)">
                        <template #leading><IconArchive :size="13" /></template>
                      </OaMenuItem>
                      <OaMenuItem v-if="canDelete" :title="t('deleteChat')" @click.stop="close(); removeProjectConv(project, conv)">
                        <template #leading><IconTrash :size="13" /></template>
                      </OaMenuItem>
                    </template>
                  </OaMenu>
                </div>
              </template>
              <p v-else class="ai-project-empty-convs">{{ t('noProjectConversations') }}</p>
            </div>
          </div>
        </div>
      </template>
    </OaScrollArea>
  </div>
</template>
