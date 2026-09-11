<script setup lang="ts">
// The administration shell: the same header the chat has, a rail of sections,
// and whichever page the route names.
//
// The rail is the conversation rail with different contents — same width,
// same radius, same surface, same hover tint — so moving between chatting and
// administering does not feel like moving between two applications.

import { computed, markRaw, onMounted, ref, watch, type Component } from 'vue';
import { useMediaQuery } from '@vueuse/core';
import { useRoute, useRouter } from 'vue-router';
import { health } from '@/api/client';
import OaIconButton from '@/components/OaIconButton.vue';
import OaResizer from '@/components/OaResizer.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { matchesSearch } from '@/lib/search';
import { t, type StringKey } from '@/composables/useI18n';
import {
  IconChart, IconChevron, IconCpu, IconFile, IconHome, IconKey, IconLayers, IconLock,
  IconMenu, IconPulse, IconServer, IconSliders, IconSpark, IconUsers, type OaIcon,
} from '@/icons';
import AppShell from '@/layouts/AppShell.vue';
import { useRailCollapse } from '@/composables/useRailCollapse';
import { formatUptime } from '@/lib/format';
import { isAdmin } from '@/stores/session';
import UnauthorizedModal from '@/views/UnauthorizedModal.vue';
import ChatLayout from '@/layouts/ChatLayout.vue';
import { provideAdminView } from './adminView';

import AdminDashboard from './AdminDashboard.vue';
import AdminUsers from './AdminUsers.vue';
import AdminGroups from './AdminGroups.vue';
import AdminProviders from './AdminProviders.vue';
import AdminModels from './AdminModels.vue';
import AdminAvailability from './AdminAvailability.vue';
import AdminUsage from './AdminUsage.vue';
import AdminResources from './AdminResources.vue';
import AdminCodes from './AdminCodes.vue';
import AdminLogs from './AdminLogs.vue';
import AdminSecurity from './AdminSecurity.vue';
import AdminSettings from './AdminSettings.vue';
import AdminAnnouncements from './AdminAnnouncements.vue';

interface AdminPageSpec {
  /** The path segment after /admin, empty for the dashboard. */
  slug: string;
  label: StringKey;
  icon: OaIcon;
  component: Component;
}

// Labels are looked up at render rather than stored, because this table is
// evaluated at import time — before the language is known.
const PAGES: AdminPageSpec[] = [
  { slug: '', label: 'navDashboard', icon: IconHome, component: markRaw(AdminDashboard) },
  { slug: 'users', label: 'navUsers', icon: IconUsers, component: markRaw(AdminUsers) },
  { slug: 'groups', label: 'navGroups', icon: IconLayers, component: markRaw(AdminGroups) },
  { slug: 'providers', label: 'navProviders', icon: IconServer, component: markRaw(AdminProviders) },
  { slug: 'models', label: 'navModels', icon: IconSpark, component: markRaw(AdminModels) },
  { slug: 'availability', label: 'navAvailability', icon: IconPulse, component: markRaw(AdminAvailability) },
  { slug: 'usage', label: 'navUsage', icon: IconChart, component: markRaw(AdminUsage) },
  { slug: 'resources', label: 'navResources', icon: IconCpu, component: markRaw(AdminResources) },
  { slug: 'codes', label: 'navCodes', icon: IconKey, component: markRaw(AdminCodes) },
  { slug: 'logs', label: 'navLogs', icon: IconFile, component: markRaw(AdminLogs) },
  { slug: 'security', label: 'navSecurity', icon: IconLock, component: markRaw(AdminSecurity) },
  { slug: 'settings', label: 'navSettings', icon: IconSliders, component: markRaw(AdminSettings) },
  { slug: 'announcements', label: 'announcements', icon: IconFile, component: markRaw(AdminAnnouncements) },
];

const route = useRoute();
const router = useRouter();
const query = ref('');
const narrow = useMediaQuery('(max-width: 900px)');
const filteredPages = computed(() => PAGES.filter((entry) => matchesSearch(query.value, t(entry.label))));

const rail = ref<HTMLElement | null>(null);

/**
 * The strip a page puts its own buttons in.
 *
 * Made here, before any page mounts, and attached to the head below. See
 * `AdminView.actionsHost` for why this is an element rather than a ref.
 */
const actionsHost = document.createElement('div');
actionsHost.className = 'oa-admin-actions';

function attachActions(node: unknown): void {
  if (node instanceof HTMLElement && actionsHost.parentElement !== node) {
    node.appendChild(actionsHost);
  }
}

/** Set once and never cleared, for the same reason `keepBody` is not. */
function keepRail(node: unknown): void {
  if (node instanceof HTMLElement) rail.value = node;
}

const segments = computed(() => route.path.replace(/^\/admin\/?/, '').split('/').filter(Boolean));
const current = computed(() => PAGES.find((entry) => entry.slug === (segments.value[0] ?? '')) ?? PAGES[0]!);

const title = ref('');
const subtitle = ref('');
const reloadCount = ref(0);
/** Which way the body arrives: further down the rail, back up it, or fresh. */
const direction = ref<'forward' | 'back' | 'rise'>('rise');

const build = ref('');
const buildTitle = ref('');

// The same control the chat's conversation list has, in the same place, doing
// the same thing to the same kind of column. It used to be the one rail here
// that could not be got out of the way.
const railState = useRailCollapse('obsidian-arc-admin-rail-collapsed');

provideAdminView({
  setTitle(next, hint) {
    title.value = next;
    subtitle.value = hint ?? '';
  },
  reload() {
    reloadCount.value += 1;
  },
  get params() {
    return segments.value.slice(1);
  },
  actionsHost,
});

// Changing views should never leave a stale heading or a set of buttons
// belonging to the previous section. Remounting the page on the key below
// takes care of the body; these two are the shell's own.
watch(current, (next, previous) => {
  const from = PAGES.indexOf(previous);
  const to = PAGES.indexOf(next);
  direction.value = to > from ? 'forward' : to < from ? 'back' : 'rise';
  title.value = t(next.label);
  subtitle.value = '';
});

const bodyKey = computed(() => `${current.value.slug}:${reloadCount.value}`);

onMounted(() => {
  title.value = t(current.value.label);
  // Which build is running, from the server rather than from the bundle: the
  // two can differ behind a stale cache, and the server's answer is the one
  // that matters.
  void health()
    .then((status) => {
      build.value = status.version;
      buildTitle.value = `Build ${status.version} · up ${formatUptime(status.uptime_sec)}`;
    })
    .catch(() => {
      // A version nobody can read is not worth an error state.
    });
});
</script>

<template>
  <!-- Reached directly rather than from inside the app: the chat is drawn
       behind the refusal so it sits over the product rather than over a boot
       spinner that will now never resolve. -->
  <template v-if="!isAdmin">
    <ChatLayout />
    <UnauthorizedModal />
  </template>

  <AppShell
    v-else
    :body-class="{ 'oa-admin': true, 'rail-collapsed': railState.collapsed.value }"
    @brand="router.push('/')"
  >
    <!-- Beside the title, where the chat keeps its own rail toggle. Both
         controls belong to the whole screen rather than to the rail, and the
         way out in particular was the one thing in this header that moved
         when the rail did — including out of reach, once the rail could be
         slid away. -->
    <template #leading>
      <OaIconButton
        class="oa-icon-btn oa-admin-rail-toggle"
        :label="t('navToggle')"
        @click="railState.toggle()"
      ><IconMenu :size="17" /></OaIconButton>
      <OaIconButton
        class="oa-icon-btn oa-admin-back"
        :label="t('backToChat')"
        @click="router.push('/')"
      ><IconChevron :size="16" /></OaIconButton>
    </template>

    <div :ref="keepRail" class="oa-admin-rail">
      <div class="oa-admin-rail-head">
        <span class="oa-admin-rail-title">{{ t('administration') }}</span>
      </div>

      <OaSearchField
        v-model="query"
        class="oa-admin-search"
        :label="t('searchAdmin')"
        :collapsible="narrow"
      />

      <OaScrollArea wrap-class="oa-admin-nav-wrap" scroll-class="oa-admin-nav-list">
        <p v-if="!filteredPages.length" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>
        <RouterLink
          v-for="entry in filteredPages"
          :key="entry.slug"
          class="oa-admin-nav"
          :class="{ active: entry === current }"
          :to="entry.slug ? `/admin/${entry.slug}` : '/admin'"
        >
          <component :is="entry.icon" :size="15" />
          <span>{{ t(entry.label) }}</span>
        </RouterLink>
      </OaScrollArea>

      <div class="oa-admin-rail-foot">
        <span class="oa-admin-build" :title="buildTitle">{{ build }}</span>
      </div>

      <OaResizer
        edge="right"
        css-variable="--oa-admin-rail-width"
        :style-target="rail"
        storage-key="obsidian-arc-admin-rail-width"
        :min="170"
        :max="380"
        :fallback="220"
        :label="t('resizeNav')"
      />
    </div>

    <div class="oa-admin-main">
      <div class="oa-admin-head" :ref="attachActions">
        <div>
          <h1 class="oa-admin-title">{{ title }}</h1>
          <p class="oa-admin-subtitle" :hidden="!subtitle">{{ subtitle }}</p>
        </div>
        <span class="oa-admin-head-spacer" />
      </div>

      <!-- Keyed by the section, so each navigation gets a fresh body element.
           A CSS animation runs when its class arrives, and `enter-forward`
           arriving on an element that already carries it is not an arrival:
           on a node that persists, the slide played once and then only when
           the direction happened to reverse. Recreating the element also puts
           the scroll back to the top, which is what walking to another
           section should do. `reload()` deliberately does not key it —
           re-reading the same screen after a save should not replay an
           entrance. -->
      <OaScrollArea
        :key="current.slug"
        wrap-class="oa-admin-body-wrap"
        :scroll-class="`oa-admin-body enter-${direction}`"
      >
        <component :is="current.component" :key="bodyKey" />
      </OaScrollArea>
    </div>
  </AppShell>
</template>
