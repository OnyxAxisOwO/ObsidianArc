<script setup lang="ts">
// The administration shell: the same header the chat has, a rail of sections,
// and whichever page the route names.
//
// The rail is the conversation rail with different contents — same width,
// same radius, same surface, same hover tint — so moving between chatting and
// administering does not feel like moving between two applications.

import { computed, markRaw, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useMediaQuery } from '@vueuse/core';
import { useRoute, useRouter } from 'vue-router';
import { health } from '@/api/client';
import OaIconButton from '@/components/OaIconButton.vue';
import OaResizer from '@/components/OaResizer.vue';
import OaScrollArea from '@/components/OaScrollArea.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import { t } from '@/composables/useI18n';
import {
  IconArchive, IconChart, IconChevron, IconCpu, IconFile, IconGift, IconHome, IconKey, IconLayers, IconLock,
  IconMenu, IconMessage, IconPulse, IconSend, IconServer, IconSliders, IconSpark,
  IconPuzzle, IconTrophy,
  IconUsers,
} from '@/icons';
import AppShell from '@/layouts/AppShell.vue';
import { useRailCollapse } from '@/composables/useRailCollapse';
import { formatUptime } from '@/lib/format';
import { fetchMe, type Account } from '@/api/auth';
import { BACKOFFICE_LOCKED } from '@/api/client';
import { leaveBackoffice } from '@/api/twofactor';
import { adopt, canAdmin, currentPreferences, currentUser, isAdmin, isSuperAdmin } from '@/stores/session';
import TwoFactorWizard from '@/views/settings/TwoFactorWizard.vue';
import AdminUnlock from './AdminUnlock.vue';
import UnauthorizedModal from '@/views/UnauthorizedModal.vue';
import ChatLayout from '@/layouts/ChatLayout.vue';
import { provideAdminView } from './adminView';
import { pageLabel, searchAdminFeatures, type AdminPageSpec, visibleAdminPages } from './features';
import { plugins } from '@/plugins/registry';
import { BACKOFFICE_PANEL_PARAM, provideBackofficePanels } from '@/composables/usePanelExit';
import { resolveSidePanel } from '@/views/panels';

import AdminDashboard from './AdminDashboard.vue';
import AdminUsers from './AdminUsers.vue';
import AdminGroups from './AdminGroups.vue';
import AdminProviders from './AdminProviders.vue';
import AdminModels from './AdminModels.vue';
import AdminAvailability from './AdminAvailability.vue';
import AdminBonus from './AdminBonus.vue';
import AdminLeaderboard from './AdminLeaderboard.vue';
import AdminUsage from './AdminUsage.vue';
import AdminResources from './AdminResources.vue';
import AdminCodes from './AdminCodes.vue';
import AdminInvites from './AdminInvites.vue';
import AdminLogs from './AdminLogs.vue';
import AdminSecurity from './AdminSecurity.vue';
import AdminSettings from './AdminSettings.vue';
import AdminAnnouncements from './AdminAnnouncements.vue';
import AdminFeedback from './AdminFeedback.vue';
import AdminSafeMode from './AdminSafeMode.vue';
import AdminBackup from './AdminBackup.vue';
import AdminPlugins from './AdminPlugins.vue';
import AdminUpdateDialog from './AdminUpdateDialog.vue';
import { loadUpdateStatus } from '@/admin/update';

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
  { slug: 'bonus', label: 'navBonus', icon: IconGift, component: markRaw(AdminBonus) },
  { slug: 'leaderboard', label: 'navLeaderboard', icon: IconTrophy, component: markRaw(AdminLeaderboard) },
  { slug: 'resources', label: 'navResources', icon: IconCpu, component: markRaw(AdminResources) },
  { slug: 'codes', label: 'navCodes', icon: IconKey, component: markRaw(AdminCodes) },
  { slug: 'invites', label: 'navInvites', icon: IconSend, component: markRaw(AdminInvites) },
  { slug: 'logs', label: 'navLogs', icon: IconFile, component: markRaw(AdminLogs) },
  { slug: 'security', label: 'navSecurity', icon: IconLock, component: markRaw(AdminSecurity) },
  { slug: 'backup', label: 'navBackup', icon: IconArchive, component: markRaw(AdminBackup), permission: '*' },
  { slug: 'settings', label: 'navSettings', icon: IconSliders, component: markRaw(AdminSettings) },
  { slug: 'announcements', label: 'announcements', icon: IconFile, component: markRaw(AdminAnnouncements) },
  { slug: 'feedback', label: 'navFeedback', icon: IconMessage, component: markRaw(AdminFeedback) },
  {
    slug: 'plugins', label: 'navPlugins', icon: IconPuzzle, component: markRaw(AdminPlugins),
    permission: 'plugins,plugins_manage,plugins_remove',
    keywords: ['插件', 'plugins', 'extension', '扩展'],
  },
];

// The pages made of groups of rows, which sit in the middle of the window at
// the width a form wants — heading included, or the heading and the page
// below it would start at different edges. The rest are tables and figures,
// and take the whole window.
const CENTERED = new Set(['security', 'backup', 'settings', 'availability', 'leaderboard', 'bonus', 'invites', 'plugins']);

// Core pages only in the sidebar rail. Individual plugin pages are accessed
// as tabs inside the Plugins screen instead of crowding the main navigation.
const pages = computed<AdminPageSpec[]>(() => PAGES);

const route = useRoute();
const router = useRouter();

// Route redirect: /admin/<pluginSlug> -> /admin/plugins?tab=<pluginSlug>
watch(
  () => route.path,
  (path) => {
    const slug = path.replace(/^\/admin\/?/, '').split('/')[0];
    if (slug && !PAGES.some((p) => p.slug === slug)) {
      const isPluginPage = plugins().some((plugin) => plugin.adminPages?.some((page) => page.slug === slug));
      if (isPluginPage) {
        void router.replace({ path: '/admin/plugins', query: { ...route.query, tab: slug }, hash: route.hash });
      }
    }
  },
  { immediate: true },
);
const query = ref('');
const narrow = useMediaQuery('(max-width: 900px)');
const visiblePages = computed(() => visibleAdminPages(pages.value, isSuperAdmin.value));
const searchGroups = computed(() => searchAdminFeatures(query.value, visiblePages.value));

const bodyScroll = ref<InstanceType<typeof OaScrollArea> | null>(null);

let highlightTimer = 0;
let disposed = false;
// A quick navigation can leave a pending search waiting for a section that
// belongs to the screen being removed.
onBeforeUnmount(() => {
  disposed = true;
  window.clearTimeout(highlightTimer);
});

function scrollToSection(id: string): void {
  window.clearTimeout(highlightTimer);
  let attempts = 0;
  const maxAttempts = 30;

  const check = () => {
    if (disposed) return;
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
const current = computed(() => pages.value.find((entry) => entry.slug === (segments.value[0] ?? '')) ?? PAGES[0]!);

// A section's grant is its slug unless it says otherwise, and any one of a
// comma-separated list opens it — the plugins screen is three grants. The
// backup page spans the whole instance, so its `*` marker is reserved for
// the super admin.
const allowed = computed(() => {
  const needs = current.value.permission ?? (current.value.slug || 'dashboard');
  return needs === '*' ? isSuperAdmin.value : needs.split(',').some((grant) => canAdmin(grant.trim()));
});

// The operator's policy wants a second sign-in step before the backoffice
// opens, and this account has none. Every endpoint behind these pages says
// so too; drawing the setup here, in place of the page, is what turns a wall
// of refusals into one thing to do. The rail stays, so it is clear where
// this is.
const gated = computed(() => !!currentUser.value?.two_factor_backoffice);

function enrolled(user: Account): void {
  adopt(user, currentPreferences.value);
  reloadCount.value += 1;
}

// The operator's code at the backoffice's own door, on top of signing in.
// Whether this visit is open is the server's to say — it depends on the
// mode, the minutes and what this browser did last — so the shell asks as it
// mounts, draws nothing until it knows, and then either the page or the
// place to type a code. A visit that lapses while a page is open reaches the
// shell as an event from the API client, and the page gives way to the door.
const stepUp = computed(() => !!currentUser.value?.two_factor_backoffice_verify && !gated.value);
const visit = ref<'checking' | 'locked' | 'open'>(stepUp.value ? 'checking' : 'open');

// Forced when the server has just turned a request away: the account the
// shell holds may predate the operator switching the door on, and trusting it
// would leave the page drawing that refusal as an error instead of the door.
async function checkVisit(force = false): Promise<void> {
  if (!stepUp.value && !force) {
    visit.value = 'open';
    return;
  }
  visit.value = 'checking';
  try {
    const { user } = await fetchMe();
    if (disposed) return;
    currentUser.value = user;
    visit.value = user.two_factor_backoffice_verify && user.two_factor_backoffice_locked ? 'locked' : 'open';
  } catch {
    // Unknown is locked: the door costs a code, a page drawn behind a door
    // that was shut costs a screen of refusals.
    if (!disposed) visit.value = 'locked';
  }
}

// The account can change under the shell — finishing the enrolment gate
// above turns the door on — so the question is asked again when it does.
watch(stepUp, () => {
  // Already settled by the forced check that changed the account.
  if (visit.value === 'open') void checkVisit();
});

function unlocked(): void {
  visit.value = 'open';
  reloadCount.value += 1;
}

function onLocked(): void {
  void checkVisit(true);
}

// Leaving is what ends a visit in the every-visit mode, so it is said on the
// way out of the shell and as the page itself goes — a reload or a closed
// tab. The server decides whether leaving means anything in the mode it is
// in, so this is sent whenever the backoffice asks for a code at all.
function leave(): void {
  if (stepUp.value) leaveBackoffice();
}

window.addEventListener(BACKOFFICE_LOCKED, onLocked);
window.addEventListener('pagehide', leave);
onBeforeUnmount(() => {
  window.removeEventListener(BACKOFFICE_LOCKED, onLocked);
  window.removeEventListener('pagehide', leave);
  leave();
});

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
  open(path, query) {
    void router.push({ path, query: query ?? {} });
  },
});

// A panel from the account menu or a link, drawn beside this page. The address
// keeps the page's own path, query and hash, and only the panel's parameter
// comes and goes, so the page underneath is never taken away (usePanelExit).
const sidePanel = computed(() => resolveSidePanel(route.query[BACKOFFICE_PANEL_PARAM]));

provideBackofficePanels({
  show(name) {
    void router.push({ path: route.path, query: { ...route.query, [BACKOFFICE_PANEL_PARAM]: name }, hash: route.hash });
  },
  hide() {
    const query = { ...route.query };
    delete query[BACKOFFICE_PANEL_PARAM];
    void router.replace({ path: route.path, query, hash: route.hash });
  },
});

// Changing views should never leave a stale heading or a set of buttons
// belonging to the previous section. Remounting the page on the key below
// takes care of the body; these two are the shell's own.
watch(current, (next, previous) => {
  const from = pages.value.indexOf(previous);
  const to = pages.value.indexOf(next);
  direction.value = to > from ? 'forward' : to < from ? 'back' : 'rise';
  title.value = pageLabel(next);
  subtitle.value = '';
});

const bodyKey = computed(() => `${current.value.slug}:${reloadCount.value}`);

// The release notice waits for the same things the page does: a super
// administrator, past the two-step gates. Asking before the visit is open
// would be refused, and a refusal is not an answer worth remembering.
const updateReady = computed(() => isSuperAdmin.value && visit.value === 'open' && !gated.value);
watch(updateReady, (ready) => {
  if (ready) void loadUpdateStatus();
}, { immediate: true });

onMounted(() => {
  void checkVisit();
  title.value = pageLabel(current.value);
  // Which build is running, from the server rather than from the bundle: the
  // two can differ behind a stale cache, and the server's answer is the one
  // that matters.
  void health()
    .then((status) => {
      build.value = status.version ?? '';
      buildTitle.value = status.version && status.uptime_sec !== undefined
        ? `Build ${status.version} · up ${formatUptime(status.uptime_sec)}`
        : '';
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

    <template #header>
      <AdminSafeMode />
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
            v-for="entry in visiblePages"
            :key="entry.slug"
            class="oa-admin-nav"
            :class="{ active: entry === current && !route.hash }"
            :to="entry.slug ? `/admin/${entry.slug}` : '/admin'"
          >
            <component :is="entry.icon" :size="15" />
            <span>{{ pageLabel(entry) }}</span>
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
              <span>{{ pageLabel(group.page) }}</span>
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

    <!-- The whole section arrives together, including its heading and actions.
         Keying only navigation keeps saves from replaying the entrance. -->
    <div :key="current.slug" class="oa-admin-main" :class="[`enter-${direction}`, { 'oa-admin-main-centered': CENTERED.has(current.slug), 'oa-admin-main-dashboard': !current.slug && allowed && !gated && visit === 'open' }]">
      <!-- The overview owns its editorial heading; other pages keep the shared toolbar. -->
      <!-- None while a card stands in for the page: it says what it is itself,
           and a heading above an empty page is what made it look lost. -->
      <div v-if="(current.slug || !allowed) && !gated && visit === 'open'" class="oa-admin-head" :ref="attachActions">
        <div>
          <h1 class="oa-admin-title">{{ title }}</h1>
          <p class="oa-admin-subtitle" :hidden="!subtitle">{{ subtitle }}</p>
        </div>
        <span class="oa-admin-head-spacer" />
      </div>

      <OaScrollArea
        ref="bodyScroll"
        wrap-class="oa-admin-body-wrap"
        scroll-class="oa-admin-body"
      >
        <div v-if="gated || visit !== 'open'" class="oa-2fa-stage">
          <div v-if="gated" class="oa-2fa-gate">
            <h2 class="oa-2fa-title">{{ t('twoFactorGateTitle') }}</h2>
            <p class="oa-field-hint">{{ t('twoFactorGateBody') }}</p>
            <TwoFactorWizard @done="enrolled" />
          </div>
          <p v-else-if="visit === 'checking'" class="oa-table-empty">{{ t('backofficeChecking') }}</p>
          <AdminUnlock v-else @unlocked="unlocked" />
        </div>
        <component v-else-if="allowed" :is="current.component" v-bind="current.props ?? {}" :key="bodyKey" />
        <div v-else class="oa-permission-empty" role="alert">
          <IconLock :size="28" />
          <h2>{{ t('permissionDeniedTitle') }}</h2>
          <p>{{ t('permissionDeniedHint') }}</p>
        </div>
      </OaScrollArea>
    </div>
    <AdminUpdateDialog v-if="updateReady" />

    <!-- Teleported into this shell's row, as the chat's panels are into theirs,
         so it is a column beside the page and not a page of its own. -->
    <component
      v-if="sidePanel"
      :is="sidePanel.component"
      :key="sidePanel.name"
      v-bind="sidePanel.props"
    />
  </AppShell>
</template>
