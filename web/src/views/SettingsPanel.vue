<script setup lang="ts">
// User settings.
//
// Five sections is more than fits a panel without scrolling past what you
// came for, so they are grouped into three and shown one group at a time.
// Which group is a menu rather than a row of tabs: the panel head has room
// for one more control, not for three labels plus the two beside them. Full
// screen swaps the tab strip for a rail down the side, which is why the two
// navigations are both in the markup and one of them is always hidden.
//
// There is no footer. Each section commits on its own — the theme the moment
// it is picked, the profile and the password on their own buttons — so one
// Save at the bottom would be claiming to commit things it has nothing to do
// with.

import { computed, reactive, ref, watch } from 'vue';
import OaFullscreenButton from '@/components/OaFullscreenButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { usePanelFullscreen } from '@/composables/usePanelFullscreen';
import AccountSection from './settings/AccountSection.vue';
import AppearanceSection from './settings/AppearanceSection.vue';
import ChatSection from './settings/ChatSection.vue';
import InvitesSection from './settings/InvitesSection.vue';
import SecuritySection from './settings/SecuritySection.vue';
import { sectionMatches, type SettingsSection } from './settings/search';

type Category = 'appearance' | 'chat' | 'account' | 'security' | 'invites';

const CATEGORIES: Array<{ id: Category; label: StringKey }> = [
  { id: 'appearance', label: 'secAppearance' },
  { id: 'chat', label: 'secChat' },
  { id: 'account', label: 'account' },
  { id: 'security', label: 'secSecurity' },
  { id: 'invites', label: 'secInvites' },
];

const panels = usePanelExit();

// The tab a link names, given by whichever address opened this panel: the
// router reads `/settings?tab=`, the backoffice reads its own `panel` parameter.
// The panel never reads a query itself, because over the backoffice the page's
// own `tab` is a different thing.
const props = defineProps<{ tab?: string }>();

const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const scroll = ref<InstanceType<typeof OaScrollArea> | null>(null);

// A link can name the tab — the backoffice sends an administrator straight to
// the security one to turn on the second step a policy is asking for.
const category = ref<Category>(
  CATEGORIES.some((entry) => entry.id === props.tab) ? props.tab as Category : 'appearance',
);
const query = ref('');
watch(() => props.tab, (tab) => {
  // A second link to a different tab, clicked while the panel is already
  // open, does not remount it — the prop changes under a component that is
  // already running, so the initial value above only ever saw the first one.
  const known = CATEGORIES.find((entry) => entry.id === tab);
  if (known) select(known.id);
});
const searching = computed(() => !!query.value.trim());
// A section shows while a search runs when one of its groups matches, and
// otherwise when it is the open category. Which group sits in which section is
// kept in search.ts, beside the terms the search reads.
const visibleSections = computed(() => {
  const shown = (section: SettingsSection) => searching.value
    ? sectionMatches(query.value, section)
    : category.value === section;
  return {
    appearance: shown('appearance'),
    chat: shown('chat'),
    account: shown('account'),
    security: shown('security'),
    invites: shown('invites'),
  };
});

// A section is mounted the first time it shows, not when the panel opens: each
// one loads its own data on mount, and the reader is usually looking at one
// category. Once mounted it stays, so a draft survives a switch to another section.
// The account section is the exception: it takes the outcome of a connection from
// the address as it mounts, wherever the reader is, so it must mount with the panel;
// its lists still wait for it to show (its `active`).
const mounted = reactive<Record<SettingsSection, boolean>>({
  appearance: false,
  chat: false,
  account: true,
  security: false,
  invites: false,
});
watch(visibleSections, (shown) => {
  for (const section of Object.keys(shown) as SettingsSection[]) {
    if (shown[section]) mounted[section] = true;
  }
}, { immediate: true });
watch(query, () => {
  const node = scroll.value?.scroller;
  if (node) node.scrollTop = 0;
});
const { fullscreen, toggle: toggleFullscreen } = usePanelFullscreen(() => panel.value);
/** Which way the pane arrives, so a step back does not read as a step on. */
const direction = ref<'forward' | 'back' | 'rise'>('rise');

const paneClass = computed(() => `oa-settings enter-${direction.value}`);

function select(next: Category): void {
  query.value = '';
  if (next === category.value) return;
  const from = CATEGORIES.findIndex((entry) => entry.id === category.value);
  const to = CATEGORIES.findIndex((entry) => entry.id === next);
  direction.value = to > from ? 'forward' : 'back';
  category.value = next;
  const node = scroll.value?.scroller;
  if (node) node.scrollTop = 0;
}

</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('settings')"
    :footer="false"
    :width="460"
    body-class="oa-settings-body"
    @close="panels.close('replace')"
  >
    <template #actions>
      <OaFullscreenButton :fullscreen="fullscreen" @toggle="toggleFullscreen" />
    </template>

    <OaSearchField v-model="query" class="oa-settings-search" :label="t('searchSettings')" />

    <!-- Drawer view: a segmented control fixed at the top. -->
    <div class="oa-settings-tabs-bar">
      <nav class="oa-settings-tabs">
        <button
          v-for="entry in CATEGORIES"
          :key="entry.id"
          type="button"
          class="oa-settings-tab"
          :class="{ active: !searching && entry.id === category }"
          @click="select(entry.id)"
        >{{ t(entry.label) }}</button>
      </nav>
    </div>

    <!-- Full-screen view: a rail beside the scrolling form. -->
    <div class="oa-settings-split">
      <nav class="oa-settings-rail">
        <button
          v-for="entry in CATEGORIES"
          :key="entry.id"
          type="button"
          class="oa-settings-rail-item"
          :class="{ active: !searching && entry.id === category }"
          @click="select(entry.id)"
        >{{ t(entry.label) }}</button>
      </nav>

      <OaScrollArea
        ref="scroll"
        wrap-class="oa-settings-scroll-wrap"
        scroll-class="oa-settings-scroll"
      >
        <div :class="paneClass">
          <p v-if="!Object.values(visibleSections).some(Boolean)" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>
          <!-- Hidden with v-show rather than unmounted, so a search that hides a section
               does not throw away what was typed into it. -->
          <AppearanceSection v-if="mounted.appearance" v-show="visibleSections.appearance" />
          <ChatSection v-if="mounted.chat" v-show="visibleSections.chat" />
          <div v-show="visibleSections.account" class="oa-settings-group">
            <AccountSection v-if="mounted.account" :query="query" :active="visibleSections.account" />
          </div>
          <div v-show="visibleSections.security" class="oa-settings-group">
            <SecuritySection v-if="mounted.security" :query="query" />
          </div>
          <div v-show="visibleSections.invites" class="oa-settings-group">
            <InvitesSection v-if="mounted.invites" :query="query" />
          </div>
        </div>
      </OaScrollArea>
    </div>
  </OaPanel>
</template>
