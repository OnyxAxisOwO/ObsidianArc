<script setup lang="ts">
// Providers: the upstream endpoints an administrator points this server at.
//
// The API keys are write-only. The form never receives one — the row carries
// only hints like ••••1234 — so editing a provider's name cannot leak a
// credential into a response. A stored key is kept or removed by its position
// in those hints, and the hints go back with the save so the server can tell
// whether the list it is changing is still the one that was shown.

import { computed, nextTick, onMounted, ref } from 'vue';
import { adminApi, type KeyRotation, type Meta, type Provider, type ProviderKind, type ReasoningStyle } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaBulkBar from '@/components/OaBulkBar.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaRow from '@/components/OaRow.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextField from '@/components/OaTextField.vue';
import type { Column } from '@/components/table-types';
import { t, tn } from '@/composables/useI18n';
import { useBulk } from '@/composables/useBulk';
import { usePanelSlot } from '@/composables/usePanelSlot';
import { IconCopy, IconTrash, IconUndo } from '@/icons';
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
  keyRotation: 'sequential' as KeyRotation,
  reasoning: 'auto' as ReasoningStyle,
  timeout: 120 as number | null,
  anthropicVersion: '',
  enabled: true,
  sortOrder: 0 as number | null,
});

const creating = computed(() => existing.value === null);

// --- keys ---------------------------------------------------------------------------

/** Typed this visit and not saved yet. The browser has these; it never had the stored ones. */
const pendingKeys = ref<string[]>([]);
/** Positions in `storedHints` the operator took out. */
const removedKeys = ref<number[]>([]);

/** The keys already there: this provider's, or on a duplicate, its source's. */
const storedHints = computed(() => (existing.value ?? template.value)?.api_key_hints ?? []);

/**
 * Whether the address is not the one the stored keys were typed for. They
 * never follow it — the server refuses — so they are shown as going and only
 * typed ones are sent. Only spelling is ignored here; the server normalises
 * further and decides.
 */
const moved = computed(() => {
  const source = existing.value ?? template.value;
  const bare = (value: string) => value.trim().replace(/\/+$/, '');
  return source !== null && bare(form.value.baseURL) !== bare(source.base_url);
});

const keptPositions = computed(() => (moved.value
  ? []
  : storedHints.value.map((_, position) => position).filter((position) => !removedKeys.value.includes(position))));

function splitKeys(text: string): string[] {
  return text.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

/** Everything typed, the field's own text included: a key left in it when Save is pressed was meant to be added. */
function typedKeys(): string[] {
  return [...new Set([...pendingKeys.value, ...splitKeys(form.value.apiKey)])];
}

const keyCount = computed(() => keptPositions.value.length + typedKeys().length);

function addTyped(): void {
  const keys = splitKeys(form.value.apiKey);
  if (!keys.length) return;
  pendingKeys.value = [...new Set([...pendingKeys.value, ...keys])];
  form.value.apiKey = '';
}

/**
 * A password field drops the line breaks of what is pasted into it, so a list
 * of keys would arrive as one long wrong key. A paste of several lines is
 * taken out of the field's hands and added as that many keys.
 */
function pasteKeys(event: ClipboardEvent): void {
  const text = event.clipboardData?.getData('text') ?? '';
  if (splitKeys(text).length < 2) return;
  event.preventDefault();
  form.value.apiKey = text;
  addTyped();
}

function toggleStored(position: number): void {
  removedKeys.value = removedKeys.value.includes(position)
    ? removedKeys.value.filter((entry) => entry !== position)
    : [...removedKeys.value, position];
}

/** The same shape as the server's hint, so a new key reads like the stored ones beside it. */
function hintOf(key: string): string {
  return `••••${[...key].slice(-4).join('')}`;
}

function storedMeta(position: number): string | undefined {
  if (moved.value) return t('apiKeyDroppedOnMove');
  if (removedKeys.value.includes(position)) return t('apiKeyRemoved');
  return undefined;
}

const columns = computed<Array<Column<Provider>>>(() => [
  { key: 'name', header: t('colName') },
  { key: 'type', header: t('colType'), width: '110px' },
  { key: 'key', header: t('colKey'), text: keySummary, secondary: true, width: '130px' },
  { key: 'models', header: t('colModels'), text: (row) => String(row.model_count), numeric: true, width: '80px' },
  { key: 'state', header: t('colState'), width: '130px' },
  { key: 'updated', header: t('colUpdated'), text: (row) => relativeTime(row.updated_at), secondary: true, width: '110px' },
]);

function keySummary(row: Provider): string {
  if (!row.api_key_hint) return '—';
  const hint = maskCredential(row.api_key_hint);
  const count = row.api_key_hints?.length ?? 1;
  return count > 1 ? t('apiKeysSummary', { hint, count, more: count - 1 }) : hint;
}

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
    keyRotation: source?.key_rotation ?? 'sequential',
    reasoning: source?.reasoning_style ?? 'auto',
    timeout: source?.timeout_seconds ?? 120,
    anthropicVersion: source?.anthropic_version ?? '',
    enabled: source?.enabled ?? true,
    sortOrder: source?.sort_order ?? 0,
  };

  pendingKeys.value = [];
  removedKeys.value = [];

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
    key_rotation: form.value.keyRotation,
  };
  const typed = typedKeys();
  const kept = keptPositions.value;
  const everyStored = kept.length === storedHints.value.length;
  if (typed.length) payload['api_keys'] = typed;
  // Positions are sent only when the list changes: an edit that leaves the
  // keys alone should not fail because someone else changed them meanwhile.
  const sendKept = () => {
    payload['keep_keys'] = kept;
    payload['key_hints'] = storedHints.value;
  };
  if (template.value) {
    // Headers have no field in this form, and the keys are not something this
    // page could send even if it wanted to: it names the provider to take
    // them from, and which of them.
    payload['headers'] = template.value.headers;
    if (kept.length) {
      payload['copy_key_from'] = template.value.id;
      if (!everyStored || typed.length) sendKept();
    }
  } else if (!creating.value && (typed.length || !everyStored)) {
    sendKept();
  }

  try {
    if (creating.value) await adminApi.createProvider(payload);
    else await adminApi.updateProvider(existing.value!.id, payload);
    finish();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? saveFailureText(failure) : String(failure);
  }
}

/**
 * The server words its refusals in English. The two a person can act on from
 * this form are translated here, so they read in the language they chose.
 */
function saveFailureText(failure: ApiError): string {
  if (failure.code === 'provider_key_needed') return t('providerKeyNeeded');
  if (failure.code === 'super_admin_required') return t('providerBaseURLSuperAdmin');
  if (failure.code === 'provider_keys_changed') return t('providerKeysChanged');
  return failure.message;
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
      <OaRow
        v-for="(hint, position) in storedHints"
        :key="`stored-${position}`"
        class="oa-provider-key"
        :class="{ dropped: moved || removedKeys.includes(position) }"
        :title="maskCredential(hint)"
        :meta="storedMeta(position)"
      >
        <OaIconButton
          v-if="!moved"
          class="oa-icon-btn tiny"
          :label="removedKeys.includes(position) ? t('apiKeyKeep') : t('remove')"
          @click="toggleStored(position)"
        >
          <IconUndo v-if="removedKeys.includes(position)" :size="13" />
          <IconTrash v-else :size="13" />
        </OaIconButton>
      </OaRow>
      <OaRow
        v-for="key in pendingKeys"
        :key="`new-${key}`"
        class="oa-provider-key"
        :title="hintOf(key)"
        :meta="t('apiKeyNew')"
      >
        <OaIconButton
          class="oa-icon-btn tiny"
          :label="t('remove')"
          @click="pendingKeys = pendingKeys.filter((entry) => entry !== key)"
        >
          <IconTrash :size="13" />
        </OaIconButton>
      </OaRow>
      <!-- Enter adds the key to the list above rather than saving the form,
           so a pool of keys is typed one after another in the same field. -->
      <OaTextField
        v-model="form.apiKey"
        :label="storedHints.length || pendingKeys.length ? t('addAPIKey') : t('apiKey')"
        placeholder="sk-…"
        :hint="t('apiKeysHint')"
        type="password"
        autocomplete="off"
        @keydown.enter.prevent.stop="addTyped"
        @paste="pasteKeys"
      />
      <OaSelectField
        v-if="keyCount > 1"
        v-model="form.keyRotation"
        :label="t('keyRotation')"
        :hint="t('keyRotationHint')"
        :options="[
          { value: 'sequential', label: t('keyRotationSequential') },
          { value: 'random', label: t('keyRotationRandom') },
        ]"
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
