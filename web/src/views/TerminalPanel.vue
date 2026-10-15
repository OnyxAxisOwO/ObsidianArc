<script setup lang="ts">
// The web terminal: CONTRACT.md #0/#5.
//
// A panel beside the chat, opened from the account menu, for every account
// whose group allows it. What each account can run is the server's answer,
// not this component's: the spec lists only the commands the caller may run,
// and every command is a request to an endpoint that checks the caller again
// — an ordinary account's terminal is its own screens, an administrator's is
// the backoffice as well. It used to be a backoffice section; it moved here
// when it stopped being only for administrators.
//
// Every tab is a `TerminalSession` (`terminal/session.ts`) — a line-oriented
// client of `/api/console/*` — so this component's only jobs are the tab
// strip, wiring appearance preferences to CSS custom properties, and
// disposing every tab's in-flight fetch when the panel goes away. Command
// execution, history and ANSI decoding all live one layer down.

import { computed, markRaw, nextTick, onBeforeUnmount, onMounted, ref, type Raw } from 'vue';
import { useRouter } from 'vue-router';
import { useEventListener } from '@vueuse/core';
import { fetchConsoleSpec, type ConsoleSpec } from '@/api/console';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { IconClose, IconGear, IconPlus } from '@/icons';
import { loadTerminalPrefs, saveTerminalPrefs, terminalCSSVariables, type TerminalPrefs } from '@/terminal/prefs';
import { createTerminalSession, type TerminalSession } from '@/terminal/session';
import TerminalPane from '@/terminal/TerminalPane.vue';
import TerminalSettings from '@/terminal/TerminalSettings.vue';

const router = useRouter();

interface TerminalTab {
  id: string;
  /** Creation order, for the default "Tab N" label — stable even after an
   * earlier tab closes, the way a shell's own tab numbers do not renumber. */
  serial: number;
  // `Raw<...>`, not `TerminalSession`: `tabs` below is a ref, and
  // `UnwrapRef` recurses into a plain object nested inside one, unwrapping
  // every `Ref` it finds — `title`, `draft`, `scrollback`, `running` — to
  // their plain value type. Declaring the field itself already raw is what
  // stops that at the type level; `markRaw()` in `createTab()` is the
  // matching runtime half.
  session: Raw<TerminalSession>;
}

const prefs = ref<TerminalPrefs>(loadTerminalPrefs());
/** Plain state, not a menu's: the appearance column is open or it is not. */
const settingsOpen = ref(false);
const cssVars = computed(() => terminalCSSVariables(prefs.value));

const spec = ref<ConsoleSpec | null>(null);
const tabs = ref<TerminalTab[]>([]);
const activeId = ref('');
const renamingId = ref<string | null>(null);
const renameDraft = ref('');
const renameInputRef = ref<HTMLInputElement | null>(null);

let nextTabSerial = 1;
let nextTabSeq = 0;
/**
 * Columns per tab, read by that tab's `getColumns()` closure at the moment a
 * command actually runs. A plain map rather than a ref: it is written by
 * `TerminalPane`'s resize observer on every layout change and only ever read
 * imperatively, so making it reactive would repaint the tab strip on every
 * pixel the pane resizes through.
 */
const columnsById = new Map<string, number>();

const activeTab = computed(() => tabs.value.find((tab) => tab.id === activeId.value) ?? null);

function tabLabel(tab: TerminalTab): string {
  return tab.session.title.value || t('terminalTabDefault', { n: tab.serial });
}

function createTab(): void {
  const id = `terminal-${nextTabSeq}`;
  nextTabSeq += 1;
  const serial = nextTabSerial;
  nextTabSerial += 1;
  // markRaw — see `TerminalTab.session`'s own comment for why. The session's
  // refs already do their own reactivity; a second, outer proxy over the
  // whole object would only duplicate that.
  const session = markRaw(createTerminalSession({
    id,
    banner: spec.value?.banner,
    scrollbackCap: prefs.value.scrollback,
    getColumns: () => columnsById.get(id) ?? 80,
  }));
  tabs.value.push({ id, serial, session });
  activeId.value = id;
}

function closeTab(id: string): void {
  const index = tabs.value.findIndex((tab) => tab.id === id);
  if (index === -1) return;
  const closed = tabs.value[index];
  tabs.value.splice(index, 1);
  closed?.session.dispose();
  columnsById.delete(id);
  if (activeId.value === id) {
    const fallback = tabs.value[index] ?? tabs.value[index - 1] ?? null;
    activeId.value = fallback?.id ?? '';
  }
  // A terminal with no tabs is a broken terminal, not an empty state —
  // CONTRACT.md #5.2 is explicit that closing the last one opens a fresh one.
  if (tabs.value.length === 0) createTab();
}

function activate(id: string): void {
  activeId.value = id;
}

/**
 * Arrow keys move between tabs the way a tablist does: the neighbour is
 * selected and keeps focus. A tab's parent is its pill, not the tablist, so the
 * neighbour is looked up from the tablist that owns every tab.
 */
function stepTab(event: KeyboardEvent, id: string, direction: 1 | -1): void {
  const index = tabs.value.findIndex((tab) => tab.id === id);
  const next = tabs.value[index + direction];
  if (!next) return;
  activate(next.id);
  const tablist = (event.currentTarget as HTMLElement).closest<HTMLElement>('[role="tablist"]');
  const target = tablist?.querySelectorAll<HTMLElement>('.oa-terminal-tab')[index + direction];
  // Focus after the redraw: the newly shown pane takes focus for its prompt
  // when it mounts, which would otherwise pull it off the tab.
  void nextTick(() => target?.focus());
}

// A plain string `ref` on a node inside a `v-for` collects into an array —
// useful when every iteration renders one, useless here where only the one
// tab actually being renamed ever has this `<input>` in the tree at all. A
// function ref sidesteps that collection and assigns the single element
// (or `null`, once renaming ends) directly.
function setRenameInputRef(node: unknown): void {
  renameInputRef.value = node instanceof HTMLInputElement ? node : null;
}

function startRename(tab: TerminalTab): void {
  renamingId.value = tab.id;
  renameDraft.value = tabLabel(tab);
  void nextTick(() => {
    renameInputRef.value?.focus();
    renameInputRef.value?.select();
  });
}

function commitRename(tab: TerminalTab): void {
  if (renamingId.value !== tab.id) return;
  // Empty restores the default "Tab N" label — the same meaning `session.ts`
  // already gives an empty `title`, so there is no separate "unset" state.
  tab.session.title.value = renameDraft.value.trim();
  renamingId.value = null;
}

function cancelRename(): void {
  renamingId.value = null;
}

function reportColumns(id: string, cols: number): void {
  columnsById.set(id, cols);
}

/**
 * `update:modelValue` (a drag in progress) repaints without touching
 * `localStorage`; `commit` does both — the same split `OaRangeField` uses,
 * carried one level up.
 */
function applyPrefs(next: TerminalPrefs, persist: boolean): void {
  prefs.value = next;
  for (const tab of tabs.value) tab.session.setScrollbackCap(next.scrollback);
  if (persist) saveTerminalPrefs(next);
}

function sshConnectHint(username: string, addr: string): string {
  // `addr` already carries its own leading colon (":2222", from Go's
  // net.Listen), and the browser knows its own host — between the two this
  // needs no server round trip to say something copy-pasteable.
  return t('terminalSSHHint', { user: username, host: window.location.hostname || 'host', addr });
}

// `key` is what Option makes of the letter on macOS (Option+T is a dagger), so
// it is read only when it is a letter, and the physical key stands in for it
// otherwise.
function letterOf(event: KeyboardEvent): string {
  if (/^[a-z]$/i.test(event.key)) return event.key.toLowerCase();
  return event.code.replace(/^Key/, '').toLowerCase();
}

// Alt, never Ctrl: CONTRACT.md #5.2 asks for tab shortcuts, and Ctrl+T/Ctrl+W
// are the browser's own tab commands — binding them here would either do
// nothing (the browser wins) or, worse, occasionally win and close the
// browser tab the terminal is running in.
useEventListener(window, 'keydown', (event: KeyboardEvent) => {
  if (!event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
  const key = letterOf(event);
  if (key === 't') {
    event.preventDefault();
    createTab();
  } else if (key === 'w' && activeTab.value) {
    event.preventDefault();
    closeTab(activeTab.value.id);
  }
});

onMounted(() => {
  void fetchConsoleSpec()
    .then((result) => {
      spec.value = result;
    })
    .catch(() => {
      // No spec is not fatal: `help`, `whoami` and every other command are
      // still one round trip away through `run()`. Only the banner and the
      // SSH badge, which come from this call, are missing.
      spec.value = null;
    })
    .finally(() => {
      if (tabs.value.length === 0) createTab();
    });
});

// Every tab's `AbortController` — an `exec` stream left running after this
// page unmounts would keep calling handlers that write into scrollback refs
// Vue has already discarded.
onBeforeUnmount(() => {
  for (const tab of tabs.value) tab.session.dispose();
});
</script>

<template>
  <!-- The widest a panel may be dragged, since a terminal is only useful when
       a line fits; full screen is one button away for anything wider. -->
  <OaPanel
    class="oa-terminal-panel"
    :title="t('navTerminal')"
    :footer="false"
    :width="720"
    :immediate-close="true"
    body-class="oa-terminal-body"
    @close="router.replace('/')"
  >

    <div class="oa-terminal" :style="cssVars">
      <div class="oa-terminal-tabstrip">
        <!-- role=tab is only valid inside a tablist, and this strip is the one
             that holds the tabs. -->
        <div class="oa-terminal-tabs" role="tablist" :aria-label="t('terminalTabList')">
          <!-- The pill is a presentational group, and the tab is only its label:
               ARIA gives a tab presentational children alone, so the × and the
               rename field sit beside the tab rather than inside it. -->
          <div
            v-for="tab in tabs"
            :key="tab.id"
            class="oa-terminal-tab-group"
            role="presentation"
            :class="{ active: tab.id === activeId, running: tab.session.running.value }"
            @mousedown.middle.prevent="closeTab(tab.id)"
          >
            <!-- v-show, not v-if: the tab element must survive a rename so the
                 label it holds is the same node when the rename ends. -->
            <div
              v-show="renamingId !== tab.id"
              class="oa-terminal-tab"
              role="tab"
              :tabindex="tab.id === activeId ? 0 : -1"
              :aria-selected="tab.id === activeId"
              :title="tabLabel(tab)"
              @click="activate(tab.id)"
              @dblclick="startRename(tab)"
              @keydown.enter.self.prevent="activate(tab.id)"
              @keydown.space.self.prevent="activate(tab.id)"
              @keydown.left.self.prevent="stepTab($event, tab.id, -1)"
              @keydown.right.self.prevent="stepTab($event, tab.id, 1)"
              @keydown.f2.self.prevent="startRename(tab)"
            >
              <span v-if="tab.session.running.value" class="oa-terminal-tab-dot" aria-hidden="true" />
              <span class="oa-terminal-tab-label">{{ tabLabel(tab) }}</span>
            </div>
            <span v-if="renamingId === tab.id" class="oa-terminal-tab-rename">
              <input
                :ref="setRenameInputRef"
                v-model="renameDraft"
                :aria-label="t('terminalRenameTab')"
                @keydown.enter.prevent="commitRename(tab)"
                @keydown.escape.prevent="cancelRename"
                @blur="commitRename(tab)"
              >
            </span>
            <OaIconButton
              class="oa-icon-btn oa-terminal-tab-close"
              :label="t('terminalCloseTab')"
              @click="closeTab(tab.id)"
            >
              <IconClose :size="10" />
            </OaIconButton>
          </div>
          <OaIconButton
            class="oa-icon-btn oa-terminal-tab-add"
            :label="t('terminalNewTab')"
            @click="createTab()"
          >
            <IconPlus :size="14" />
          </OaIconButton>
        </div>

        <!-- A marker, not a sentence — CONTRACT.md's own `SSH` / `READ-ONLY`
             precedent for `OaBadge`. Present only once the server confirms the
             listener is actually up. -->
        <span
          v-if="spec?.ssh.enabled"
          class="oa-badge oa-terminal-ssh-badge"
          :title="sshConnectHint(spec.you.username, spec.ssh.addr)"
        >SSH</span>

        <OaIconButton
          class="oa-icon-btn oa-terminal-settings-btn"
          :label="t('terminalSettings')"
          :aria-expanded="settingsOpen ? 'true' : 'false'"
          @click="settingsOpen = !settingsOpen"
        >
          <IconGear :size="16" />
        </OaIconButton>
      </div>

      <!-- A column of the chat row, not a sheet over the terminal: the same
           card the settings and keys screens open, so the terminal beside it
           narrows instead of being covered. It teleports out to the row itself,
           which is why it is written here rather than inside the strip the
           button lives in. -->
      <OaPanel
        v-if="settingsOpen"
        :title="t('terminalSettings')"
        :width="360"
        :footer="false"
        @close="settingsOpen = false"
      >
        <TerminalSettings
          :model-value="prefs"
          @update:model-value="applyPrefs($event, false)"
          @commit="applyPrefs($event, true)"
        />
      </OaPanel>

      <TerminalPane
        v-if="activeTab"
        :key="activeTab.id"
        :session="activeTab.session"
        :font-size="prefs.fontSize"
        :letter-spacing="prefs.letterSpacing"
        :timestamps="prefs.timestamps"
        @measure="(cols) => reportColumns(activeTab!.id, cols)"
      />
    </div>
  </OaPanel>
</template>
