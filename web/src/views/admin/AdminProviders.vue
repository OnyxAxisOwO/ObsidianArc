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
import OaBulkBar from '@/components/OaBulkBar.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import { t, tn } from '@/composables/useI18n';
import { useBulk } from '@/composables/useBulk';
import { usePanelSlot } from '@/composables/usePanelSlot';
import { IconCopy } from '@/icons';
import { relativeTime } from '@/lib/format';
import { canAdmin } from '@/stores/session';
import { isMasked, maskProvider, maskCredential } from '@/admin/safeMode';
import AdminControlCard from './AdminControlCard.vue';
import AdminDetectModels from './AdminDetectModels.vue';
import AdminFailure from './AdminFailure.vue';
import { reasoningLabel } from './reasoning-labels';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('providersTitle'), t('providersSubtitle'));

const providers = ref<Provider[]>([]);
const meta = ref<Meta | null>(null);
const error = ref('');
const loaded = ref(false);

const { open: panelOpen, panel, key: panelKey, show: showPanel, hide: hidePanel, closed: panelClosed } = usePanelSlot();
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

const columns = computed<Array<Column<Provider>>>(() => [
  { key: 'name', header: t('colName') },
  { key: 'type', header: t('colType'), width: '110px' },
  { key: 'key', header: t('colKey'), text: (row) => row.api_key_hint ? maskCredential(row.api_key_hint) : '—', secondary: true, width: '130px' },
  { key: 'models', header: t('colModels'), text: (row) => String(row.model_count), numeric: true, width: '80px' },
  { key: 'state', header: t('colState'), width: '130px' },
  { key: 'updated', header: t('colUpdated'), text: (row) => relativeTime(row.updated_at), secondary: true, width: '110px' },
]);

function open(row: Provider | null, from: Provider | null = null): void {
  existing.value = row;
  template.value = from;
  panelError.value = '';

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

  // Saving leaves this set while the panel slides out.
  busy.value = false;
  showPanel();
  void nextTick(() => nameField.value?.focus({ preventScroll: true }));
}

function duplicate(): void {
  const row = existing.value;
  if (!row) return;
  open(null, { ...row, name: t('copyOfName', { name: row.name }) });
}

/**
 * The panel slides away and the table is refetched where it stands, rather
 * than the page being mounted again — which put the reader back at the top.
 */
function finish(): void {
  hidePanel();
  void load();
}

// --- several at once ----------------------------------------------------------------

const bulk = useBulk(() => providers.value, (row) => row.id);

function bulkEnable(on: boolean): Promise<void> {
  return bulk.run((row) => (row.enabled === on ? Promise.resolve() : adminApi.updateProvider(row.id, { enabled: on })), load);
}

function bulkRemove(): Promise<void> {
  return bulk.run((row) => adminApi.deleteProvider(row.id), load);
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
    finish();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError
      ? (failure.code === 'provider_key_needed' ? t('providerKeyNeeded') : failure.message)
      : String(failure);
  }
}

async function remove(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  busy.value = true;
  try {
    await adminApi.deleteProvider(row.id);
    finish();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
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
    <button id="addProvider" type="button" class="oa-btn primary" @click="open(null)">{{ t('addProvider') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
  <OaTable
    id="providersList"
    :columns="columns"
    :rows="providers"
    :empty="t('noProviders')"
    :muted="(row) => !row.enabled"
    selectable
    multi
    v-model:selected="bulk.selected.value"
    :row-key="(row) => row.id"
    @select="open($event)"
  >
    <template #cell-name="{ row }">
      <OaCellStack :title="maskProvider(row.name)" :sub="maskProvider(row.base_url)" />
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
  <OaBulkBar
    :count="bulk.selected.value.length"
    :total="providers.length"
    :busy="bulk.busy.value"
    :error="bulk.error.value"
    deletable
    :delete-question="t('bulkDeleteProvidersConfirm', { count: bulk.selected.value.length })"
    @all="bulk.selectAll"
    @clear="bulk.clear"
    @delete="bulkRemove"
  >
    <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkEnable(true)">{{ t('bulkEnable') }}</button>
    <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkEnable(false)">{{ t('bulkDisable') }}</button>
  </OaBulkBar>
  </template>

  <OaPanel
    v-if="panelOpen && meta"
    ref="panel"
    :key="panelKey"
    :title="creating ? t('addProvider') : maskProvider(existing!.name)"
    :confirm-label="creating ? t('add') : t('save')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing
      ? tn(existing.model_count, 'confirmDeleteProviderOne', 'confirmDeleteProviderOther', { name: maskProvider(existing.name) })
      : undefined"
    :busy="busy"
    :error="panelError"
    @close="panelClosed"
    @confirm="save"
    @destructive="remove"
  >
    <template v-if="existing" #actions>
      <OaIconButton class="oa-icon-btn" :label="t('duplicate')" @click="duplicate">
        <IconCopy :size="16" />
      </OaIconButton>
    </template>

    <AdminControlCard :title="t('secProviderConnection')">
      <!-- Masking either field would break editing it, so the value stays real
           and only its focus state decides whether it can be read — blurred
           until the operator clicks in, same as a name or an endpoint typed on
           a shared screen would need. -->
      <OaTextField
        ref="nameField"
        v-model="form.name"
        :class="{ 'oa-safe-blur': isMasked('providers') }"
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
        :class="{ 'oa-safe-blur': isMasked('providers') }"
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
        :placeholder="creating ? 'sk-…' : t('apiKeyKeepHint', { hint: maskCredential(existing!.api_key_hint) })"
        :hint="t('apiKeyHint')"
        type="password"
      />
    </AdminControlCard>

    <AdminControlCard :title="t('secBehaviour')">
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
    </AdminControlCard>

    <AdminControlCard v-if="existing" :title="t('navModels')">
      <AdminDetectModels :provider-id="existing.id" mode="add" :can-add="canAdmin('models')" />
    </AdminControlCard>
  </OaPanel>
</template>
