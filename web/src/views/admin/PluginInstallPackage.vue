<script setup lang="ts">
// What an uploaded plugin package asks before it is installed.
//
// The file has been read by the server and nothing else: nothing is stored,
// nothing runs. This panel is the moment the operator sees who wrote it, what
// version it is, and above all what it asks to be allowed to do — the same
// question a browser asks of an extension — and answers with the settings it
// wants to begin with. The install is one request that either does all of it
// or none.

import { computed, reactive, ref } from 'vue';
import { adminApi, type PackageSetting, type PluginChange, type PluginPreview, type PluginText } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { currentLanguage, t } from '@/composables/useI18n';
import { IconInfo, IconPuzzle } from '@/icons';
import type { SettingControl, SettingsSection } from '@/plugins/types';
import PluginSettingControls from './PluginSettingControls.vue';
import { permissionsOf } from './pluginPermissions';

const props = defineProps<{
  preview: PluginPreview;
  /** Whether the server wants a two-step code for plugin changes. */
  twoFactorRequired: boolean;
  /** Whether this account has two-step verification to give one from. */
  hasTwoFactor: boolean;
}>();

const emit = defineEmits<{
  (event: 'close'): void;
  (event: 'done', change: PluginChange & { did: 'install' | 'update' | 'adopt' }): void;
}>();

function words(value: Partial<PluginText> | undefined): string {
  if (!value) return '';
  return (currentLanguage() === 'zh' ? value.zh : value.en) || value.en || '';
}

const manifest = computed(() => props.preview.manifest);
const name = computed(() => words(manifest.value.title) || manifest.value.name);
const updating = computed(() => props.preview.action === 'update');
const unchanged = computed(() => props.preview.action === 'unchanged');

const permissions = computed(() => permissionsOf(manifest.value.permissions));

const runsInPage = computed(() => props.preview.warnings.includes('ui'));
const downgrade = computed(() => props.preview.warnings.includes('downgrade'));

// The manifest's settings, drawn with the controls every other plugin setting
// is drawn with, so the install panel and the settings page cannot disagree
// about what a select or a secret looks like.
const settingSection = computed<SettingsSection | null>(() => {
  const asked = (manifest.value.settings ?? []).filter((s) => s.initial);
  if (!asked.length) return null;
  const controls: SettingControl[] = asked.map((s) => controlFor(s));
  return {
    id: 'install', page: 'security', category: '', column: 0,
    title: () => name.value, controls,
    defaults: Object.fromEntries(asked.map((s) => [s.key, s.default ?? ''])),
  };
});

function controlFor(s: PackageSetting): SettingControl {
  const label = () => words(s.label) || s.key;
  const hint = s.hint ? () => words(s.hint) : undefined;
  if (s.enum?.length) {
    return {
      kind: 'select', key: s.key, label, ...(hint ? { hint } : {}),
      options: s.enum.map((value) => ({ value, label: () => value })),
    };
  }
  if (s.secret) return { kind: 'secret', key: s.key, label, ...(hint ? { hint } : {}) };
  return { kind: 'text', key: s.key, label, ...(hint ? { hint } : {}) };
}

const draft = reactive<Record<string, string>>({});
for (const control of settingSection.value?.controls ?? []) {
  draft[control.key] = control.kind === 'secret' ? '' : settingSection.value?.defaults[control.key] ?? '';
}
const noHints: Record<string, string> = {};
const enableNow = ref(true);
const code = ref('');
const busy = ref(false);
const error = ref('');

const confirmable = computed(() => !unchanged.value && (!props.twoFactorRequired || props.hasTwoFactor));

async function install(): Promise<void> {
  busy.value = true;
  error.value = '';
  // An untouched secret is not sent: empty is "none", and the install writes
  // nothing it was not given.
  const settings: Record<string, string> = {};
  for (const control of settingSection.value?.controls ?? []) {
    const value = (draft[control.key] ?? '').trim();
    if (control.kind === 'secret' && !value) continue;
    settings[control.key] = value;
  }
  try {
    emit('done', await adminApi.installPluginPackage(props.preview.token, enableNow.value, settings, code.value.trim()));
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

function size(bytes: number): string {
  return bytes >= 1 << 20 ? `${(bytes / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} kB`;
}
</script>

<template>
  <OaPanel
    :title="updating ? t('pluginPackageUpdateTitle', { name }) : t('pluginPackageInstallTitle', { name })"
    :confirm-label="updating ? t('pluginUpdate') : t('pluginInstall')"
    :confirmable="confirmable"
    :width="480"
    :busy="busy"
    :error="error"
    @close="emit('close')"
    @confirm="install"
  >
    <div class="oa-plugin-detail">
      <div class="oa-plugin-detail-hero">
        <div class="oa-plugin-detail-icon"><IconPuzzle :size="24" /></div>
        <div class="oa-plugin-detail-lead">
          <div class="oa-plugin-detail-header-row">
            <h3 class="oa-plugin-detail-name">{{ name }}</h3>
            <span class="oa-plugin-detail-slug">{{ manifest.name }}</span>
          </div>
          <div class="oa-plugin-detail-badge-row">
            <span class="oa-plugin-detail-version">v{{ manifest.version }}</span>
            <span v-if="manifest.author" class="oa-field-hint">{{ manifest.author }}</span>
          </div>
          <p class="oa-plugin-detail-desc">{{ words(manifest.description) }}</p>
        </div>
      </div>

      <p v-if="updating && preview.existing" class="oa-field-hint">
        {{ t('pluginPackageUpdateHint', { from: preview.existing.version, to: manifest.version }) }}
      </p>
      <p v-if="downgrade" class="oa-plugin-notice warn" role="alert">
        <IconInfo :size="14" /><span>{{ t('pluginPackageDowngrade') }}</span>
      </p>
      <p v-if="unchanged" class="oa-plugin-notice warn" role="alert">
        <IconInfo :size="14" /><span>{{ t('pluginPackageUnchanged') }}</span>
      </p>

      <div class="oa-plugin-detail-section">
        <div class="oa-plugin-detail-section-head">
          <h4 class="oa-field-label">{{ t('pluginPermissions') }}</h4>
        </div>
        <p v-if="!permissions.length" class="oa-field-hint">{{ t('pluginNoPermissions') }}</p>
        <ul v-else class="oa-plugin-permissions">
          <li v-for="permission in permissions" :key="permission.id" :class="{ risky: permission.risky }">
            <span class="oa-plugin-permission-text">{{ t(permission.label) }}</span>
            <OaBadge v-if="permission.risky" tone="warning">{{ t('pluginRiskHigh') }}</OaBadge>
          </li>
        </ul>
        <div v-if="runsInPage" class="oa-plugin-notice warn">
          <IconInfo :size="14" />
          <span><strong>{{ t('pluginRunsInPage') }}</strong> — {{ t('pluginRunsInPageHint') }}</span>
        </div>
      </div>

      <div v-if="settingSection" class="oa-plugin-detail-section">
        <div class="oa-plugin-detail-section-head">
          <h4 class="oa-field-label">{{ t('pluginInitialSettings') }}</h4>
        </div>
        <p class="oa-field-hint">{{ t('pluginInitialSettingsHint') }}</p>
        <PluginSettingControls :section="settingSection" :draft="draft" :hints="noHints" />
      </div>

      <OaSwitchField v-model="enableNow" :label="t('pluginEnableNow')" :hint="t('pluginEnableNowHint')" />

      <template v-if="twoFactorRequired">
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

      <dl class="oa-plugin-fingerprint">
        <dt>{{ t('pluginFingerprint') }}</dt>
        <dd class="mono">{{ preview.sha256.slice(0, 16) }}…</dd>
        <dt>{{ t('pluginSize') }}</dt>
        <dd>{{ size(preview.size) }}</dd>
      </dl>
    </div>
  </OaPanel>
</template>
