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

import { computed, onMounted, reactive, ref } from 'vue';
import { adminApi, type AdminPlugin, type PluginChange, type PluginText } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { currentLanguage, t } from '@/composables/useI18n';
import { IconPuzzle } from '@/icons';
import { absoluteTime } from '@/lib/format';
import { loadPluginModule } from '@/plugins/registry';
import type { ArcPlugin } from '@/plugins/types';
import { canAdmin, currentUser, refreshSite } from '@/stores/session';
import AdminControlCard from './AdminControlCard.vue';
import AdminFailure from './AdminFailure.vue';
import PluginSettingControls from './PluginSettingControls.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navPlugins'), t('pluginsSubtitle'));

const list = ref<AdminPlugin[]>([]);
const loaded = ref(false);
const error = ref('');
/** The browser halves, by name, for the icon and the declared settings and pages. */
const modules = reactive<Record<string, ArcPlugin | null>>({});

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
    const { plugins } = await adminApi.plugins();
    list.value = plugins;
    loaded.value = true;
    await Promise.all(plugins.filter((plugin) => !plugin.missing && !(plugin.name in modules)).map(async (plugin) => {
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
}

function openDetail(plugin: AdminPlugin): void {
  closePanels();
  detail.value = plugin;
}

function listed(values: string[]): string {
  return values.length ? values.join(', ') : t('pluginNothing');
}

const detailRows = computed(() => {
  const plugin = detail.value;
  if (!plugin) return [];
  const c = plugin.contributions;
  const pages = modules[plugin.name]?.adminPages?.map((page) => page.title()) ?? [];
  return [
    { label: t('pluginSettingsKeys'), value: listed(c.settings.map((s) => s.secret ? `${s.key} (${t('pluginSecret')})` : s.key)) },
    { label: t('pluginFields'), value: listed(c.fields) },
    { label: t('pluginGuards'), value: listed(c.guards) },
    { label: t('pluginCaptchaModes'), value: listed(c.captcha_modes) },
    { label: t('pluginRoutes'), value: listed([...c.admin_routes.map((r) => r.pattern), ...c.public_routes]) },
    { label: t('pluginCommands'), value: listed(c.commands) },
    { label: t('pluginMigrations'), value: listed(c.migrations) },
    { label: t('pluginPages'), value: listed(pages) },
  ];
});

// --- install ----------------------------------------------------------------

const installing = ref<AdminPlugin | null>(null);
const enableNow = ref(true);
const draft = reactive<Record<string, string>>({});
const noHints: Record<string, string> = {};
const busy = ref(false);
const panelError = ref('');

function openInstall(plugin: AdminPlugin): void {
  closePanels();
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

  <OaPanel
    v-if="detail"
    :title="title(detail)"
    :footer="false"
    :width="460"
    @close="detail = null"
  >
    <p class="oa-field-hint">{{ text(detail.manifest.description) }}</p>
    <h4 class="oa-field-label">{{ t('pluginManifest') }}</h4>
    <dl class="oa-resource-definition">
      <div><dt>{{ t('pluginVersionLabel') }}</dt><dd>{{ detail.manifest.version || '—' }}</dd></div>
      <div v-if="detail.manifest.author"><dt>{{ t('pluginAuthor') }}</dt><dd>{{ detail.manifest.author }}</dd></div>
      <div v-if="detail.manifest.license"><dt>{{ t('pluginLicense') }}</dt><dd>{{ detail.manifest.license }}</dd></div>
      <div v-if="detail.manifest.homepage"><dt>{{ t('pluginHomepage') }}</dt><dd>{{ detail.manifest.homepage }}</dd></div>
      <div><dt>{{ t('status') }}</dt><dd><OaBadge :tone="stateTone(detail)">{{ stateLabel(detail) }}</OaBadge></dd></div>
      <div v-if="detail.installed.at"><dt>{{ t('pluginInstalledAt') }}</dt><dd>{{ absoluteTime(detail.installed.at) }}</dd></div>
      <div v-if="detail.installed.version"><dt>{{ t('pluginInstalledVersion') }}</dt><dd>{{ detail.installed.version }}</dd></div>
    </dl>
    <h4 class="oa-field-label">{{ t('pluginContributes') }}</h4>
    <dl class="oa-resource-definition">
      <div v-for="row in detailRows" :key="row.label"><dt>{{ row.label }}</dt><dd class="oa-plugin-list">{{ row.value }}</dd></div>
    </dl>
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
    :confirmable="confirming.action === 'disable' && hasTwoFactor"
    :confirm-label="t('pluginDisable')"
    :destructive-label="confirming.action === 'uninstall' && hasTwoFactor ? t('pluginUninstall') : undefined"
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
    <p v-if="!hasTwoFactor" class="oa-field-hint" role="alert">{{ t('pluginTwoFactorMissing') }}</p>
    <OaTextField
      v-else
      v-model="code"
      :label="t('pluginTwoFactorCode')"
      :hint="t('pluginTwoFactorHint')"
      autocomplete="one-time-code"
      monospace
    />
  </OaPanel>
</template>
