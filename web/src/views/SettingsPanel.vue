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

import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { IconCollapse, IconExpand } from '@/icons';
import AccountSection from './settings/AccountSection.vue';
import AppearanceSection from './settings/AppearanceSection.vue';
import ChatSection from './settings/ChatSection.vue';
import WallpaperSection from './settings/WallpaperSection.vue';

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
const fullscreen = ref(false);
/** Which way the pane arrives, so a step back does not read as a step on. */
const direction = ref<'forward' | 'back' | 'rise'>('rise');

const paneClass = computed(() => `oa-settings enter-${direction.value}`);

function select(next: Category): void {
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

    <!-- Drawer view: a segmented control fixed at the top. -->
    <div class="oa-settings-tabs-bar">
      <nav class="oa-settings-tabs">
        <button
          v-for="entry in CATEGORIES"
          :key="entry.id"
          type="button"
          class="oa-settings-tab"
          :class="{ active: entry.id === category }"
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
          :class="{ active: entry.id === category }"
          @click="select(entry.id)"
        >{{ t(entry.label) }}</button>
      </nav>

      <OaScrollArea
        ref="scroll"
        wrap-class="oa-settings-scroll-wrap"
        scroll-class="oa-settings-scroll"
      >
        <div :key="category" :class="paneClass">
          <template v-if="category === 'appearance'">
            <AppearanceSection />
            <WallpaperSection />
          </template>
          <ChatSection v-else-if="category === 'chat'" />
          <AccountSection v-else />
        </div>
      </OaScrollArea>
    </div>
  </OaPanel>
</template>
