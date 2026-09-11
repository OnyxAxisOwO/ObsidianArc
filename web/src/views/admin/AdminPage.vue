<script setup lang="ts">
// The administration shell: the same header the chat has, a rail of sections,
// and whichever page the route names.
//
// The rail is the conversation rail with different contents — same width,
// same radius, same surface, same hover tint — so moving between chatting and
// administering does not feel like moving between two applications.

import { computed, markRaw, nextTick, onMounted, ref, watch } from 'vue';
import { useMediaQuery } from '@vueuse/core';
import { useRoute, useRouter } from 'vue-router';
import { health } from '@/api/client';
import OaIconButton from '@/components/OaIconButton.vue';
import OaResizer from '@/components/OaResizer.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t } from '@/composables/useI18n';
import {
  IconChart, IconChevron, IconCpu, IconFile, IconHome, IconKey, IconLayers, IconLock,
  IconMenu, IconPulse, IconServer, IconSliders, IconSpark, IconUsers,
} from '@/icons';
import AppShell from '@/layouts/AppShell.vue';
import { useRailCollapse } from '@/composables/useRailCollapse';
import { formatUptime } from '@/lib/format';
import { isAdmin } from '@/stores/session';
import UnauthorizedModal from '@/views/UnauthorizedModal.vue';
import ChatLayout from '@/layouts/ChatLayout.vue';
import { provideAdminView } from './adminView';
import { searchAdminFeatures, type AdminPageSpec } from './features';

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
const searchGroups = computed(() => searchAdminFeatures(query.value, PAGES));

const bodyScroll = ref<InstanceType<typeof OaScrollArea> | null>(null);

let highlightTimer = 0;
function scrollToSection(id: string): void {
  window.clearTimeout(highlightTimer);
  let attempts = 0;
  const maxAttempts = 30;

  const check = () => {
    const el = document.getElementById(id);
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' });
      el.classList.add('oa-highlight-target');
      window.setTimeout(() => {
        el.classList.remove('oa-highlight-target');
      }, 2000);
      return;
    }
    attempts += 1;
    if (attempts < maxAttempts) {
      highlightTimer = window.setTimeout(check, 100);
    }
  };
  void nextTick(check);
}

function onPageClick(slug: string): void {
  if ((segments.value[0] ?? '') === slug && route.hash) {
    void router.push({ path: slug ? `/admin/${slug}` : '/admin' });
    bodyScroll.value?.scroller?.scrollTo({ top: 0, behavior: 'smooth' });
  }
}

function onItemClick(slug: string, id: string): void {
  if ((segments.value[0] ?? '') === slug && route.hash === `#${id}`) {
    scrollToSection(id);
  }
}

watch(
  () => route.hash,
  (nextHash) => {
    const id = nextHash.replace(/^#/, '');
    if (id) scrollToSection(id);
  },
  { flush: 'post' },
);

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
  if (route.hash) {
    const id = route.hash.replace(/^#/, '');
    if (id) scrollToSection(id);
  }
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
        <template v-if="!query.trim()">
          <RouterLink
            v-for="entry in PAGES"
            :key="entry.slug"
            class="oa-admin-nav"
            :class="{ active: entry === current && !route.hash }"
            :to="entry.slug ? `/admin/${entry.slug}` : '/admin'"
          >
            <component :is="entry.icon" :size="15" />
            <span>{{ t(entry.label) }}</span>
          </RouterLink>
        </template>
        <template v-else>
          <p v-if="!searchGroups.length" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>
          <div
            v-for="group in searchGroups"
            :key="group.page.slug"
            class="oa-admin-search-group"
          >
            <RouterLink
              :to="group.page.slug ? `/admin/${group.page.slug}` : '/admin'"
              class="oa-admin-nav oa-admin-group-head"
              :class="{ active: group.page === current && !route.hash }"
              @click="onPageClick(group.page.slug)"
            >
              <component :is="group.page.icon" :size="15" />
              <span>{{ t(group.page.label) }}</span>
            </RouterLink>

            <div v-if="group.items.length" class="oa-admin-subnav-list">
              <RouterLink
                v-for="item in group.items"
                :key="item.id"
                :to="{ path: group.page.slug ? `/admin/${group.page.slug}` : '/admin', hash: `#${item.id}` }"
                class="oa-admin-subnav-item"
                :class="{ active: group.page === current && route.hash === `#${item.id}` }"
                @click="onItemClick(group.page.slug, item.id)"
              >
                <span class="oa-admin-subnav-dot" />
                <span class="oa-admin-subnav-text">{{ t(item.titleKey) }}</span>
              </RouterLink>
            </div>
          </div>
        </template>
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
        ref="bodyScroll"
        :key="current.slug"
        wrap-class="oa-admin-body-wrap"
        :scroll-class="`oa-admin-body enter-${direction}`"
      >
        <component :is="current.component" :key="bodyKey" />
      </OaScrollArea>
    </div>
  </AppShell>
</template>
