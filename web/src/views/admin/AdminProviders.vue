<script setup lang="ts">
// Providers: the upstream endpoints an administrator points this server at.
//
// The API key is write-only. The form never receives one — the row carries
// only a hint like ••••1234 — so editing a provider's name cannot leak the
// credential into a response, and leaving the key field empty on an edit
// means "keep the one you have" rather than "clear it".

import { computed, nextTick, onMounted, ref } from 'vue';
import { adminApi, type Meta, type Provider, type ProviderKind, type ReasoningStyle } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import { t, tn } from '@/composables/useI18n';
import { IconCopy } from '@/icons';
import { relativeTime } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import { reasoningLabel } from './reasoning-labels';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('providersTitle'), t('providersSubtitle'));

const providers = ref<Provider[]>([]);
const meta = ref<Meta | null>(null);
const error = ref('');
const loaded = ref(false);

const panelOpen = ref(false);
const existing = ref<Provider | null>(null);
/**
 * Values to start from when creating. A copy of a provider is the create form
 * with somebody else's answers in it — except the API key, which the browser
 * has never been given: the payload names the provider to take it from and
 * the server moves the ciphertext without opening it.
 */
const template = ref<Provider | null>(null);
const busy = ref(false);
const panelError = ref('');
const nameField = ref<InstanceType<typeof OaTextField> | null>(null);

const form = ref({
  name: '',
  kind: 'openai' as ProviderKind,
  baseURL: '',
  allowInsecure: false,
  apiKey: '',
  reasoning: 'auto' as ReasoningStyle,
  timeout: 120 as number | null,
  anthropicVersion: '',
  enabled: true,
  sortOrder: 0 as number | null,
});

const creating = computed(() => existing.value === null);

// --- detection ----------------------------------------------------------------

interface Detected {
  model_id: string;
  display_name: string;
  configured: boolean;
  picked: boolean;
}

const detecting = ref(false);
const detected = ref<Detected[] | null>(null);
const detectStatus = ref('');
const addLabel = ref('');

const columns = computed<Array<Column<Provider>>>(() => [
  { key: 'name', header: t('colName') },
  { key: 'type', header: t('colType'), width: '110px' },
  { key: 'key', header: t('colKey'), text: (row) => row.api_key_hint || '—', secondary: true, width: '130px' },
  { key: 'models', header: t('colModels'), text: (row) => String(row.model_count), numeric: true, width: '80px' },
  { key: 'state', header: t('colState'), width: '130px' },
  { key: 'updated', header: t('colUpdated'), text: (row) => relativeTime(row.updated_at), secondary: true, width: '110px' },
]);

function open(row: Provider | null, from: Provider | null = null): void {
  existing.value = row;
  template.value = from;
  panelError.value = '';
  detected.value = null;
  detectStatus.value = '';
  addLabel.value = '';

  const source = row ?? from;
  form.value = {
    name: source?.name ?? '',
    kind: source?.kind ?? 'openai',
    baseURL: source?.base_url ?? '',
    allowInsecure: source?.allow_insecure ?? false,
    apiKey: '',
    reasoning: source?.reasoning_style ?? 'auto',
    timeout: source?.timeout_seconds ?? 120,
    anthropicVersion: source?.anthropic_version ?? '',
    enabled: source?.enabled ?? true,
    sortOrder: source?.sort_order ?? 0,
  };

  panelOpen.value = true;
  void nextTick(() => nameField.value?.focus({ preventScroll: true }));
}

function duplicate(): void {
  const row = existing.value;
  if (!row) return;
  open(null, { ...row, name: t('copyOfName', { name: row.name }) });
}

async function save(): Promise<void> {
  busy.value = true;
  panelError.value = '';
  const payload: Record<string, unknown> = {
    name: form.value.name.trim(),
    kind: form.value.kind,
    base_url: form.value.baseURL.trim(),
    allow_insecure: form.value.allowInsecure,
    reasoning_style: form.value.reasoning,
    timeout_seconds: form.value.timeout ?? 120,
    enabled: form.value.enabled,
    sort_order: form.value.sortOrder ?? 0,
    anthropic_version: form.value.kind === 'anthropic' ? form.value.anthropicVersion.trim() : '',
  };
  // An empty key on an edit keeps the stored one; on a create there is
  // nothing to keep.
  if (form.value.apiKey || creating.value) payload['api_key'] = form.value.apiKey;
  if (template.value) {
    // Headers have no field in this form, and the key is not something this
    // page could send even if it wanted to.
    payload['headers'] = template.value.headers;
    if (!form.value.apiKey) payload['copy_key_from'] = template.value.id;
  }

  try {
    if (creating.value) await adminApi.createProvider(payload);
    else await adminApi.updateProvider(existing.value!.id, payload);
    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function remove(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  busy.value = true;
  try {
    await adminApi.deleteProvider(row.id);
    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function detect(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  detecting.value = true;
  detected.value = null;
  detectStatus.value = '';
  try {
    const { models } = await adminApi.detect(row.id);
    detected.value = models.map((model) => ({ ...model, picked: false }));
    detectStatus.value = t('nModelsFound', { count: models.length });
  } catch (failure) {
    detectStatus.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    detecting.value = false;
  }
}

async function addDetected(): Promise<void> {
  const row = existing.value;
  const picked = (detected.value ?? []).filter((entry) => entry.picked);
  if (!row || !picked.length) return;
  addLabel.value = t('adding');
  try {
    await Promise.all(picked.map((entry) => adminApi.createModel({
      provider_id: row.id,
      model_id: entry.model_id,
      display_name: entry.display_name || entry.model_id,
    })));
    addLabel.value = t('addedN', { count: picked.length });
    for (const entry of picked) {
      entry.picked = false;
      entry.configured = true;
    }
  } catch (failure) {
    addLabel.value = '';
    detectStatus.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    const [providersResult, metaResult] = await Promise.all([adminApi.providers(), adminApi.meta()]);
    providers.value = providersResult.providers;
    meta.value = metaResult;
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn primary" @click="open(null)">{{ t('addProvider') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <OaTable
    v-else
    :columns="columns"
    :rows="providers"
    :empty="t('noProviders')"
    :muted="(row) => !row.enabled"
    selectable
    @select="open($event)"
  >
    <template #cell-name="{ row }">
      <OaCellStack :title="row.name" :sub="row.base_url" />
    </template>
    <template #cell-type="{ row }">
      <OaBadge tone="muted">
        {{ row.kind === 'anthropic' ? t('protocolAnthropic') : t('protocolOpenAI') }}
      </OaBadge>
    </template>
    <template #cell-state="{ row }">
      <OaBadgeRow>
        <OaBadge :tone="row.enabled ? 'muted' : 'danger'">
          {{ row.enabled ? t('enabled') : t('disabled') }}
        </OaBadge>
        <OaBadge v-if="row.reasoning_style !== 'auto'" tone="muted">{{ row.reasoning_style }}</OaBadge>
      </OaBadgeRow>
    </template>
  </OaTable>

  <OaPanel
    v-if="panelOpen && meta"
    :title="creating ? t('addProvider') : existing!.name"
    :confirm-label="creating ? t('add') : t('save')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing
      ? tn(existing.model_count, 'confirmDeleteProviderOne', 'confirmDeleteProviderOther', { name: existing.name })
      : undefined"
    :busy="busy"
    :error="panelError"
    @close="panelOpen = false"
    @confirm="save"
    @destructive="remove"
  >
    <template v-if="existing" #actions>
      <OaIconButton class="oa-icon-btn" :label="t('duplicate')" @click="duplicate">
        <IconCopy :size="16" />
      </OaIconButton>
    </template>

    <OaTextField
      ref="nameField"
      v-model="form.name"
      :label="t('name')"
      placeholder="OpenRouter"
      :hint="t('providerNameHint')"
    />
    <OaSelectField
      v-model="form.kind"
      :label="t('protocol')"
      :hint="t('protocolHint')"
      :options="meta.provider_kinds.map((value) => ({
        value,
        label: value === 'anthropic' ? t('protocolAnthropic') : t('protocolOpenAI'),
      }))"
    />
    <OaTextField
      v-model="form.baseURL"
      :label="t('baseURL')"
      placeholder="https://openrouter.ai/api/v1"
      :hint="t('baseURLHint')"
      monospace
    />
    <OaSwitchField
      v-model="form.allowInsecure"
      :label="t('allowInsecure')"
      :hint="t('allowInsecureHint')"
    />
    <OaTextField
      v-model="form.apiKey"
      :label="creating ? t('apiKey') : t('replaceAPIKey')"
      :placeholder="creating ? 'sk-…' : t('apiKeyKeepHint', { hint: existing!.api_key_hint })"
      :hint="t('apiKeyHint')"
      type="password"
    />

    <OaFormSection :title="t('secBehaviour')" />
    <OaSelectField
      v-model="form.reasoning"
      :label="t('reasoningStyle')"
      :hint="t('reasoningStyleHint')"
      :options="meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) }))"
    />
    <OaTextField
      v-if="form.kind === 'anthropic'"
      v-model="form.anthropicVersion"
      :label="t('anthropicVersion')"
      placeholder="2023-06-01"
      :hint="t('anthropicVersionHint')"
      monospace
    />
    <OaNumberField v-model="form.timeout" :label="t('timeoutSeconds')" :min="5" :max="900" />
    <OaSwitchField v-model="form.enabled" :label="t('enabled')" :hint="t('providerEnabledHint')" />
    <OaNumberField v-model="form.sortOrder" :label="t('sortOrder')" />

    <template v-if="existing">
      <OaFormSection :title="t('navModels')" />
      <button type="button" class="oa-btn" :disabled="detecting" @click="detect">
        {{ detecting ? t('detecting') : t('detect') }}
      </button>

      <div v-if="detectStatus || detected" class="oa-detect-panel">
        <p class="oa-detect-status">{{ detectStatus }}</p>
        <div v-if="detected" class="oa-detect-list">
          <label v-for="entry in detected" :key="entry.model_id" class="oa-detect-row">
            <input v-model="entry.picked" type="checkbox">
            <span>
              {{ entry.display_name ? `${entry.display_name} — ${entry.model_id}` : entry.model_id }}
            </span>
            <span v-if="entry.configured" class="oa-detect-known">{{ t('alreadyAdded') }}</span>
          </label>
        </div>
        <div v-if="detected" class="oa-detect-actions">
          <button type="button" class="oa-btn primary" @click="addDetected">
            {{ addLabel || t('addSelected') }}
          </button>
        </div>
      </div>
    </template>
  </OaPanel>
</template>
