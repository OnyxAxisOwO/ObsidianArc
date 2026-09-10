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

import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconCollapse, IconExpand } from '@/icons';
import AccountSection from './settings/AccountSection.vue';
import AppearanceSection from './settings/AppearanceSection.vue';
import ChatSection from './settings/ChatSection.vue';
import WallpaperSection from './settings/WallpaperSection.vue';
import { matchesSettings, type SettingsGroup } from './settings/search';

type Category = 'appearance' | 'chat' | 'account';

const CATEGORIES: Array<{ id: Category; label: StringKey }> = [
  { id: 'appearance', label: 'secAppearance' },
  { id: 'chat', label: 'secChat' },
  { id: 'account', label: 'account' },
];

const router = useRouter();

const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const scroll = ref<InstanceType<typeof OaScrollArea> | null>(null);

const category = ref<Category>('appearance');
const query = ref('');
const searching = computed(() => !!query.value.trim());
const visibleGroups = computed(() => {
  const visible = (group: SettingsGroup, owner: Category) => searching.value
    ? matchesSettings(query.value, group)
    : category.value === owner;
  return {
    appearance: visible('appearance', 'appearance'),
    wallpaper: visible('wallpaper', 'appearance'),
    chat: visible('chat', 'chat'),
    account: (['profile', 'password', 'data'] as const).some((group) => visible(group, 'account')),
  };
});
watch(query, () => {
  const node = scroll.value?.scroller;
  if (node) node.scrollTop = 0;
});
const fullscreen = ref(false);
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

function toggleFullscreen(): void {
  fullscreen.value = panel.value?.toggleFullscreen() ?? false;
}
</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('settings')"
    :footer="false"
    :width="460"
    body-class="oa-settings-body"
    @close="router.replace('/')"
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
          <p v-if="!Object.values(visibleGroups).some(Boolean)" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>
          <!-- Keep drafts mounted while search temporarily hides their section. -->
          <AppearanceSection v-show="visibleGroups.appearance" />
          <WallpaperSection v-show="visibleGroups.wallpaper" />
          <ChatSection v-show="visibleGroups.chat" />
          <div v-show="visibleGroups.account" class="oa-settings-group">
            <AccountSection :query="query" />
          </div>
        </div>
      </OaScrollArea>
    </div>
  </OaPanel>
</template>
