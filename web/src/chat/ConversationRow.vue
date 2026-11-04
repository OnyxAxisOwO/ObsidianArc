<script setup lang="ts">
// One conversation in a list — the rail's loose ones, or a folder's. A row of
// its own so that whether it is the open one, or the one still being written
// into, arrives as a prop: switching conversations then redraws the two rows
// that changed rather than every row in the list.
//
// Renaming happens in the row, as a project's edit does in its own: the
// browser's prompt() is suppressed by some browsers and by the preview pane,
// which left a menu item that did nothing.
//
// The row is also what is dragged into a folder. The menu's "move to folder"
// is the same move without a pointer — a keyboard, a screen reader, a phone,
// where a drag is a scroll — so it lists the folders in place rather than
// sending the reader somewhere else to pick one.

import { computed, nextTick, onBeforeUnmount, ref } from 'vue';
import type { Conversation } from '@/api/chat';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import {
  IconArchive, IconChevronRight, IconEdit, IconFolder, IconFolderOutput, IconMoreVertical, IconTrash,
} from '@/icons';
import { projectList } from '@/stores/projects';
import {
  canArchive, canDelete, draggedConversation, endConversationDrag, moveConversation, rename, renamingID,
  setFlash, startConversationDrag,
} from './useChat';
import type { RowMenu } from './useRowMenu';

const props = defineProps<{
  conversation: Conversation;
  menu: RowMenu;
  active: boolean;
  pending: boolean;
  switched?: boolean;
}>();

const emit = defineEmits<{ open: []; archive: []; remove: [] }>();

const filedIn = computed(() => props.conversation.project_id || '');
const folderChoices = computed(() => projectList.value.filter((project) => project.id !== filedIn.value));
const dragging = computed(() => draggedConversation.value?.id === props.conversation.id);

// Whether the menu shows the folders. Folded again each time the menu opens,
// so the menu a reader opens is always the short one.
const picking = ref(false);

function openMenu(event: MouseEvent, toggle: () => void): void {
  picking.value = false;
  props.menu.trigger(event, props.conversation.id, toggle);
}

function togglePicking(event: MouseEvent): void {
  picking.value = !picking.value;
  if (!picking.value) return;
  // The list grows the menu past where it was placed to fit, and the rail
  // clips it, so the grown menu is brought into view the way a short list's is.
  const menuEl = (event.currentTarget as HTMLElement | null)?.closest('.ai-chat-context-menu');
  void nextTick(() => menuEl?.scrollIntoView({ block: 'nearest' }));
}

const rowEl = ref<HTMLElement | null>(null);

// What is in the document but not on screen: a closed folder keeps its rows
// mounted for the slide, and a search hides the folders rather than
// unmounting them. Focus given to either goes nowhere a reader can see.
const HIDDEN = '.ai-project-convs-collapse:not(.open), .ai-folder-section[style*="display: none"]';

/**
 * Where keyboard focus goes after a move from the menu. The item that was
 * pressed is gone with the menu, and usually the row with it, which drops
 * focus to the page: the row in its new place takes it if it is drawn there,
 * else the folder it went into, else the folder it left.
 */
function refocus(rail: Element | null, id: string, into: string, from: string): void {
  const now = document.activeElement;
  if (!rail || (now && now !== document.body && now.isConnected)) return;
  // Matched by dataset rather than spliced into a selector, so no id has to
  // be escaped to be found.
  const within = (selector: string, attribute: 'conversationId' | 'folderId', value: string, inner: string) => {
    if (!value) return null;
    const holder = [...rail.querySelectorAll<HTMLElement>(selector)].find((node) => node.dataset[attribute] === value
      && !node.closest(HIDDEN));
    return holder?.querySelector<HTMLElement>(inner) ?? null;
  };
  const target = within('[data-conversation-id]', 'conversationId', id, '.ai-chat-list-open')
    ?? within('.ai-folder[data-folder-id]', 'folderId', into, '.ai-folder-open')
    ?? within('.ai-folder[data-folder-id]', 'folderId', from, '.ai-folder-open');
  target?.focus();
}

async function moveTo(projectID: string, close: () => void): Promise<void> {
  // Read before the move: the row may be unmounted by the time it is answered.
  const id = props.conversation.id;
  const from = filedIn.value;
  const rail = rowEl.value?.closest('.ai-chat-list') ?? null;
  close();
  if (!(await moveConversation(id, projectID, from))) return;
  const folder = projectList.value.find((project) => project.id === projectID);
  // Said in the chat's live region, so a reader who cannot see the row leave
  // hears that it went, and where.
  setFlash(projectID ? t('movedToFolder', { name: folder?.name ?? '' }) : t('movedOutOfFolder'));
  await nextTick();
  refocus(rail, id, projectID, from);
}

const draft = ref('');
const field = ref<InstanceType<typeof OaTextField> | null>(null);

function beginRename(): void {
  draft.value = props.conversation.title;
  renamingID.value = props.conversation.id;
  void nextTick(() => field.value?.focus());
}

function submitRename(): void {
  void rename(props.conversation, draft.value);
}

function onRenameKey(event: KeyboardEvent): void {
  // An input method's Enter picks a candidate; it is not the reader done typing.
  if (event.key === 'Enter' && !event.isComposing) {
    event.preventDefault();
    submitRename();
  } else if (event.key === 'Escape') {
    // Prevented, so a panel open beside the rail leaves the key to this field.
    event.preventDefault();
    renamingID.value = '';
  }
}

// A row that goes mid-edit (moved to another folder, filtered out by a search,
// its folder closed, the list replaced) must not leave its edit for a row that
// appears later with the same id.
onBeforeUnmount(() => {
  if (renamingID.value === props.conversation.id) renamingID.value = '';
});
</script>

<template>
  <div v-if="renamingID === props.conversation.id" class="ai-project-form">
    <OaTextField
      ref="field"
      v-model="draft"
      :label="t('renamePrompt')"
      :max-length="120"
      @keydown="onRenameKey"
      @focusout="submitRename"
    />
  </div>
  <div
    v-else
    ref="rowEl"
    class="ai-chat-list-item"
    :class="{ active: props.active, switched: props.switched, dragging }"
    :data-conversation-id="props.conversation.id"
    draggable="true"
    @dragstart="startConversationDrag($event, props.conversation)"
    @dragend="endConversationDrag"
    @contextmenu.prevent="props.menu.contextMenu($event)"
  >
    <button
      type="button"
      class="ai-chat-list-open"
      @click="emit('open')"
      @dblclick="beginRename"
    >
      <span class="ai-chat-list-title">{{ props.conversation.title || t('newChat') }}</span>
    </button>
    <!-- The row that is still being written into, so a composer that is
         busy while the reader is somewhere else has a visible reason. -->
    <span
      v-if="props.pending"
      class="ai-chat-list-live"
      :title="t('thinking')"
    ><span class="ai-chat-spinner" /></span>
    <OaMenu
      group-class="ai-chat-item-menu"
      :menu-class="'ai-chat-context-menu' + (props.menu.openUp.value[props.conversation.id] ? ' open-up' : '')"
      :up="!!props.menu.openUp.value[props.conversation.id]"
    >
      <template #trigger="{ open: menuOpen, toggle }">
        <OaIconButton
          class="ai-chat-list-more"
          :class="{ active: menuOpen }"
          :label="t('moreOptions')"
          aria-haspopup="menu"
          :aria-expanded="menuOpen ? 'true' : 'false'"
          @click.stop="openMenu($event, toggle)"
        >
          <IconMoreVertical :size="13" />
        </OaIconButton>
      </template>
      <template #default="{ close }">
        <OaMenuItem :title="t('rename')" @click.stop="close(); beginRename()">
          <template #leading><IconEdit :size="13" /></template>
        </OaMenuItem>
        <OaMenuItem
          v-if="folderChoices.length"
          :class="{ active: picking }"
          :title="t('moveToFolder')"
          :aria-expanded="picking ? 'true' : 'false'"
          @click.stop="togglePicking"
        >
          <template #leading><IconFolder :size="13" /></template>
          <template #trailing>
            <IconChevronRight :size="12" class="ai-chat-menu-chevron" :class="{ expanded: picking }" />
          </template>
        </OaMenuItem>
        <!-- The folders hang under the item that unfolded them, on a hairline,
             with their names in the menu's one title column. Many of them
             scroll inside a bounded list on the overlay track, so the menu
             itself never grows a scrollbar that would take width. -->
        <OaScrollArea
          v-if="picking"
          wrap-class="ai-chat-menu-folders-wrap"
          scroll-class="ai-chat-menu-folders"
        >
          <OaMenuItem
            v-for="folder in folderChoices"
            :key="folder.id"
            class="ai-chat-menu-folder"
            :title="folder.name"
            @click.stop="moveTo(folder.id, close)"
          >
            <template #leading><IconFolder :size="13" class="ai-chat-menu-folder-glyph" /></template>
          </OaMenuItem>
        </OaScrollArea>
        <OaMenuItem v-if="filedIn" :title="t('moveOutOfFolder')" @click.stop="moveTo('', close)">
          <template #leading><IconFolderOutput :size="13" /></template>
        </OaMenuItem>
        <OaMenuItem v-if="canArchive" :title="t('archive')" @click.stop="close(); emit('archive')">
          <template #leading><IconArchive :size="13" /></template>
        </OaMenuItem>
        <OaMenuItem
          v-if="canDelete"
          :title="props.menu.armed.value === props.conversation.id ? t('confirmDelete') : t('deleteChat')"
          @click.stop="props.menu.deleteFromMenu(props.conversation.id, close, () => emit('remove'))"
        >
          <template #leading><IconTrash :size="13" /></template>
        </OaMenuItem>
      </template>
    </OaMenu>
  </div>
</template>
