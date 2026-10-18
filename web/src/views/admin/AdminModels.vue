<script setup lang="ts">
// Models: what a provider is asked for, what it can do, and what it costs.
//
// Capabilities are declared rather than discovered because no endpoint will
// tell you. Whether a model reads images or reasons before answering is the
// administrator's answer, and getting it wrong is visible immediately — the
// composer stops offering attachments, or the thinking toggle disappears.

import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import {
  adminApi,
  type AdminModel, type Group, type Meta, type ModelHealth, type Provider,
  type UsageBreakdown,
} from '@/admin/api';
import { pickJSONFile, saveAsFile } from '@/api/backup';
import { ApiError } from '@/api/client';
import { DAY_MS } from '@/lib/time';
import OaBadge from '@/components/OaBadge.vue';
import OaBadgeRow from '@/components/OaBadgeRow.vue';
import OaBulkBar from '@/components/OaBulkBar.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaSelect from '@/components/OaSelect.vue';
import OaTable from '@/components/OaTable.vue';
import type { Column, SortState } from '@/components/table-types';
import { copyToClipboard } from '@/chat/markdown';
import { t } from '@/composables/useI18n';
import { useBulk } from '@/composables/useBulk';
import { usePanelSlot } from '@/composables/usePanelSlot';
import { compactNumber } from '@/lib/format';
import { canAdmin } from '@/stores/session';
import { maskProvider } from '@/admin/safeMode';
import type { DetectedModel } from './AdminDetectModels.vue';
import AdminModelEditor from './AdminModelEditor.vue';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';
import { blankModelForm, formFromModel, isOverrideObject, modelPayload, type ModelForm } from './modelForm';
import { failureText, runRemove } from './shared';
import { FEEDBACK_FLASH_MS } from '@/lib/flash';

const route = useRoute();
const view = useAdminView();
view.setTitle(() => t('modelsTitle'), () => t('modelsSubtitle'));

const idCopied = ref(false);
// Kept so the reset can be cancelled: a page left within 1.5s would otherwise
// get its timer fired into a component that is already gone.
let idCopiedTimer = 0;

async function copyID(value: string): Promise<void> {
  // The shared helper has the execCommand fallback. A bare writeText did nothing
  // on a plain-http origin, where navigator.clipboard does not exist, and the
  // button gave no sign that it had failed.
  if (!(await copyToClipboard(value))) return;
  idCopied.value = true;
  window.clearTimeout(idCopiedTimer);
  idCopiedTimer = window.setTimeout(() => { idCopied.value = false; }, FEEDBACK_FLASH_MS);
}

const models = ref<AdminModel[]>([]);
const providers = ref<Pick<Provider, 'id' | 'name' | 'kind' | 'enabled'>[]>([]);
const groups = ref<Pick<Group, 'id' | 'name'>[]>([]);
const meta = ref<Meta | null>(null);
const health = ref(new Map<string, ModelHealth>());
const error = ref('');
// Kept apart from `error`: load() clears that one before the reorder's message
// could be drawn, and a refused move is not a failed load.
const reorderError = ref('');
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
    text: (row) => maskProvider(row.provider_name),
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
    // Wide enough for the longest pair it prints, "0.25× / 2.5×": at 80 it
    // was cut to "0.25× / …" the moment a panel narrowed the table.
    width: '112px',
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
  // 0.99 is the line the server draws for "up" (internal/server/server.go), so
  // the colour agrees with the state it was computed from. 0.995 only decides
  // whether the percentage keeps its decimal place near the top of the scale.
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

function onSort(next: SortState | null): void {
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
  reorderError.value = '';
  try {
    await adminApi.reorderModels(next.map((row) => row.id));
  } catch (failure) {
    reorderError.value = failureText(failure);
  }
  // Either way: on success to show the stored order, and on failure to snap
  // back to it rather than leaving the screen claiming a move never written.
  await load();
}

// --- several at once ---------------------------------------------------------------

const bulk = useBulk(() => visible.value, (row) => row.id);

// Skips the ones already in the state asked for, so "enable" over a mixed
// selection does not spend a request on each model that was on.
function bulkEnable(on: boolean): Promise<void> {
  return bulk.run((row) => (row.enabled === on ? Promise.resolve() : adminApi.updateModel(row.id, { enabled: on })), load);
}

function bulkRemove(): Promise<void> {
  return bulk.run((row) => adminApi.deleteModel(row.id), load);
}

// --- the editor -------------------------------------------------------------------

const { open: panelOpen, panel, key: panelKey, show: showPanel, hide: hidePanel, closed: panelClosed } = usePanelSlot();
// The slot holds the editor as its panel: the editor closes like one, and also
// takes the first field's focus when a panel opens.
const editor = (): InstanceType<typeof AdminModelEditor> | null =>
  panel.value as InstanceType<typeof AdminModelEditor> | null;
const existing = ref<AdminModel | null>(null);
const template = ref<AdminModel | null>(null);
const busy = ref(false);
const panelError = ref('');
const form = ref<ModelForm>(blankModelForm());

const creating = computed(() => existing.value === null);

/** Rows ticked in the detected list. Several make one model each, sharing everything else on the form. */
const detected = ref<DetectedModel[]>([]);
const many = computed(() => creating.value && detected.value.length > 1);

const status = computed(() => (existing.value ? health.value.get(existing.value.id) : undefined));

/**
 * The accounts that used a model most over the last month, for its panel.
 *
 * Asked of the one-dimension endpoint rather than the whole usage report:
 * this is one list for one panel, and the report is a dozen aggregates. Only
 * for an operator who may read usage at all; nothing is shown otherwise.
 */
const users = ref<UsageBreakdown[] | null>(null);
let usersRequest = 0;
async function loadUsers(modelID: string): Promise<void> {
  const ticket = ++usersRequest;
  users.value = null;
  if (!canAdmin('usage')) return;
  try {
    const since = Date.now() - 30 * DAY_MS;
    const { rows } = await adminApi.usageBreakdown('user', `model_id=${modelID}&since=${since}&metric=tokens`);
    if (ticket === usersRequest) users.value = rows;
  } catch {
    // A panel that cannot say who uses the model still edits it.
    if (ticket === usersRequest) users.value = [];
  }
}

function open(row: AdminModel | null, from: AdminModel | null = null): void {
  existing.value = row;
  if (row) void loadUsers(row.id);
  else users.value = null;
  template.value = from;
  idCopied.value = false;
  panelError.value = '';
  detected.value = [];

  // Where the fields start. `existing` still decides everything else: the
  // title, the delete button, and whether saving is a POST or a PATCH.
  form.value = formFromModel(row, from, groups.value, providers.value[0]?.id ?? '');

  // Saving leaves this set while the panel slides out.
  busy.value = false;
  showPanel();
  void nextTick(() => editor()?.focusModelID());
}

function duplicate(): void {
  const row = existing.value;
  if (!row) return;
  // The API name is dropped rather than suffixed: it is unique across the
  // instance, and a guessed one would be a second public name nobody asked for.
  open(null, { ...row, api_name: '', display_name: t('copyOfName', { name: row.display_name }) });
}

/**
 * What follows a save or a delete: the panel slides away and the table is
 * redrawn where it stands.
 *
 * This used to be `view.reload()`, which mounts the whole page again — back
 * to the top, back to the first page of rows, the filter's focus gone — so
 * editing the fortieth model cost the way back down to it. The rows are
 * refetched into the page that is already here instead.
 */
function finish(): void {
  hidePanel();
  void load();
}

async function save(): Promise<void> {
  busy.value = true;
  panelError.value = '';

  const trimmedOverride = form.value.requestOverride.trim();
  if (trimmedOverride && !isOverrideObject(trimmedOverride)) {
    panelError.value = t('invalidRequestOverrideJSON');
    busy.value = false;
    return;
  }

  const payload = modelPayload(form.value, trimmedOverride);
  if (creating.value) payload['provider_id'] = form.value.providerID;
  // The avatar has no field in this form, so a copy would silently lose one
  // that had been set through the API.
  if (template.value) payload['avatar'] = template.value.avatar;

  if (many.value) {
    await createMany(payload);
    return;
  }

  try {
    if (creating.value) await adminApi.createModel(payload);
    else await adminApi.updateModel(existing.value!.id, payload);
    finish();
  } catch (failure) {
    busy.value = false;
    // Only a code the interface has a sentence for is translated; any other
    // keeps the server's sentence, which an administrator may need verbatim.
    panelError.value = failureText(failure);
  }
}

/**
 * One model per ticked row, each with the form's settings and its own ids.
 * The API name is left out: it is unique on the instance, so one typed here
 * could belong to only one of them. A row that fails stays ticked and the
 * ones that were made are unticked, so saving again finishes the job rather
 * than making the rest twice.
 */
async function createMany(shared: Record<string, unknown>): Promise<void> {
  const entries = detected.value;
  const results = await Promise.allSettled(entries.map((entry) => adminApi.createModel({
    ...shared,
    model_id: entry.model_id,
    display_name: entry.display_name || entry.model_id,
    api_name: '',
  })));
  const failed = entries.filter((_, index) => results[index]!.status === 'rejected');
  if (!failed.length) {
    finish();
    return;
  }
  detected.value = failed;
  busy.value = false;
  panelError.value = failed.map((entry) => {
    const failure = (results[entries.indexOf(entry)] as PromiseRejectedResult).reason;
    return `${entry.model_id}: ${failureText(failure)}`;
  }).join('; ');
  void load();
}

async function remove(): Promise<void> {
  const row = existing.value;
  if (!row) return;
  await runRemove(() => adminApi.deleteModel(row.id), { busy, error: panelError }, finish);
}

// --- detection ---------------------------------------------------------------------

// A single tick fills the two fields nobody can guess, as picking one always
// did. Only when it is a different model from the one already there, so
// unticking a second row does not undo a display name typed meanwhile.
watch(detected, (entries) => {
  const only = entries.length === 1 ? entries[0]! : null;
  if (!only || only.model_id === form.value.modelID) return;
  form.value.modelID = only.model_id;
  form.value.displayName = only.display_name || only.model_id;
});

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
    // The import writes whatever the file says here, an absent key as empty,
    // so a catalogue taken out and back in without it lost every override.
    request_override: row.request_override ?? '',
    route_to: target ? { provider: target.provider_name, model_id: target.model_id } : null,
    supports_reasoning: row.supports_reasoning,
    supports_images: row.supports_images,
    supports_vision: row.supports_vision,
    supports_streaming: row.supports_streaming,
    supports_system_prompt: row.supports_system_prompt,
    supports_tools: row.supports_tools,
    supports_image_gen: row.supports_image_gen,
    supports_chat_image_gen: row.supports_chat_image_gen,
    emulate_tools: row.emulate_tools,
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
      void load();
    })
    .catch((failure: unknown) => {
      report.value = {
        headline: failureText(failure),
        skipped: [],
      };
    })
    .finally(() => { uploadBusy.value = false; });
}

async function load(): Promise<void> {
  // Only the first load may open a panel from the address's hash: this runs
  // again after every save, and a hash still in the address would open the
  // panel that was just closed.
  const first = !loaded.value;
  error.value = '';
  try {
    const [modelsResult, providersResult, groupsResult, metaResult] = await Promise.all([
      adminApi.models(),
      adminApi.providerOptions(),
      adminApi.groupOptions(),
      adminApi.meta(),
    ]);
    models.value = modelsResult.models;
    providers.value = providersResult.providers;
    groups.value = groupsResult.groups;
    meta.value = metaResult;
  } catch (failure) {
    error.value = failureText(failure);
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
  if (first) checkDrawerTarget();
}

const DRAWER_HASHES = new Set(['#addModel', '#secCapabilities', '#secWeights', '#secGroupAccess', '#secRouting', '#secThinking', '#secRequestOverride']);

function checkDrawerTarget(): void {
  if (DRAWER_HASHES.has(route.hash) && !panelOpen.value && providers.value.length > 0) {
    open(null);
  }
}

watch(() => route.hash, () => {
  if (loaded.value) {
    checkDrawerTarget();
  }
});

onMounted(load);
onBeforeUnmount(() => window.clearTimeout(idCopiedTimer));
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
    <div id="modelImportExport" class="oa-button-row">
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
    </div>
    <button
      id="addModel"
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
    <p v-if="reorderError" role="alert" class="oa-drawer-flash visible oa-control-flash">{{ reorderError }}</p>
    <div id="modelsList" class="oa-filters" :hidden="models.length === 0">
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
      multi
      v-model:selected="bulk.selected.value"
      :row-key="(row) => row.id"
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
          <OaBadge v-if="row.supports_chat_image_gen" tone="muted">{{ t('canChatImageGen') }}</OaBadge>
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
    <OaBulkBar
      :count="bulk.selected.value.length"
      :total="visible.length"
      :busy="bulk.busy.value"
      :error="bulk.error.value"
      deletable
      @all="bulk.selectAll"
      @clear="bulk.clear"
      @delete="bulkRemove"
    >
      <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkEnable(true)">{{ t('bulkEnable') }}</button>
      <button type="button" class="oa-btn small" :disabled="bulk.busy.value" @click="bulkEnable(false)">{{ t('bulkDisable') }}</button>
    </OaBulkBar>
  </template>

  <AdminModelEditor
    v-if="panelOpen && meta"
    ref="panel"
    :key="panelKey"
    v-model:form="form"
    v-model:detected="detected"
    :existing="existing"
    :many="many"
    :providers="providers"
    :groups="groups"
    :models="models"
    :meta="meta"
    :status="status"
    :users="users"
    :busy="busy"
    :error="panelError"
    :id-copied="idCopied"
    @close="panelClosed"
    @confirm="save"
    @destructive="remove"
    @duplicate="duplicate"
    @copy-id="copyID"
  />

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
        <code v-for="(line, index) in report.skipped" :key="index" class="oa-code-line">{{ line }}</code>
      </div>
    </template>
  </OaPanel>
</template>
