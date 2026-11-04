<script setup lang="ts">
// The rail's folders: create, open, edit and delete, as one section of the
// rail's single list rather than a tab of their own. A folder is a project on
// the server — a name and a standing brief — and small enough that a side
// panel for it would be more chrome than content.
//
// Drawn inside the rail's scroll area, above the loose conversations, so the
// two scroll as one list and a row can be dragged from either into the other.
// Each folder, its row and its open list alike, takes a dropped row.

import { computed, nextTick, ref, watch } from 'vue';
import '@/styles/folders.scss';
import type { Project } from '@/api/projects';
import { apiErrorText } from '@/lib/api-error';
import { listConversations, type Conversation } from '@/api/chat';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconCheck, IconChevronRight, IconFolder, IconGear, IconPlus, IconTrash } from '@/icons';
import {
  createProject, deleteProject, projectList, projectMax,
  renameProject, updateProjectInstructions,
} from '@/stores/projects';
import { pendingProjectID, setProject } from '@/stores/workspace';
// Shared with the rest of the chat rather than a second banner of its own:
// a failed rename and a failed send are the same kind of interruption, and
// the reader is already watching this one spot for it.
import {
  activeID, archiveConversation, conversations, dropConversation, dropTarget,
  flash, lastMove, lastOpenedInFolder, offerConversationDrop, openConversation, pendingID,
  releaseConversations, removeConversation, startProjectConversation,
} from './useChat';
import ConversationRow from './ConversationRow.vue';
import { useRowMenu } from './useRowMenu';

const props = defineProps<{
  /** The create form is open. The rail's head holds the button that opens it. */
  creating?: boolean;
  /**
   * The rail is showing search results, which are drawn flat across every
   * folder. Hidden rather than unmounted, so the folders a reader had open
   * are still open when the search is cleared.
   */
  searching?: boolean;
}>();

const emit = defineEmits<{ 'update:creating': [value: boolean]; navigate: [] }>();

const atMax = computed(() => projectMax.value > 0 && projectList.value.length >= projectMax.value);
const shown = computed(() => !props.searching && (projectList.value.length > 0 || !!props.creating));
const busy = ref(false);
const editingID = ref('');

function reportFailure(error: unknown): void {
  flash.value = apiErrorText(error);
}

// --- folder expansion and conversation list ----------------------------------

// A collapsed folder's rows stay mounted, and loaded, until this list goes.
// The grid slide is what closes a folder, and rows unmounted with it would
// take an open row menu down with them and cut the slide short. Opening again
// shows the rows already loaded and refreshes them behind.
const expandedProjects = ref<Record<string, boolean>>({});
const projectConversations = ref<Record<string, Conversation[]>>({});
const loadingProjects = ref<Record<string, boolean>>({});
// Made in setup, as the rail's is: the armed delete it holds is cleared with this list.
const rowMenu = useRowMenu();

// The watch below refetches on every change to the conversations, so two
// requests for one folder can overlap. Only the newest may write: an older
// response that arrives last would otherwise put back a list that is already
// out of date.
const latestLoad: Record<string, number> = {};

async function loadProjectConversations(projectID: string): Promise<void> {
  const load = (latestLoad[projectID] ?? 0) + 1;
  latestLoad[projectID] = load;
  const current = () => latestLoad[projectID] === load;
  // The spinner is for a folder with nothing to show yet. The watch below
  // refreshes every open folder after each turn, and swapping a list already
  // on screen for a spinner flashed it — and unmounted any row menu open in it.
  if (!projectConversations.value[projectID]) loadingProjects.value[projectID] = true;
  try {
    const { conversations: list } = await listConversations({ project_id: projectID, archived: false });
    if (current()) projectConversations.value[projectID] = list;
  } catch (error) {
    if (current()) reportFailure(error);
  } finally {
    if (current()) loadingProjects.value[projectID] = false;
  }
}

function toggleExpand(project: Project): void {
  expandedProjects.value[project.id] = !expandedProjects.value[project.id];
  if (expandedProjects.value[project.id]) {
    // Opening a folder makes it the one the next chat goes into. Closing it
    // only tidies the list, so it must not change where new work lands.
    setProject(project.id);
    void loadProjectConversations(project.id);
  }
}

function createProjectChat(projectID: string): void {
  setProject(projectID);
  startProjectConversation(projectID);
  emit('navigate');
}

function openProjectConv(projectID: string, convID: string): void {
  setProject(projectID);
  void openConversation(convID);
  emit('navigate');
}

// The list and its count change only when the server has changed the
// conversation. A refused request leaves the row where it was, and the
// failure is already in the flash.
function dropProjectConv(project: Project, conv: Conversation): void {
  if (projectConversations.value[project.id]) {
    projectConversations.value[project.id] = projectConversations.value[project.id]!.filter(
      (entry) => entry.id !== conv.id,
    );
  }
}

async function archiveProjectConv(project: Project, conv: Conversation): Promise<void> {
  if (!(await archiveConversation(conv))) return;
  dropProjectConv(project, conv);
}

async function removeProjectConv(project: Project, conv: Conversation): Promise<void> {
  if (!(await removeConversation(conv))) return;
  dropProjectConv(project, conv);
  // The server counts archived conversations too, so only deletion subtracts.
  project.conversations = Math.max(0, project.conversations - 1);
}

watch(conversations, () => {
  for (const pID of Object.keys(expandedProjects.value)) {
    if (expandedProjects.value[pID]) {
      void loadProjectConversations(pID);
    }
  }
}, { deep: true });

// A move the server accepted. The row leaves the folder it left at once — a
// collapsed folder's list too, or opening it would show the row for a moment
// before the refresh behind took it away — and the folder it went to, if its
// list has been read, is read again to take it in. A read of the folder it
// left that is still in flight began before the move and would put the row
// back, so it is outdated: an open folder is read again, which outdates it the
// way any newer read does, and a collapsed one is read when it is next opened.
// A folder the row was never in keeps its reads: dropping one would leave
// that folder's list as stale as the read was meant to correct.
watch(lastMove, (move) => {
  if (!move) return;
  const left = new Set<string>();
  if (move.from) left.add(move.from);
  for (const [pID, list] of Object.entries(projectConversations.value)) {
    if (list.some((entry) => entry.id === move.id)) left.add(pID);
  }
  left.delete(move.projectID);
  for (const pID of left) {
    const list = projectConversations.value[pID];
    if (list) projectConversations.value[pID] = list.filter((entry) => entry.id !== move.id);
    if (expandedProjects.value[pID]) {
      void loadProjectConversations(pID);
    } else {
      latestLoad[pID] = (latestLoad[pID] ?? 0) + 1;
      // An outdated read no longer clears its own spinner.
      loadingProjects.value[pID] = false;
    }
  }
  if (move.projectID && projectConversations.value[move.projectID]) {
    void loadProjectConversations(move.projectID);
  }
});

// A turn opened a conversation in this folder. It is the conversation being
// written into, so its folder opens to show it, collapsed or not, and is read
// again to take it in.
watch(lastOpenedInFolder, (opened) => {
  if (!opened || !projectList.value.some((project) => project.id === opened.projectID)) return;
  expandedProjects.value[opened.projectID] = true;
  void loadProjectConversations(opened.projectID);
}, { immediate: true });

// --- create ------------------------------------------------------------------

const createName = ref('');
const createInstructions = ref('');
const createNameField = ref<InstanceType<typeof OaTextField> | null>(null);

watch(() => props.creating, (open) => {
  if (!open) return;
  editingID.value = '';
  createName.value = '';
  createInstructions.value = '';
  void nextTick(() => createNameField.value?.focus());
}, { immediate: true });

async function submitCreate(): Promise<void> {
  const name = createName.value.trim();
  if (!name) return;
  busy.value = true;
  try {
    // Taken straight into the folder just made: creating one and then
    // having to find and click it again would be the same gesture twice for
    // one intention.
    const record = await createProject(name, createInstructions.value.trim());
    emit('update:creating', false);
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

const editName = ref('');
const editInstructions = ref('');
const editNameField = ref<InstanceType<typeof OaTextField> | null>(null);

function beginEdit(project: Project): void {
  emit('update:creating', false);
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
    releaseConversations(project.id);
    if (pendingProjectID.value === project.id) setProject('');
  } catch (error) {
    reportFailure(error);
  }
}
</script>

<template>
  <section v-show="shown" class="ai-rail-section ai-folder-section" :aria-label="t('foldersHeading')">
    <div class="ai-rail-section-head">
      <span>{{ t('foldersHeading') }}</span>
      <span class="ai-rail-count">{{ projectList.length }}</span>
    </div>

    <p v-if="atMax" class="ai-folder-note">{{ t('projectsFull') }}</p>

    <div v-if="props.creating" class="ai-project-form">
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
        <button type="button" class="oa-btn" @click="emit('update:creating', false)">{{ t('cancel') }}</button>
        <button type="button" class="oa-btn primary" :disabled="busy || !createName.trim()" @click="submitCreate">
          {{ t('saveProject') }}
        </button>
      </div>
    </div>

    <div
      v-for="project in projectList"
      :key="project.id"
      class="ai-folder ai-drop-zone"
      :class="{ 'drop-target': dropTarget === project.id }"
      :data-folder-id="project.id"
      @dragover="offerConversationDrop($event, project.id)"
      @drop="dropConversation($event, project.id)"
    >
      <div
        v-if="editingID !== project.id"
        class="ai-chat-list-item ai-folder-row"
        :class="{ active: project.id === pendingProjectID }"
      >
        <!-- Named with its count: the figure beside it is drawn for the eye
             and hidden from a screen reader, which would hear a bare number. -->
        <button
          type="button"
          class="ai-chat-list-open ai-folder-open"
          :title="t('openProject')"
          :aria-label="t('folderSummary', { name: project.name, count: project.conversations })"
          :aria-expanded="expandedProjects[project.id] ? 'true' : 'false'"
          @click="toggleExpand(project)"
        >
          <IconChevronRight
            :size="12"
            class="ai-project-chevron"
            :class="{ expanded: expandedProjects[project.id] }"
          />
          <IconFolder :size="13" class="ai-folder-glyph" />
          <span class="ai-chat-list-title">{{ project.name }}</span>
        </button>
        <span
          class="ai-rail-count ai-folder-count"
          aria-hidden="true"
          :title="t('folderSummary', { name: project.name, count: project.conversations })"
        >{{ project.conversations }}</span>
        <!-- Laid over the end of the row under the pointer, on the row's own
             fill, rather than given a slot of their own: a slot would either
             sit empty beside the count or re-truncate the name each time the
             pointer crossed the row. -->
        <span class="ai-folder-actions">
          <OaIconButton
            class="ai-project-action-btn"
            :label="t('editProject')"
            @click="beginEdit(project)"
          ><IconGear :size="13" /></OaIconButton>
          <OaConfirmButton
            class="ai-project-action-btn danger"
            :resting-title="t('deleteProject')"
            :armed-title="t('deleteProjectConfirm')"
            @confirm="remove(project)"
          >
            <IconTrash :size="13" />
            <template #armed><IconCheck :size="13" /></template>
          </OaConfirmButton>
        </span>
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
              <ConversationRow
                v-for="conv in projectConversations[project.id]"
                :key="conv.id"
                class="ai-project-conv-item"
                :conversation="conv"
                :menu="rowMenu"
                :active="conv.id === activeID"
                :pending="conv.id === pendingID"
                @open="openProjectConv(project.id, conv.id)"
                @archive="archiveProjectConv(project, conv)"
                @remove="removeProjectConv(project, conv)"
              />
            </template>
            <p v-else class="ai-project-empty-convs">{{ t('noProjectConversations') }}</p>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
