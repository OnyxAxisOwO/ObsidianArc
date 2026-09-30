<script setup lang="ts">
// The plugins screen: every plugin this build carries, the way a browser's
// extensions page lists them — installed or not, on or off, what each one
// is and what it would attach.
//
// Installing asks the two questions an install cannot take back quietly:
// switch it on straight away, and what its first settings are. Switching
// one off and taking one away each ask for a two-step code, because a
// plugin can be the check standing in front of sign-up.
//
// A change redraws from the server's answer and then reloads the session's
// plugins, so a page the plugin brings appears in the rail — or leaves it —
// the moment the change is made.

import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { adminApi, type AdminPlugin, type PluginChange, type PluginText } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { currentLanguage, t } from '@/composables/useI18n';
import { IconArrowUpRight, IconPlus, IconPuzzle, IconDownload, IconRefresh } from '@/icons';
import { absoluteTime } from '@/lib/format';
import { loadPluginModule, plugins } from '@/plugins/registry';
import type { ArcPlugin } from '@/plugins/types';
import { canAdmin, currentUser, refreshSite } from '@/stores/session';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';
import PluginAdminPage from './PluginAdminPage.vue';
import PluginSettingControls from './PluginSettingControls.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navPlugins'), t('pluginsSubtitle'));

let route: ReturnType<typeof useRoute> | undefined;
let router: ReturnType<typeof useRouter> | undefined;
try {
  route = useRoute();
  router = useRouter();
} catch {
  // Unit tests mount without a router.
}

const list = ref<AdminPlugin[]>([]);
const loaded = ref(false);
const error = ref('');
const twoFactorRequired = ref(true);
/** The browser halves, by name, for the icon and the declared settings and pages. */
const modules = reactive<Record<string, ArcPlugin | null>>({});

const enabledPages = computed(() => {
  return plugins().flatMap((plugin) => plugin.adminPages ?? []);
});

const activeTab = ref<string>('manage');

const currentPage = computed(() => {
  return enabledPages.value.find((p) => p.slug === activeTab.value) ?? null;
});

function switchTab(slug: string): void {
  activeTab.value = slug;
  closePanels();
  if (router && route) {
    void router.replace({
      query: slug === 'manage' ? { ...route.query, tab: undefined } : { ...route.query, tab: slug },
    });
  }
  updateTitle();
}

function updateTitle(): void {
  if (activeTab.value === 'manage') {
    view.setTitle(t('navPlugins'), t('pluginsSubtitle'));
  } else {
    const page = currentPage.value;
    if (page) {
      view.setTitle(page.title(), page.hint?.());
    }
  }
}

watch(
  () => route?.query?.tab,
  (tab) => {
    if (typeof tab === 'string' && enabledPages.value.some((p) => p.slug === tab)) {
      activeTab.value = tab;
    } else {
      activeTab.value = 'manage';
    }
    updateTitle();
  },
  { immediate: true },
);

watch(enabledPages, (pages) => {
  if (activeTab.value !== 'manage' && !pages.some((p) => p.slug === activeTab.value)) {
    switchTab('manage');
  }
});

const canManage = computed(() => canAdmin('plugins_manage'));
const canRemove = computed(() => canAdmin('plugins_remove'));
const hasTwoFactor = computed(() => !!currentUser.value?.two_factor_at);

function message(failure: unknown): string {
  return failure instanceof ApiError ? failure.message : String(failure);
}

function text(value: PluginText | undefined): string {
  if (!value) return '';
  return (currentLanguage() === 'zh' ? value.zh : value.en) || value.en;
}

function title(plugin: AdminPlugin): string {
  return text(plugin.manifest.title) || plugin.name;
}

function stateLabel(plugin: AdminPlugin): string {
  if (plugin.missing) return t('pluginMissing');
  switch (plugin.state) {
    case 'enabled': return t('pluginStateEnabled');
    case 'disabled': return t('pluginStateDisabled');
    default: return t('pluginStateAvailable');
  }
}

function stateTone(plugin: AdminPlugin): 'default' | 'muted' | 'warning' {
  if (plugin.missing) return 'warning';
  return plugin.state === 'enabled' ? 'default' : 'muted';
}

async function load(): Promise<void> {
  error.value = '';
  try {
    const res = await adminApi.plugins();
    list.value = res.plugins;
    twoFactorRequired.value = res.two_factor_required ?? true;
    loaded.value = true;
    await Promise.all(res.plugins.filter((plugin) => !plugin.missing && !(plugin.name in modules)).map(async (plugin) => {
      modules[plugin.name] = await loadPluginModule(plugin.name);
    }));
  } catch (failure) {
    error.value = message(failure);
  }
}

/** Every settings section the plugin declares, wherever it would be drawn. */
function sectionsOf(name: string) {
  return modules[name]?.settings ?? [];
}

async function applied(change: PluginChange): Promise<void> {
  list.value = change.plugins;
  if (change.two_factor_required !== undefined) {
    twoFactorRequired.value = change.two_factor_required;
  }
  // The rail's pages and every screen's plugin hooks come from the session's
  // own list, which only the server can say has changed.
  try {
    await refreshSite();
  } catch {
    // The change stands; the rail catches up on the next page load.
  }
}

// --- details ----------------------------------------------------------------

const detail = ref<AdminPlugin | null>(null);

// One panel at a time: they are columns beside the list, and two of them
// would squeeze it to nothing.
function closePanels(): void {
  detail.value = null;
  installing.value = null;
  confirming.value = null;
  showNewPluginModal.value = false;
}

function openDetail(plugin: AdminPlugin): void {
  closePanels();
  detail.value = plugin;
}

const detailPages = computed(() => {
  const plugin = detail.value;
  if (!plugin) return [];
  return modules[plugin.name]?.adminPages ?? [];
});

interface ContributionItem {
  text: string;
  sub?: string;
  secret?: boolean;
  method?: string;
  slug?: string;
  to?: string;
}

interface ContributionGroup {
  key: string;
  label: string;
  count: number;
  items: ContributionItem[];
}

const contributionGroups = computed<ContributionGroup[]>(() => {
  const plugin = detail.value;
  if (!plugin) return [];
  const c = plugin.contributions;
  const groups: ContributionGroup[] = [];

  const pages = modules[plugin.name]?.adminPages ?? [];
  if (pages.length) {
    groups.push({
      key: 'pages',
      label: t('pluginPages'),
      count: pages.length,
      items: pages.map((page) => ({
        text: page.title(),
        slug: page.slug,
        to: `/admin/${page.slug}`,
      })),
    });
  }

  if (c.settings.length) {
    groups.push({
      key: 'settings',
      label: t('pluginSettingsKeys'),
      count: c.settings.length,
      items: c.settings.map((s) => ({
        text: s.key,
        secret: !!s.secret,
      })),
    });
  }

  if (c.fields.length) {
    groups.push({
      key: 'fields',
      label: t('pluginFields'),
      count: c.fields.length,
      items: c.fields.map((f) => ({ text: f })),
    });
  }

  if (c.guards.length) {
    groups.push({
      key: 'guards',
      label: t('pluginGuards'),
      count: c.guards.length,
      items: c.guards.map((g) => ({ text: g })),
    });
  }

  if (c.captcha_modes.length) {
    groups.push({
      key: 'captcha',
      label: t('pluginCaptchaModes'),
      count: c.captcha_modes.length,
      items: c.captcha_modes.map((m) => ({ text: m })),
    });
  }

  const routes: ContributionItem[] = [
    ...c.admin_routes.map((r) => ({ text: r.pattern, method: 'ADMIN' })),
    ...c.public_routes.map((p) => ({ text: p, method: 'API' })),
  ];
  if (routes.length) {
    groups.push({
      key: 'routes',
      label: t('pluginRoutes'),
      count: routes.length,
      items: routes,
    });
  }

  if (c.migrations.length) {
    groups.push({
      key: 'migrations',
      label: t('pluginMigrations'),
      count: c.migrations.length,
      items: c.migrations.map((m) => ({ text: m })),
    });
  }

  if (c.commands.length) {
    groups.push({
      key: 'commands',
      label: t('pluginCommands'),
      count: c.commands.length,
      items: c.commands.map((cmd) => ({ text: cmd })),
    });
  }

  return groups;
});

// --- install ----------------------------------------------------------------

const installing = ref<AdminPlugin | null>(null);
const enableNow = ref(true);
const draft = reactive<Record<string, string>>({});
const noHints: Record<string, string> = {};
const busy = ref(false);
const panelError = ref('');
const needCode = computed(() => twoFactorRequired.value);
const availablePlugins = computed(() => list.value.filter((p) => p.state === 'available' && !p.missing));
const showNewPluginModal = ref(false);
const dragover = ref(false);
const uploading = ref(false);
const uploadError = ref('');

function choosePlugin(): void {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = '.zip';
  input.onchange = (e) => {
    const file = (e.target as HTMLInputElement).files?.[0];
    if (file) void uploadPlugin(file);
  };
  input.click();
}

function onDropPlugin(event: DragEvent): void {
  dragover.value = false;
  const file = event.dataTransfer?.files?.[0];
  if (file && file.name.endsWith('.zip')) {
    void uploadPlugin(file);
  } else {
    uploadError.value = '只支持 .zip 文件。';
  }
}

async function uploadPlugin(file: File): Promise<void> {
  uploading.value = true;
  uploadError.value = '';
  const formData = new FormData();
  formData.append('file', file);
  try {
    const res = await fetch('/api/admin/plugins/upload', {
      method: 'POST',
      body: formData,
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new Error(data.error?.message || `HTTP ${res.status}`);
    }
    const data = await res.json();
    uploadError.value = `源码上传并解压成功 ( ${data.name} )！请回到终端执行 'make build' 重新编译，完成后刷新本页即可安装启动它。`;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    uploadError.value = msg;
  } finally {
    uploading.value = false;
  }
}

function openInstallNewPlugin(): void {
  closePanels();
  const available = availablePlugins.value;
  if (available.length > 0) {
    openInstall(available[0]!);
  } else {
    showNewPluginModal.value = true;
  }
}

function openInstall(plugin: AdminPlugin): void {
  closePanels();
  code.value = '';
  for (const key of Object.keys(draft)) delete draft[key];
  for (const section of sectionsOf(plugin.name)) {
    for (const control of section.controls) {
      draft[control.key] = control.kind === 'secret' ? '' : section.defaults[control.key] ?? '';
    }
  }
  enableNow.value = true;
  panelError.value = '';
  installing.value = plugin;
}

async function install(): Promise<void> {
  const plugin = installing.value;
  if (!plugin) return;
  busy.value = true;
  panelError.value = '';
  // An untouched secret is not sent: empty is "none", and the install
  // writes nothing it was not given.
  const settings: Record<string, string> = {};
  for (const section of sectionsOf(plugin.name)) {
    for (const control of section.controls) {
      const value = (draft[control.key] ?? '').trim();
      if (control.kind === 'secret' && !value) continue;
      settings[control.key] = value;
    }
  }
  try {
    await applied(await adminApi.installPlugin(plugin.name, enableNow.value, settings));
    installing.value = null;
  } catch (failure) {
    panelError.value = message(failure);
  } finally {
    busy.value = false;
  }
}

async function enable(plugin: AdminPlugin): Promise<void> {
  error.value = '';
  try {
    await applied(await adminApi.enablePlugin(plugin.name));
  } catch (failure) {
    error.value = message(failure);
  }
}

// --- disable and uninstall ---------------------------------------------------

const confirming = ref<{ plugin: AdminPlugin; action: 'disable' | 'uninstall' } | null>(null);
const code = ref('');
const purge = ref(false);

function openConfirm(plugin: AdminPlugin, action: 'disable' | 'uninstall'): void {
  closePanels();
  code.value = '';
  purge.value = false;
  panelError.value = '';
  confirming.value = { plugin, action };
}

const confirmTitle = computed(() => {
  const current = confirming.value;
  if (!current) return '';
  const name = title(current.plugin);
  return current.action === 'disable' ? t('pluginDisableTitle', { name }) : t('pluginUninstallTitle', { name });
});

async function confirm(): Promise<void> {
  const current = confirming.value;
  if (!current) return;
  busy.value = true;
  panelError.value = '';
  try {
    const change = current.action === 'disable'
      ? await adminApi.disablePlugin(current.plugin.name, code.value.trim())
      : await adminApi.uninstallPlugin(current.plugin.name, purge.value, code.value.trim());
    await applied(change);
    confirming.value = null;
  } catch (failure) {
    panelError.value = message(failure);
  } finally {
    busy.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div v-if="enabledPages.length" class="oa-plugin-tabstrip">
    <div class="oa-plugin-tabs">
      <button
        type="button"
        class="oa-plugin-tab"
        :class="{ active: activeTab === 'manage' }"
        @click="switchTab('manage')"
      >
        <IconPuzzle :size="14" />
        <span>{{ t('pluginManagement') }}</span>
      </button>
      <button
        v-for="page in enabledPages"
        :key="page.slug"
        type="button"
        class="oa-plugin-tab"
        :class="{ active: activeTab === page.slug }"
        @click="switchTab(page.slug)"
      >
        <component :is="page.icon ?? IconPuzzle" :size="14" />
        <span>{{ page.title() }}</span>
      </button>
    </div>
  </div>

  <template v-if="activeTab === 'manage'">
    <Teleport :to="view.actionsHost">
      <button
        type="button"
        class="oa-btn primary"
        :disabled="!canManage"
        @click="openInstallNewPlugin"
      >
        <IconPlus :size="13" />
        <span>{{ t('pluginInstallNew') }}</span>
      </button>
    </Teleport>

    <AdminFailure v-if="error && !loaded" :message="error" @retry="load" />
    <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>
    <p v-else-if="!list.length" class="oa-table-empty">{{ t('pluginsEmpty') }}</p>
    <div v-else class="oa-workbench">
      <p v-if="error" class="oa-field-hint" role="alert">{{ error }}</p>
      <div class="oa-workbench-grid">
        <AdminControlCard
          v-for="plugin in list"
          :id="`plugin-${plugin.name}`"
          :key="plugin.name"
          :title="title(plugin)"
          :hint="t('pluginVersion', { version: plugin.manifest.version || plugin.installed.version || '—' })"
          :icon="modules[plugin.name]?.icon ?? IconPuzzle"
        >
          <template #actions>
            <OaBadgeRow><OaBadge :tone="stateTone(plugin)">{{ stateLabel(plugin) }}</OaBadge></OaBadgeRow>
          </template>
          <p class="oa-field-hint">{{ plugin.missing ? t('pluginMissingHint') : text(plugin.manifest.description) }}</p>
          <div class="oa-control-actions">
            <button v-if="!plugin.missing" type="button" class="oa-btn" @click="openDetail(plugin)">{{ t('pluginDetails') }}</button>
            <template v-if="!plugin.missing && plugin.state === 'available'">
              <button type="button" class="oa-btn primary" :disabled="!canManage" @click="openInstall(plugin)">{{ t('pluginInstall') }}</button>
            </template>
            <template v-else-if="!plugin.missing">
              <button v-if="plugin.state === 'disabled'" type="button" class="oa-btn primary" :disabled="!canManage" @click="enable(plugin)">{{ t('pluginEnable') }}</button>
              <button v-else type="button" class="oa-btn" :disabled="!canManage" @click="openConfirm(plugin, 'disable')">{{ t('pluginDisable') }}</button>
              <button type="button" class="oa-btn oa-btn-danger" :disabled="!canRemove" @click="openConfirm(plugin, 'uninstall')">{{ t('pluginUninstall') }}</button>
            </template>
          </div>
        </AdminControlCard>
      </div>
    </div>
  </template>

  <template v-else-if="currentPage">
    <PluginAdminPage :key="currentPage.slug" :page="currentPage" />
  </template>

  <OaPanel
    v-if="detail"
    :title="title(detail)"
    :footer="false"
    :width="480"
    @close="detail = null"
  >
    <div class="oa-plugin-detail">
      <div class="oa-plugin-detail-hero">
        <div class="oa-plugin-detail-icon">
          <component :is="modules[detail.name]?.icon ?? IconPuzzle" :size="24" />
        </div>
        <div class="oa-plugin-detail-lead">
          <div class="oa-plugin-detail-header-row">
            <h3 class="oa-plugin-detail-name">{{ title(detail) }}</h3>
            <span class="oa-plugin-detail-slug">{{ detail.name }}</span>
          </div>
          <div class="oa-plugin-detail-badge-row">
            <span class="oa-plugin-detail-version">v{{ detail.manifest.version || detail.installed.version || '—' }}</span>
            <OaBadge :tone="stateTone(detail)">{{ stateLabel(detail) }}</OaBadge>
          </div>
          <p class="oa-plugin-detail-desc">
            {{ detail.missing ? t('pluginMissingHint') : text(detail.manifest.description) }}
          </p>
        </div>
      </div>

      <div v-if="detail.state === 'enabled' && detailPages.length" class="oa-plugin-detail-action-bar">
        <button
          v-for="page in detailPages"
          :key="page.slug"
          type="button"
          class="oa-btn small primary oa-plugin-page-jump"
          @click="switchTab(page.slug); detail = null"
        >
          <span>{{ page.title() }}</span>
          <IconArrowUpRight :size="13" />
        </button>
      </div>

      <div class="oa-plugin-detail-section">
        <div class="oa-plugin-detail-section-head">
          <h4 class="oa-field-label">{{ t('pluginManifest') }}</h4>
        </div>
        <div class="oa-plugin-meta-grid">
          <div class="oa-plugin-meta-item">
            <span class="oa-plugin-meta-label">{{ t('pluginVersionLabel') }}</span>
            <span class="oa-plugin-meta-value mono">{{ detail.manifest.version || '—' }}</span>
          </div>
          <div v-if="detail.manifest.author" class="oa-plugin-meta-item">
            <span class="oa-plugin-meta-label">{{ t('pluginAuthor') }}</span>
            <span class="oa-plugin-meta-value">{{ detail.manifest.author }}</span>
          </div>
          <div v-if="detail.manifest.license" class="oa-plugin-meta-item">
            <span class="oa-plugin-meta-label">{{ t('pluginLicense') }}</span>
            <span class="oa-plugin-meta-value">{{ detail.manifest.license }}</span>
          </div>
          <div class="oa-plugin-meta-item">
            <span class="oa-plugin-meta-label">{{ t('status') }}</span>
            <span class="oa-plugin-meta-value">
              <OaBadge :tone="stateTone(detail)">{{ stateLabel(detail) }}</OaBadge>
            </span>
          </div>
          <div v-if="detail.installed.version" class="oa-plugin-meta-item">
            <span class="oa-plugin-meta-label">{{ t('pluginInstalledVersion') }}</span>
            <span class="oa-plugin-meta-value mono">{{ detail.installed.version }}</span>
          </div>
          <div v-if="detail.installed.at" class="oa-plugin-meta-item wide">
            <span class="oa-plugin-meta-label">{{ t('pluginInstalledAt') }}</span>
            <span class="oa-plugin-meta-value mono">{{ absoluteTime(detail.installed.at) }}</span>
          </div>
          <div v-if="detail.manifest.homepage" class="oa-plugin-meta-item wide">
            <span class="oa-plugin-meta-label">{{ t('pluginHomepage') }}</span>
            <a :href="detail.manifest.homepage" target="_blank" rel="noopener noreferrer" class="oa-plugin-meta-link">
              <span>{{ detail.manifest.homepage }}</span>
              <IconArrowUpRight :size="12" />
            </a>
          </div>
        </div>
      </div>

      <div class="oa-plugin-detail-section">
        <div class="oa-plugin-detail-section-head">
          <h4 class="oa-field-label">{{ t('pluginContributes') }}</h4>
          <span v-if="contributionGroups.length" class="oa-plugin-contrib-total-badge">
            {{ contributionGroups.reduce((acc, g) => acc + g.count, 0) }}
          </span>
        </div>

        <p v-if="!contributionGroups.length" class="oa-field-hint">{{ t('pluginNothing') }}</p>
        <div v-else class="oa-plugin-contrib-list">
          <div v-for="group in contributionGroups" :key="group.key" class="oa-plugin-contrib-group">
            <div class="oa-plugin-contrib-head">
              <span class="oa-plugin-contrib-title">{{ group.label }}</span>
              <span class="oa-plugin-contrib-count">{{ group.count }}</span>
            </div>
            <div class="oa-plugin-chips">
              <template v-for="item in group.items" :key="item.text">
                <button
                  v-if="item.slug && detail.state === 'enabled'"
                  type="button"
                  class="oa-plugin-chip link"
                  @click="switchTab(item.slug); detail = null"
                >
                  <span>{{ item.text }}</span>
                  <IconArrowUpRight :size="12" />
                </button>
                <div v-else class="oa-plugin-chip" :class="{ mono: item.secret !== undefined || item.method }">
                  <span v-if="item.method" class="oa-plugin-chip-method">{{ item.method }}</span>
                  <span>{{ item.text }}</span>
                  <span v-if="item.secret" class="oa-plugin-chip-secret">{{ t('pluginSecret') }}</span>
                </div>
              </template>
            </div>
          </div>
        </div>
      </div>
    </div>
  </OaPanel>

  <OaPanel
    v-if="installing"
    :title="t('pluginInstallTitle', { name: title(installing) })"
    :confirm-label="t('pluginInstall')"
    :width="460"
    :busy="busy"
    :error="panelError"
    @close="installing = null"
    @confirm="install"
  >
    <OaSwitchField v-model="enableNow" :label="t('pluginEnableNow')" :hint="t('pluginEnableNowHint')" />
    <h4 class="oa-field-label">{{ t('pluginInitialSettings') }}</h4>
    <p class="oa-field-hint">{{ sectionsOf(installing.name).length ? t('pluginInitialSettingsHint') : t('pluginNoSettings') }}</p>
    <template v-for="section in sectionsOf(installing.name)" :key="section.id">
      <h4 class="oa-field-label">{{ section.title() }}</h4>
      <PluginSettingControls :section="section" :draft="draft" :hints="noHints" />
    </template>
  </OaPanel>

  <OaPanel
    v-if="confirming"
    :title="confirmTitle"
    :confirmable="confirming.action === 'disable' && (!needCode || hasTwoFactor)"
    :confirm-label="t('pluginDisable')"
    :destructive-label="confirming.action === 'uninstall' && (!needCode || hasTwoFactor) ? t('pluginUninstall') : undefined"
    :destructive-confirm="confirming.action === 'uninstall'
      ? (purge ? t('pluginPurgeConfirm', { name: title(confirming.plugin) }) : t('pluginUninstallConfirm', { name: title(confirming.plugin) }))
      : undefined"
    :width="440"
    :busy="busy"
    :error="panelError"
    @close="confirming = null"
    @confirm="confirm"
    @destructive="confirm"
  >
    <p class="oa-field-hint">{{ confirming.action === 'disable' ? t('pluginDisableHint') : t('pluginUninstallHint') }}</p>
    <OaSwitchField
      v-if="confirming.action === 'uninstall'"
      v-model="purge"
      :label="t('pluginPurge')"
      :hint="confirming.plugin.contributions.purges ? t('pluginPurgeHint') : t('pluginPurgeSettingsOnly')"
    />
    <template v-if="needCode">
      <p v-if="!hasTwoFactor" class="oa-field-hint" role="alert">{{ t('pluginTwoFactorMissing') }}</p>
      <OaTextField
        v-else
        v-model="code"
        :label="t('pluginTwoFactorCode')"
        :hint="t('pluginTwoFactorHint')"
        autocomplete="one-time-code"
        monospace
      />
    </template>
  </OaPanel>

  <OaPanel
    v-if="showNewPluginModal"
    :title="t('pluginInstallNewTitle')"
    :footer="false"
    :width="460"
    @close="showNewPluginModal = false"
  >
    <div class="oa-plugin-guide">
      <div class="oa-plugin-guide-banner">
        <IconPuzzle :size="28" class="oa-plugin-guide-icon" />
        <p class="oa-plugin-guide-text">{{ t('pluginInstallNewAllInstalled') }}</p>
      </div>

      <div class="oa-plugin-guide-section">
        <h4 class="oa-field-label">{{ t('pluginInstallNewGuideTitle') }}</h4>
        <ol class="oa-plugin-guide-steps">
          <li>{{ t('pluginInstallNewGuideStep1') }}</li>
          <li>
            <span>{{ t('pluginInstallNewGuideStep2') }}</span>
            <code class="oa-plugin-guide-code">make build PLUGINS="cardgrant &lt;name&gt;"</code>
          </li>
          <li>{{ t('pluginInstallNewGuideStep3') }}</li>
        </ol>
      </div>

      <div class="oa-plugin-guide-section">
        <h4 class="oa-field-label">一键上传（仅本地或源码环境）</h4>
        <div
          class="oa-plugin-dropzone"
          :class="{ dragover }"
          @dragover.prevent="dragover = true"
          @dragleave="dragover = false"
          @drop.prevent="onDropPlugin"
          @click="choosePlugin"
        >
          <IconDownload v-if="!uploading" :size="24" />
          <IconRefresh v-else :size="24" class="oa-spin" />
          <p>{{ uploading ? '正在上传...' : '将 .zip 插件包拖拽至此处，或点击选择文件' }}</p>
        </div>
        <p class="oa-field-hint" v-if="uploadError" role="alert">{{ uploadError }}</p>
      </div>
    </div>
  </OaPanel>
</template>
