<script setup lang="ts">
// Models: what a provider is asked for, what it can do, and what it costs.
//
// Capabilities are declared rather than discovered because no endpoint will
// tell you. Whether a model reads images or reasons before answering is the
// administrator's answer, and getting it wrong is visible immediately — the
// composer stops offering attachments, or the thinking toggle disappears.

import { computed, nextTick, onMounted, ref } from 'vue';
import {
  adminApi,
  type AdminModel, type Group, type Meta, type ModelHealth, type Provider,
  type ReasoningStyle, type ReasoningTier,
} from '@/admin/api';
import { pickJSONFile, saveAsFile } from '@/api/backup';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import OaTable from '@/components/OaTable.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import OaTierList from '@/components/OaTierList.vue';
import type { ListItem } from '@/components/list-items';
import type { Column, SortState } from '@/components/table-types';
import { t } from '@/composables/useI18n';
import { IconCopy } from '@/icons';
import { compactNumber } from '@/lib/format';
import AdminFailure from './AdminFailure.vue';
import ReasoningTiers from './ReasoningTiers.vue';
import { reasoningLabel } from './reasoning-labels';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('modelsTitle'), t('modelsSubtitle'));

const models = ref<AdminModel[]>([]);
const providers = ref<Provider[]>([]);
const groups = ref<Group[]>([]);
const meta = ref<Meta | null>(null);
const health = ref(new Map<string, ModelHealth>());
const error = ref('');
const loaded = ref(false);

/**
 * Kept out here so that editing a model and coming back does not silently
 * reset the filter the administrator was reading through, and so that the
 * order they chose survives a repaint.
 */
const filters = ref({ ...filterState });
const order = ref<SortState | null>(sortState);

const uploadBusy = ref(false);
const report = ref<{ headline: string; skipped: string[] } | null>(null);

/**
 * One dropdown rather than one per axis.
 *
 * Enabled/disabled, hidden and routed are three independent properties, so a
 * strict reading wants three controls. But the question actually being asked
 * of this table is "show me the X ones", one X at a time, and three dropdowns
 * to answer it is two more than the question needs.
 */
function matches(row: AdminModel): boolean {
  if (filters.value.provider && row.provider_id !== filters.value.provider) return false;
  switch (filters.value.state) {
    case 'enabled': if (!row.enabled) return false; break;
    case 'disabled': if (row.enabled) return false; break;
    case 'hidden': if (!row.hidden) return false; break;
    case 'routed': if (!row.route_to_id) return false; break;
    default: break;
  }
  if (filters.value.q) {
    // The upstream id as well as the name: an administrator hunting for a
    // model usually has the id in hand, and it is the half the reader never
    // sees.
    const haystack = `${row.display_name} ${row.model_id} ${row.provider_name}`.toLowerCase();
    if (!haystack.includes(filters.value.q.toLowerCase())) return false;
  }
  return true;
}

// Filtered here rather than by the server: this screen already holds every
// model in memory to resolve route targets and to name them, so a query would
// be a round trip for a list that is already on the page.
const visible = computed(() => models.value.filter(matches));

function nameOf(modelID: string): string {
  return models.value.find((entry) => entry.id === modelID)?.display_name ?? modelID;
}

const columns = computed<Array<Column<AdminModel>>>(() => [
  {
    key: 'model',
    header: t('colModel'),
    sort: (row) => row.display_name,
  },
  {
    key: 'uptime',
    header: t('colUptime'),
    width: '104px',
    // Worst first when sorted: the reason to sort this column is to find what
    // is broken, and unknown is not broken.
    sort: (row) => {
      const status = health.value.get(row.id)?.status;
      if (!status || status.samples === 0) return 2;
      return status.uptime;
    },
  },
  {
    key: 'provider',
    header: t('colProvider'),
    text: (row) => row.provider_name,
    secondary: true,
    width: '130px',
    // The provider first, then the name, so the models of one provider arrive
    // together and in a readable order.
    sort: (row) => `${row.provider_name}\u0000${row.display_name}`,
  },
  { key: 'can', header: t('colCan'), width: '140px' },
  {
    key: 'weights',
    header: t('colWeights'),
    text: (row) => weightLabel(row),
    numeric: true,
    secondary: true,
    width: '80px',
    // What the cell prints is a pair; what anyone sorts by is the output
    // rate, which is the half that dominates a bill.
    sort: (row) => row.output_token_weight,
  },
  {
    key: 'state',
    header: t('colState'),
    width: '110px',
    // Ascending walks from most available to least: on, on but hidden, off.
    sort: (row) => (row.enabled ? 0 : 2) + (row.hidden ? 1 : 0),
  },
]);

// Three different empty tables: nothing configured, nothing to configure it
// with, and a filter that happens to exclude everything. Saying "no models
// yet" to the third is how somebody concludes their work is gone.
const emptyText = computed(() => {
  if (!providers.value.length) return t('addProviderFirst');
  return models.value.length ? t('noModelsMatch') : t('noModels');
});

function weightLabel(model: AdminModel): string {
  const { input_token_weight: input, output_token_weight: output } = model;
  if (input === 1 && output === 1 && model.request_weight === 0) return '1×';
  return `${compactNumber(input)}× / ${compactNumber(output)}×`;
}

/** A light and a number. Nothing at all when there is no evidence either way. */
function uptime(row: AdminModel): { text: string; tone: 'default' | 'muted' | 'danger' | 'warning'; title?: string } {
  const status = health.value.get(row.id)?.status;
  if (!status || status.samples === 0) return { text: t('healthUnknown'), tone: 'muted' };
  // Down is danger; up but not clean is a warning, because a model at 96% is
  // failing one turn in twenty and that is worth a colour.
  const tone = status.state !== 'up' ? 'danger' : status.uptime >= 0.99 ? 'default' : 'warning';
  const result: { text: string; tone: typeof tone; title?: string } = {
    text: `${(status.uptime * 100).toFixed(status.uptime >= 0.995 ? 0 : 1)}%`,
    tone,
  };
  // The reason, without opening anything: an operator scanning the column for
  // what is broken should not have to click to learn it is the API key.
  if (status.last_code) result.title = `${status.last_code}: ${status.last_message || ''}`.trim();
  return result;
}

function onSort(next: SortState): void {
  order.value = next;
  sortState = next;
}

function onFilter(): void {
  Object.assign(filterState, filters.value);
}

/**
 * Writes back an order a row was dragged into.
 *
 * The list handed back is only what was on screen, which may be a filtered
 * subset. The rows that were filtered out keep the positions they had: the
 * visible ones are dealt back into the slots they occupied, in their new
 * sequence. Moving a row you can see must not move a row you cannot.
 */
async function reorder(reordered: AdminModel[]): Promise<void> {
  const moved = new Set(reordered.map((row) => row.id));
  const queue = [...reordered];
  const next = models.value.map((row) => (moved.has(row.id) ? queue.shift()! : row));
  try {
    await adminApi.reorderModels(next.map((row) => row.id));
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : String(failure);
  }
  // Either way: on success to show the stored order, and on failure to snap
  // back to it rather than leaving the screen claiming a move never written.
  view.reload();
}

// --- the editor -------------------------------------------------------------------

const panelOpen = ref(false);
const existing = ref<AdminModel | null>(null);
const template = ref<AdminModel | null>(null);
const busy = ref(false);
const panelError = ref('');
const modelIDField = ref<InstanceType<typeof OaTextField> | null>(null);

const form = ref({
  providerID: '',
  modelID: '',
  apiName: '',
  systemPrompt: '',
  displayName: '',
  description: '',
  enabled: true,
  hidden: false,
  sortOrder: 0 as number | null,
  groupGrants: {} as Record<string, 'use' | 'view'>,
  reasoning: false,
  images: false,
  vision: false,
  streaming: true,
  systemPromptSupported: true,
  tools: false,
  imageGen: false,
  contextWindow: null as number | null,
  maxOutput: null as number | null,
  routeTo: '',
  reasoningStyle: '' as ReasoningStyle | '',
  tiers: [] as ReasoningTier[],
  requestWeight: 0 as number | null,
  inputWeight: 1 as number | null,
  outputWeight: 1 as number | null,
  reasoningWeight: 1 as number | null,
});

const creating = computed(() => existing.value === null);
const status = computed(() => (existing.value ? health.value.get(existing.value.id) : undefined));

const groupItems = computed<ListItem[]>(() => groups.value.map((group) => ({
  value: group.id,
  label: group.name,
  sub: group.description || undefined,
})));

// Every other model is a candidate except this one and any that is already
// routed: resolution is a single hop, so a chain would not do what the second
// link says. The server refuses both as well.
const routeChoices = computed(() => [
  { value: '', label: t('routeNone') },
  ...models.value
    .filter((entry) => entry.id !== existing.value?.id && !entry.route_to_id)
    .map((entry) => ({ value: entry.id, label: `${entry.display_name} — ${entry.provider_name}` })),
]);

function open(row: AdminModel | null, from: AdminModel | null = null): void {
  existing.value = row;
  template.value = from;
  panelError.value = '';
  detected.value = null;
  detectStatus.value = '';

  // Where the fields start. `existing` still decides everything else: the
  // title, the delete button, and whether saving is a POST or a PATCH.
  const source = row ?? from;

  const grants: Record<string, 'use' | 'view'> = {};
  for (const grant of source?.group_grants ?? []) {
    if (grant.access === 'use' || grant.access === 'view') grants[grant.group_id] = grant.access;
  }

  form.value = {
    providerID: source?.provider_id ?? providers.value[0]?.id ?? '',
    modelID: source?.model_id ?? '',
    apiName: row?.api_name ?? '',
    systemPrompt: source?.system_prompt ?? '',
    displayName: source?.display_name ?? '',
    description: source?.description ?? '',
    enabled: source?.enabled ?? true,
    hidden: source?.hidden ?? false,
    sortOrder: source?.sort_order ?? 0,
    groupGrants: grants,
    reasoning: source?.supports_reasoning ?? false,
    images: source?.supports_images ?? false,
    vision: source?.supports_vision ?? false,
    streaming: source?.supports_streaming ?? true,
    systemPromptSupported: source?.supports_system_prompt ?? true,
    tools: source?.supports_tools ?? false,
    imageGen: source?.supports_image_gen ?? false,
    contextWindow: source?.context_window ?? null,
    maxOutput: source?.max_output_tokens ?? null,
    routeTo: source?.route_to_id ?? '',
    reasoningStyle: source?.reasoning_style ?? '',
    tiers: source?.reasoning_tiers ?? [],
    requestWeight: source?.request_weight ?? 0,
    inputWeight: source?.input_token_weight ?? 1,
    outputWeight: source?.output_token_weight ?? 1,
    reasoningWeight: source?.reasoning_token_weight ?? 1,
  };

  panelOpen.value = true;
  void nextTick(() => modelIDField.value?.focus({ preventScroll: true }));
}

function duplicate(): void {
  const row = existing.value;
  if (!row) return;
  // The API name is dropped rather than suffixed: it is unique across the
  // instance, and a guessed one would be a second public name nobody asked for.
  open(null, { ...row, api_name: '', display_name: t('copyOfName', { name: row.display_name }) });
}

async function save(): Promise<void> {
  busy.value = true;
  panelError.value = '';
  const payload: Record<string, unknown> = {
    route_to_id: form.value.routeTo,
    reasoning_style: form.value.reasoningStyle,
    reasoning_tiers: form.value.tiers,
    model_id: form.value.modelID.trim(),
    api_name: form.value.apiName.trim(),
    system_prompt: form.value.systemPrompt.trim(),
    display_name: form.value.displayName.trim(),
    description: form.value.description.trim(),
    enabled: form.value.enabled,
    hidden: form.value.hidden,
    sort_order: form.value.sortOrder ?? 0,
    supports_reasoning: form.value.reasoning,
    supports_images: form.value.images,
    supports_vision: form.value.vision,
    supports_streaming: form.value.streaming,
    supports_system_prompt: form.value.systemPromptSupported,
    supports_tools: form.value.tools,
    supports_image_gen: form.value.imageGen,
    context_window: form.value.contextWindow ?? 0,
    max_output_tokens: form.value.maxOutput ?? 0,
    request_weight: form.value.requestWeight ?? 0,
    input_token_weight: form.value.inputWeight ?? 1,
    output_token_weight: form.value.outputWeight ?? 1,
    reasoning_token_weight: form.value.reasoningWeight ?? 1,
    group_grants: Object.entries(form.value.groupGrants).map(([group_id, access]) => ({ group_id, access })),
  };
  if (creating.value) payload['provider_id'] = form.value.providerID;
  // The avatar has no field in this form, so a copy would silently lose one
  // that had been set through the API.
  if (template.value) payload['avatar'] = template.value.avatar;

  try {
    if (creating.value) await adminApi.createModel(payload);
    else await adminApi.updateModel(existing.value!.id, payload);
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
    await adminApi.deleteModel(row.id);
    panelOpen.value = false;
    view.reload();
  } catch (failure) {
    busy.value = false;
    panelError.value = failure instanceof ApiError ? failure.message : String(failure);
  }
}

// --- detection ---------------------------------------------------------------------

interface Detected {
  model_id: string;
  display_name: string;
  configured: boolean;
}

const detecting = ref(false);
const detected = ref<Detected[] | null>(null);
const detectStatus = ref('');

/**
 * Asks the provider what it serves and lets one row fill the form.
 *
 * The provider editor's version of this adds every ticked row at once. This
 * one is a picker: the panel it opens into is already creating exactly one
 * model, and the two fields it fills are the two nobody can guess.
 */
async function detect(): Promise<void> {
  if (!form.value.providerID) return;
  detecting.value = true;
  detected.value = null;
  detectStatus.value = '';
  try {
    const { models: found } = await adminApi.detect(form.value.providerID);
    detected.value = found;
    detectStatus.value = t('nModelsFound', { count: found.length });
  } catch (failure) {
    detectStatus.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    detecting.value = false;
  }
}

function useDetected(entry: Detected): void {
  form.value.modelID = entry.model_id;
  form.value.displayName = entry.display_name || entry.model_id;
  detected.value = null;
}

// --- taking the catalogue in and out -------------------------------------------------

/**
 * One model as a file can carry it: names where the database has ids, because
 * a ULID means nothing on the instance this is being taken to.
 */
function portable(row: AdminModel): Record<string, unknown> {
  const target = row.route_to_id ? models.value.find((entry) => entry.id === row.route_to_id) : undefined;
  const groupNameOf = (id: string) => groups.value.find((group) => group.id === id)?.name ?? '';

  return {
    provider: row.provider_name,
    model_id: row.model_id,
    api_name: row.api_name,
    system_prompt: row.system_prompt,
    display_name: row.display_name,
    description: row.description,
    avatar: row.avatar,
    enabled: row.enabled,
    hidden: row.hidden,
    sort_order: row.sort_order,
    reasoning_style: row.reasoning_style,
    reasoning_tiers: row.reasoning_tiers,
    route_to: target ? { provider: target.provider_name, model_id: target.model_id } : null,
    supports_reasoning: row.supports_reasoning,
    supports_images: row.supports_images,
    supports_vision: row.supports_vision,
    supports_streaming: row.supports_streaming,
    supports_system_prompt: row.supports_system_prompt,
    supports_tools: row.supports_tools,
    supports_image_gen: row.supports_image_gen,
    context_window: row.context_window,
    max_output_tokens: row.max_output_tokens,
    request_weight: row.request_weight,
    input_token_weight: row.input_token_weight,
    output_token_weight: row.output_token_weight,
    reasoning_token_weight: row.reasoning_token_weight,
    groups: (row.group_grants ?? [])
      .map((grant) => ({ group: groupNameOf(grant.group_id), access: grant.access }))
      .filter((grant) => grant.group),
  };
}

function exportModels(): void {
  const stamp = new Date().toISOString().slice(0, 10);
  saveAsFile(
    `obsidian-arc-models-${stamp}.json`,
    JSON.stringify({ version: 1, models: models.value.map(portable) }, null, 2),
  );
}

function importModels(): void {
  void pickJSONFile(4 * 1024 * 1024)
    .then((file) => {
      if (file === null) return null;
      const listed = (file as { models?: unknown }).models;
      if (!Array.isArray(listed)) throw new ApiError(0, 'malformed', t('importModelsMalformed'));
      uploadBusy.value = true;
      return adminApi.importModels(listed);
    })
    .then((result) => {
      if (!result) return;
      // Every refusal, not a count of them: "3 skipped" sends an operator back
      // to the file with nothing to look for.
      report.value = {
        headline: t('importModelsDone', { created: result.created, updated: result.updated }),
        skipped: result.skipped,
      };
      view.reload();
    })
    .catch((failure: unknown) => {
      report.value = {
        headline: failure instanceof ApiError ? failure.message : String(failure),
        skipped: [],
      };
    })
    .finally(() => { uploadBusy.value = false; });
}

async function load(): Promise<void> {
  error.value = '';
  try {
    const [modelsResult, providersResult, groupsResult, metaResult] = await Promise.all([
      adminApi.models(),
      adminApi.providers(),
      adminApi.groups(),
      adminApi.meta(),
    ]);
    models.value = modelsResult.models;
    providers.value = providersResult.providers;
    groups.value = groupsResult.groups;
    meta.value = metaResult;
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
    loaded.value = true;
    return;
  }
  loaded.value = true;

  // Liveness is read beside the catalogue rather than as part of it: it is a
  // different question with a different shape, and a failure to answer it must
  // not take the models screen down with it.
  try {
    const health_ = await adminApi.health();
    health.value = new Map(health_.models.map((entry) => [entry.model_id, entry]));
  } catch {
    // The column simply says nothing. An operator came here to edit models.
  }
}

onMounted(load);
</script>

<script lang="ts">
const filterState: { q: string; provider: string; state: '' | 'enabled' | 'disabled' | 'hidden' | 'routed' } = {
  q: '', provider: '', state: '',
};

/**
 * Null is the order the server sent, which is sort_order then name — the
 * order an administrator arranged by hand. That is the right thing to come
 * back to, so no column is sorted until one is clicked.
 */
let sortState: SortState | null = null;
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button
      type="button"
      class="oa-btn"
      :disabled="!models.length"
      @click="exportModels"
    >{{ t('exportModels') }}</button>
    <button
      type="button"
      class="oa-btn"
      :disabled="uploadBusy"
      @click="importModels"
    >{{ t('importModels') }}</button>
    <button
      type="button"
      class="oa-btn primary"
      :disabled="!providers.length"
      :title="providers.length ? undefined : t('addProviderFirst')"
      @click="open(null)"
    >{{ t('addModel') }}</button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <template v-else>
    <div class="oa-filters" :hidden="models.length === 0">
      <input v-model="filters.q" type="search" :placeholder="t('searchModels')" @input="onFilter">
      <OaSelect
        v-model="filters.provider"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyProvider') },
          ...providers.map((provider) => ({ value: provider.id, label: provider.name })),
        ]"
        @update:model-value="onFilter"
      />
      <OaSelect
        v-model="filters.state"
        class="oa-filter-select"
        :choices="[
          { value: '', label: t('anyStatus') },
          { value: 'enabled', label: t('enabled') },
          { value: 'disabled', label: t('disabled') },
          { value: 'hidden', label: t('filterHidden') },
          { value: 'routed', label: t('filterRouted') },
        ]"
        @update:model-value="onFilter"
      />
      <span class="oa-filter-note">{{ t('dragToOrder') }}</span>
    </div>

    <OaTable
      :columns="columns"
      :rows="visible"
      :empty="emptyText"
      :sort="order"
      :muted="(row) => !row.enabled"
      selectable
      reorderable
      @select="open($event)"
      @sort="onSort"
      @reorder="reorder"
    >
      <!-- The route belongs on the name, not in a column of its own: it is
           the answer to "what does this row actually do", and it is blank on
           nearly every row. -->
      <template #cell-model="{ row }">
        <OaCellStack
          :title="row.display_name"
          :sub="row.route_to_id ? t('routedTo', { name: nameOf(row.route_to_id) }) : row.model_id"
        />
      </template>
      <template #cell-uptime="{ row }">
        <OaBadgeRow>
          <OaBadge :tone="uptime(row).tone" :title="uptime(row).title">{{ uptime(row).text }}</OaBadge>
        </OaBadgeRow>
      </template>
      <template #cell-can="{ row }">
        <OaBadgeRow>
          <OaBadge v-if="row.supports_image_gen" tone="muted">{{ t('canImageGen') }}</OaBadge>
          <OaBadge v-if="row.supports_reasoning" tone="muted">{{ t('canThinks') }}</OaBadge>
          <OaBadge v-if="row.supports_vision" tone="muted">{{ t('canSees') }}</OaBadge>
          <OaBadge v-if="row.supports_images && !row.supports_vision" tone="muted">
            {{ t('canImages') }}
          </OaBadge>
          <OaBadge v-if="!row.supports_streaming" tone="muted">{{ t('canNoStream') }}</OaBadge>
          <OaBadge v-if="row.route_to_id" tone="muted">{{ t('routedBadge') }}</OaBadge>
        </OaBadgeRow>
      </template>
      <template #cell-state="{ row }">
        <OaBadgeRow>
          <OaBadge :tone="row.enabled ? 'muted' : 'danger'">
            {{ row.enabled ? t('enabled') : t('disabled') }}
          </OaBadge>
          <OaBadge v-if="row.hidden" tone="muted">{{ t('hiddenBadge') }}</OaBadge>
        </OaBadgeRow>
      </template>
    </OaTable>
  </template>

  <OaPanel
    v-if="panelOpen && meta"
    :title="creating ? t('addModel') : existing!.display_name"
    :confirm-label="creating ? t('add') : t('save')"
    :destructive-label="existing ? t('deleteLabel') : undefined"
    :destructive-confirm="existing ? t('confirmDeleteModel', { name: existing.display_name }) : undefined"
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

    <template v-if="creating">
      <OaSelectField
        v-model="form.providerID"
        :label="t('colProvider')"
        :options="providers.map((provider) => ({ value: provider.id, label: provider.name }))"
      />
      <!-- Detect belongs here as well as on the provider screen: this is the
           form where an upstream id has to be typed exactly, so it is where
           being handed the list saves the typing. -->
      <div class="oa-field">
        <button type="button" class="oa-btn" :disabled="detecting" @click="detect">
          {{ detecting ? t('detecting') : t('detect') }}
        </button>
        <span class="oa-field-hint">{{ t('detectPickHint') }}</span>
        <div v-if="detectStatus || detected" class="oa-detect-panel">
          <p class="oa-detect-status">{{ detectStatus }}</p>
          <div v-if="detected" class="oa-detect-list">
            <button
              v-for="entry in detected"
              :key="entry.model_id"
              type="button"
              class="oa-detect-row"
              @click="useDetected(entry)"
            >
              <span>
                {{ entry.display_name ? `${entry.display_name} — ${entry.model_id}` : entry.model_id }}
              </span>
              <span v-if="entry.configured" class="oa-detect-known">{{ t('alreadyAdded') }}</span>
            </button>
          </div>
        </div>
      </div>
    </template>
    <!-- A field that shows a value the form cannot change. -->
    <div v-else class="oa-field">
      <span class="oa-field-label">{{ t('colProvider') }}</span>
      <span class="oa-field-hint">{{ existing!.provider_name }}</span>
    </div>

    <OaTextField
      ref="modelIDField"
      v-model="form.modelID"
      :label="t('modelIDLabel')"
      placeholder="anthropic/claude-opus-5"
      :hint="t('modelIDHint')"
      monospace
    />
    <OaTextField
      v-model="form.apiName"
      :label="t('apiNameLabel')"
      :placeholder="form.modelID || 'gpt-5.6-sol'"
      :hint="t('apiNameHint')"
      monospace
    />
    <OaTextField
      v-model="form.displayName"
      :label="t('displayName')"
      placeholder="Claude Opus 5"
      :hint="t('displayNameHint')"
      :max-length="80"
    />
    <OaTextArea
      v-model="form.description"
      :label="t('description')"
      :placeholder="t('modelDescriptionPlaceholder')"
      :rows="2"
      :hint="t('modelDescriptionHint')"
    />
    <OaTextArea
      v-model="form.systemPrompt"
      :label="t('modelPromptLabel')"
      :rows="4"
      :hint="t('modelPromptHint')"
    />
    <OaSwitchField v-model="form.enabled" :label="t('enabled')" />
    <OaSwitchField v-model="form.hidden" :label="t('modelHidden')" :hint="t('modelHiddenHint')" />
    <OaNumberField v-model="form.sortOrder" :label="t('sortOrder')" />

    <!-- Why a model is down, in the panel where somebody is about to act on it. -->
    <div v-if="status" class="oa-form-section">
      <h3 class="oa-drawer-subhead">{{ t('secHealth') }}</h3>
      <p v-if="status.status.samples === 0" class="oa-field-hint">{{ t('healthNoEvidence') }}</p>
      <template v-else>
        <p class="oa-field-hint">
          {{ t('healthSummary', {
            uptime: (status.status.uptime * 100).toFixed(1),
            users: status.status.user_samples,
            system: status.status.system_samples,
          }) }}
        </p>
        <p v-if="status.auto_disabled" class="oa-field-hint">{{ t('healthAutoDisabled') }}</p>
        <div v-if="status.status.errors.length" class="oa-code-list">
          <code v-for="failure in status.status.errors" :key="failure.code" class="oa-code-line">
            {{ failure.count }}x  {{ failure.code }}{{ failure.message ? '  ' + failure.message : '' }}
          </code>
        </div>
      </template>
    </div>

    <OaFormSection :title="t('secGroupAccess')" />
    <OaTierList
      v-model="form.groupGrants"
      :label="t('groupsTitle')"
      :hint="t('groupAccessHint')"
      :items="groupItems"
      :empty-text="t('noGroups')"
    />

    <OaFormSection :title="t('secCapabilities')" :hint="t('capabilitiesHint')" />
    <OaSwitchField v-model="form.imageGen" :label="t('capImageGen')" />
    <OaSwitchField v-model="form.reasoning" :label="t('capReasoning')" :hint="t('capReasoningHint')" />
    <OaSwitchField v-model="form.images" :label="t('capImages')" :hint="t('capImagesHint')" />
    <OaSwitchField v-model="form.vision" :label="t('capVision')" />
    <OaSwitchField v-model="form.streaming" :label="t('capStreams')" />
    <OaSwitchField v-model="form.systemPromptSupported" :label="t('capSystemPrompt')" />
    <OaSwitchField v-model="form.tools" :label="t('capTools')" />
    <OaNumberField v-model="form.contextWindow" :label="t('contextWindow')" placeholder="200000" :min="0" />
    <OaNumberField v-model="form.maxOutput" :label="t('maxOutputTokens')" placeholder="8192" :min="0" />

    <OaFormSection :title="t('secRouting')" />
    <OaSelectField
      v-model="form.routeTo"
      :label="t('routeTo')"
      :hint="t('routeToHint')"
      :options="routeChoices"
    />

    <!-- The style and the tiers are one subject — how this model is asked to
         think — and they used to sit under Routing, which is a different one. -->
    <OaFormSection :title="t('secThinking')" />
    <OaSelectField
      v-model="form.reasoningStyle"
      :label="t('reasoningStyleModel')"
      :hint="t('reasoningStyleModelHint')"
      :options="[
        { value: '', label: t('styleInherit') },
        ...meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) })),
      ]"
    />
    <ReasoningTiers v-model="form.tiers" />

    <OaFormSection :title="t('secWeights')" :hint="t('weightsHint')" />
    <OaNumberField
      v-model="form.requestWeight"
      :label="t('perRequest')"
      :step="0.1"
      :min="0"
      :hint="t('perRequestHint')"
    />
    <OaNumberField v-model="form.inputWeight" :label="t('per1kInput')" :step="0.1" :min="0" />
    <OaNumberField v-model="form.outputWeight" :label="t('per1kOutput')" :step="0.1" :min="0" />
    <OaNumberField v-model="form.reasoningWeight" :label="t('per1kReasoning')" :step="0.1" :min="0" />
  </OaPanel>

  <!-- What an import did, and every entry it would not take. -->
  <OaPanel
    v-if="report"
    :title="report.headline"
    :footer="false"
    @close="report = null"
  >
    <p v-if="!report.skipped.length" class="oa-field-hint">{{ t('importModelsClean') }}</p>
    <template v-else>
      <p class="oa-field-hint">{{ t('importModelsSkipped', { count: report.skipped.length }) }}</p>
      <div class="oa-code-list">
        <code v-for="line in report.skipped" :key="line" class="oa-code-line">{{ line }}</code>
      </div>
    </template>
  </OaPanel>
</template>
