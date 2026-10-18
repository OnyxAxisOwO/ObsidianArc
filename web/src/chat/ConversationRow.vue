<script setup lang="ts">
// One conversation in a list — the history rail's, or a project's. A row of
// its own so that whether it is the open one, or the one still being written
// into, arrives as a prop: switching conversations then redraws the two rows
// that changed rather than every row in the list.
//
// Renaming happens in the row, as a project's edit does in its own: the
// browser's prompt() is suppressed by some browsers and by the preview pane,
// which left a menu item that did nothing.

import { nextTick, onBeforeUnmount, ref } from 'vue';
import type { Conversation } from '@/api/chat';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { IconArchive, IconEdit, IconMoreVertical, IconTrash } from '@/icons';
import { canArchive, canDelete, rename, renamingID } from './useChat';
import type { RowMenu } from './useRowMenu';

const props = defineProps<{
  conversation: Conversation;
  menu: RowMenu;
  active: boolean;
  pending: boolean;
  switched?: boolean;
}>();

const emit = defineEmits<{ open: []; archive: []; remove: [] }>();

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

// A row that goes mid-edit (the other tab chosen, the list replaced) must not
// leave its edit for a row that appears later with the same id.
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
    class="ai-chat-list-item"
    :class="{ active: props.active, switched: props.switched }"
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
    >
      <template #trigger="{ open: menuOpen, toggle }">
        <OaIconButton
          class="ai-chat-list-more"
          :class="{ active: menuOpen }"
          :label="t('moreOptions')"
          aria-haspopup="menu"
          :aria-expanded="menuOpen ? 'true' : 'false'"
          @click.stop="props.menu.trigger($event, props.conversation.id, toggle)"
        >
          <IconMoreVertical :size="13" />
        </OaIconButton>
      </template>
      <template #default="{ close }">
        <OaMenuItem :title="t('rename')" @click.stop="close(); beginRename()">
          <template #leading><IconEdit :size="13" /></template>
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
