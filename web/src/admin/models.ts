// Models: what a provider is asked for, what it can do, and what it costs.
//
// Capabilities are declared rather than discovered because no endpoint will
// tell you. Whether a model reads images or reasons before answering is the
// administrator's answer, and getting it wrong is visible immediately — the
// composer stops offering attachments, or the thinking toggle disappears.

import { ApiError } from '../api/client';
import { pickJSONFile, saveAsFile } from '../api/backup';
import { t } from '../i18n';
import { ICONS, button, clear, el, iconButton } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, section, selectField, switchField, textArea, textField, tierList } from '../ui/form';
import { badge, badges, compactNumber, renderTable, stacked, type SortState } from '../ui/table';
import { adminApi, type AdminModel, type Group, type Meta, type Provider, type ReasoningStyle, type ReasoningTier } from './api';
import { failure, filterSelect, type AdminView } from './admin-page';
import { reasoningLabel } from './providers';

/**
 * What the table is narrowed to, kept out here so that editing a model and
 * coming back does not silently reset the filter the administrator was
 * reading through. The users screen keeps its own the same way.
 */
interface ModelFilters {
  q: string;
  provider: string;
  state: '' | 'enabled' | 'disabled' | 'hidden' | 'routed';
}

const filters: ModelFilters = { q: '', provider: '', state: '' };

/**
 * Null is the order the server sent, which is sort_order then name — the
 * order an administrator arranged by hand. That is the right thing to come
 * back to, so no column is sorted until one is clicked.
 */
let order: SortState | null = null;

/**
 * One dropdown rather than one per axis.
 *
 * Enabled/disabled, hidden and routed are three independent properties, so a
 * strict reading wants three controls. But the question actually being asked
 * of this table is "show me the X ones", one X at a time, and three dropdowns
 * to answer it is two more than the question needs.
 */
function matches(row: AdminModel): boolean {
  if (filters.provider && row.provider_id !== filters.provider) return false;
  switch (filters.state) {
    case 'enabled': if (!row.enabled) return false; break;
    case 'disabled': if (row.enabled) return false; break;
    case 'hidden': if (!row.hidden) return false; break;
    case 'routed': if (!row.route_to_id) return false; break;
  }
  if (filters.q) {
    // The upstream id as well as the name: an administrator hunting for a
    // model usually has the id in hand, and it is the half the reader never
    // sees.
    const haystack = `${row.display_name} ${row.model_id} ${row.provider_name}`.toLowerCase();
    if (!haystack.includes(filters.q.toLowerCase())) return false;
  }
  return true;
}

export async function renderModels(view: AdminView): Promise<void> {
  view.setTitle(t('modelsTitle'), t('modelsSubtitle'));

  let models: AdminModel[];
  let providers: Provider[];
  let groups: Group[];
  let meta: Meta;
  try {
    [{ models }, { providers }, { groups }, meta] = await Promise.all([
      adminApi.models(),
      adminApi.providers(),
      adminApi.groups(),
      adminApi.meta(),
    ]);
  } catch (error) {
    failure(view, error);
    return;
  }

  const nameOf = (modelID: string) =>
    models.find((entry) => entry.id === modelID)?.display_name ?? modelID;

  clear(view.actions);
  const add = button('oa-btn primary', t('addModel'), () => editModel(view, providers, models, groups, meta, null));
  add.disabled = providers.length === 0;
  if (!providers.length) add.title = t('addProviderFirst');

  const download = button('oa-btn', t('exportModels'), () => {
    const stamp = new Date().toISOString().slice(0, 10);
    saveAsFile(`obsidian-arc-models-${stamp}.json`,
      JSON.stringify({ version: 1, models: models.map((row) => portable(row, models, groups)) }, null, 2));
  });
  download.disabled = models.length === 0;

  const upload = button('oa-btn', t('importModels'), () => {
    void pickJSONFile(4 * 1024 * 1024)
      .then((file) => {
        if (file === null) return null;
        const listed = (file as { models?: unknown }).models;
        if (!Array.isArray(listed)) throw new ApiError(0, 'malformed', t('importModelsMalformed'));
        upload.disabled = true;
        return adminApi.importModels(listed);
      })
      .then((result) => {
        if (!result) return;
        // Every refusal, not a count of them: "3 skipped" sends an operator
        // back to the file with nothing to look for.
        const note = t('importModelsDone', { created: result.created, updated: result.updated });
        view.reload();
        if (result.skipped.length) window.setTimeout(() => report(view, note, result.skipped), 60);
        else window.setTimeout(() => report(view, note, []), 60);
      })
      .catch((error: unknown) => report(view, error instanceof ApiError ? error.message : String(error), []))
      .finally(() => { upload.disabled = false; });
  });

  view.actions.appendChild(download);
  view.actions.appendChild(upload);
  view.actions.appendChild(add);

  clear(view.body);

  // Filtered here rather than by the server: this screen already holds every
  // model in memory to resolve route targets and to name them, so a query
  // would be a round trip for a list that is already on the page.
  const bar = el('div', 'oa-filters');
  const search = el('input');
  search.type = 'search';
  search.placeholder = t('searchModels');
  search.value = filters.q;

  const providerSelect = filterSelect([
    { value: '', label: t('anyProvider') },
    ...providers.map((provider) => ({ value: provider.id, label: provider.name })),
  ], filters.provider, () => applyFilters());

  const stateSelect = filterSelect([
    { value: '', label: t('anyStatus') },
    { value: 'enabled', label: t('enabled') },
    { value: 'disabled', label: t('disabled') },
    { value: 'hidden', label: t('filterHidden') },
    { value: 'routed', label: t('filterRouted') },
  ], filters.state, () => applyFilters());

  bar.appendChild(search);
  bar.appendChild(providerSelect.element);
  bar.appendChild(stateSelect.element);
  bar.appendChild(el('span', 'oa-filter-note', t('dragToOrder')));
  bar.hidden = models.length === 0;
  view.body.appendChild(bar);

  const results = el('div');
  view.body.appendChild(results);

  const paint = (): void => {
    clear(results);
    results.appendChild(table(view, providers, models, groups, meta, nameOf, paint));
  };

  search.addEventListener('input', () => {
    filters.q = search.value.trim();
    paint();
  });
  function applyFilters(): void {
    filters.provider = providerSelect.value();
    filters.state = stateSelect.value() as ModelFilters['state'];
    paint();
  }

  paint();
}

function table(
  view: AdminView,
  providers: Provider[],
  models: AdminModel[],
  groups: Group[],
  meta: Meta,
  nameOf: (modelID: string) => string,
  repaint: () => void,
): HTMLElement {
  const visible = models.filter(matches);
  return renderTable({
    sort: order,
    onReorder: (rows) => void applyOrder(view, models, rows),
    onSort: (next) => {
      order = next;
      repaint();
    },
    columns: [
      {
        header: t('colModel'),
        // The route belongs on the name, not in a column of its own: it
        // is the answer to "what does this row actually do", and it is
        // blank on nearly every row.
        cell: (row) => stacked(
          row.display_name,
          row.route_to_id ? t('routedTo', { name: nameOf(row.route_to_id) }) : row.model_id,
        ),
        sort: (row) => row.display_name,
      },
      {
        header: t('colProvider'),
        cell: (row) => row.provider_name,
        secondary: true,
        width: '130px',
        // The provider first, then the name, so the models of one provider
        // arrive together and in a readable order rather than in whatever
        // order the rows happened to be in.
        sort: (row) => `${row.provider_name}\u0000${row.display_name}`,
      },
      { header: t('colCan'), cell: (row) => capabilityBadges(row), width: '140px' },
      {
        header: t('colWeights'),
        cell: (row) => weightLabel(row),
        numeric: true,
        secondary: true,
        width: '80px',
        // What the cell prints is a pair; what anyone sorts by is the
        // output rate, which is the half that dominates a bill.
        sort: (row) => row.output_token_weight,
      },
      {
        header: t('colState'),
        cell: (row) => badges(
          row.enabled ? badge(t('enabled'), 'muted') : badge(t('disabled'), 'danger'),
          row.hidden ? badge(t('hiddenBadge'), 'muted') : null,
        ),
        width: '110px',
        // Ascending walks from most available to least: on, on but hidden,
        // off. That is the order the column is scanned in.
        sort: (row) => (row.enabled ? 0 : 2) + (row.hidden ? 1 : 0),
      },
    ],
    rows: visible,
    // Three different empty tables: nothing configured, nothing to configure
    // it with, and a filter that happens to exclude everything. Saying "no
    // models yet" to the third is how someone concludes their work is gone.
    empty: !providers.length
      ? t('addProviderFirst')
      : models.length ? t('noModelsMatch') : t('noModels'),
    muted: (row) => !row.enabled,
    onSelect: (row) => editModel(view, providers, models, groups, meta, row),
  });
}

function capabilityBadges(model: AdminModel): HTMLElement {
  return badges(
    model.supports_reasoning ? badge(t('canThinks'), 'muted') : null,
    model.supports_vision ? badge(t('canSees'), 'muted') : null,
    model.supports_images && !model.supports_vision ? badge(t('canImages'), 'muted') : null,
    model.supports_streaming ? null : badge(t('canNoStream'), 'muted'),
    model.route_to_id ? badge(t('routedBadge'), 'muted') : null,
  );
}

function weightLabel(model: AdminModel): string {
  const { input_token_weight: input, output_token_weight: output } = model;
  if (input === 1 && output === 1 && model.request_weight === 0) return '1×';
  return `${compactNumber(input)}× / ${compactNumber(output)}×`;
}

function editModel(
  view: AdminView,
  providers: Provider[],
  models: AdminModel[],
  groups: Group[],
  meta: Meta,
  existing: AdminModel | null,
  /**
   * Values to start from when creating. A copy of a model is the create form
   * with somebody else's answers already in it: the operator changes what
   * makes this one different — which is at least the provider or the model
   * id, because a provider cannot list one upstream model twice — and saves.
   */
  template: AdminModel | null = null,
): void {
  const creating = existing === null;
  // Where the fields start. `existing` still decides everything else: the
  // title, the delete button, and whether saving is a POST or a PATCH.
  const source = existing ?? template;

  const providerID = selectField({
    label: t('colProvider'),
    value: source?.provider_id ?? providers[0]?.id ?? '',
    options: providers.map((provider) => ({ value: provider.id, label: provider.name })),
  });

  const modelID = textField({
    label: t('modelIDLabel'),
    value: source?.model_id ?? '',
    placeholder: 'anthropic/claude-opus-5',
    hint: t('modelIDHint'),
    monospace: true,
  });

  const apiName = textField({
    label: t('apiNameLabel'),
    value: existing?.api_name ?? '',
    placeholder: source?.model_id || 'gpt-5.6-sol',
    hint: t('apiNameHint'),
    monospace: true,
  });

  const displayName = textField({
    label: t('displayName'),
    value: source?.display_name ?? '',
    placeholder: 'Claude Opus 5',
    hint: t('displayNameHint'),
    maxLength: 80,
  });

  const description = textArea({
    label: t('description'),
    value: source?.description ?? '',
    placeholder: t('modelDescriptionPlaceholder'),
    rows: 2,
    hint: t('modelDescriptionHint'),
  });

  const enabled = switchField({ label: t('enabled'), value: source?.enabled ?? true });
  const hidden = switchField({
    label: t('modelHidden'),
    value: source?.hidden ?? false,
    hint: t('modelHiddenHint'),
  });
  const sortOrder = numberField({ label: t('sortOrder'), value: source?.sort_order ?? 0 });

  const initialGroupGrants: Record<string, 'use' | 'view'> = {};
  if (source?.group_grants) {
    for (const grant of source.group_grants) {
      if (grant.access === 'use' || grant.access === 'view') {
        initialGroupGrants[grant.group_id] = grant.access;
      }
    }
  }

  const groupAccess = tierList({
    label: t('groupsTitle'),
    hint: t('groupAccessHint'),
    items: groups.map((group) => ({
      value: group.id,
      label: group.name,
      sub: group.description || undefined,
    })),
    selected: initialGroupGrants,
    emptyText: t('noGroups'),
  });

  const reasoning = switchField({
    label: t('capReasoning'),
    value: source?.supports_reasoning ?? false,
    hint: t('capReasoningHint'),
  });
  const images = switchField({
    label: t('capImages'),
    value: source?.supports_images ?? false,
    hint: t('capImagesHint'),
  });
  const vision = switchField({
    label: t('capVision'),
    value: source?.supports_vision ?? false,
  });
  const streaming = switchField({ label: t('capStreams'), value: source?.supports_streaming ?? true });
  const systemPrompt = switchField({ label: t('capSystemPrompt'), value: source?.supports_system_prompt ?? true });
  const tools = switchField({ label: t('capTools'), value: source?.supports_tools ?? false });

  const contextWindow = numberField({
    label: t('contextWindow'),
    value: source?.context_window ?? null,
    placeholder: '200000',
    min: 0,
  });
  const maxOutput = numberField({
    label: t('maxOutputTokens'),
    value: source?.max_output_tokens ?? null,
    placeholder: '8192',
    min: 0,
  });

  // Every other model is a candidate except this one and any that is
  // already routed: resolution is a single hop, so a chain would not do
  // what the second link says. The server refuses both as well.
  const routeTo = selectField({
    label: t('routeTo'),
    value: source?.route_to_id ?? '',
    hint: t('routeToHint'),
    options: [
      { value: '', label: t('routeNone') },
      ...models
        .filter((entry) => entry.id !== existing?.id && !entry.route_to_id)
        .map((entry) => ({
          value: entry.id,
          label: `${entry.display_name} \u2014 ${entry.provider_name}`,
        })),
    ],
  });

  const tiers = reasoningTiersField(source?.reasoning_tiers ?? []);

  const reasoningStyle = selectField<ReasoningStyle | ''>({
    label: t('reasoningStyleModel'),
    value: source?.reasoning_style ?? '',
    hint: t('reasoningStyleModelHint'),
    options: [
      { value: '', label: t('styleInherit') },
      ...meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) })),
    ],
  });

  const requestWeight = numberField({
    label: t('perRequest'),
    value: source?.request_weight ?? 0,
    step: 0.1,
    min: 0,
    hint: t('perRequestHint'),
  });
  const inputWeight = numberField({
    label: t('per1kInput'),
    value: source?.input_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });
  const outputWeight = numberField({
    label: t('per1kOutput'),
    value: source?.output_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });
  const reasoningWeight = numberField({
    label: t('per1kReasoning'),
    value: source?.reasoning_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });

  const panel = openPanel({
    host: view.host,
    title: creating ? t('addModel') : existing.display_name,
    confirmLabel: creating ? t('add') : t('save'),
    ...(existing
      ? {
          actions: [
            iconButton('oa-icon-btn', ICONS.copy, t('duplicate'), () => {
              panel.close();
              // The API name is dropped rather than suffixed: it is unique
              // across the instance, and a guessed one would be a second
              // public name nobody asked for.
              editModel(view, providers, models, groups, meta, null, {
                ...existing,
                api_name: '',
                display_name: t('copyOfName', { name: existing.display_name }),
              });
            }),
          ],
        }
      : {}),
    ...(existing
      ? {
          destructive: {
            label: t('deleteLabel'),
            confirm: t('confirmDeleteModel', { name: existing.display_name }),
            onSelect: (handle) => removeModel(view, existing, handle),
          },
        }
      : {}),
    build: (body) => {
      if (creating) {
        body.appendChild(providerID.element);
        // Detect belongs here as well as on the provider screen: this is the
        // form where an upstream id has to be typed exactly, so it is where
        // being handed the list saves the typing. The provider editor still
        // has the bulk add, which is a different job.
        const host = el('div', 'oa-field');
        const detect = button('oa-btn', t('detect'), () => {
          void pickDetected(providerID.value(), detect, host, modelID, displayName);
        });
        host.appendChild(detect);
        host.appendChild(el('span', 'oa-field-hint', t('detectPickHint')));
        body.appendChild(host);
      } else {
        body.appendChild(readOnly(t('colProvider'), existing.provider_name));
      }
      body.appendChild(modelID.element);
      body.appendChild(apiName.element);
      body.appendChild(displayName.element);
      body.appendChild(description.element);
      body.appendChild(enabled.element);
      body.appendChild(hidden.element);
      body.appendChild(sortOrder.element);

      body.appendChild(section(t('secGroupAccess')));
      body.appendChild(groupAccess.element);

      body.appendChild(section(t('secCapabilities'), t('capabilitiesHint')));
      body.appendChild(reasoning.element);
      body.appendChild(images.element);
      body.appendChild(vision.element);
      body.appendChild(streaming.element);
      body.appendChild(systemPrompt.element);
      body.appendChild(tools.element);
      body.appendChild(contextWindow.element);
      body.appendChild(maxOutput.element);

      body.appendChild(section(t('secRouting')));
      body.appendChild(routeTo.element);

      // The style and the tiers are one subject — how this model is asked to
      // think — and they used to sit under Routing, which is a different
      // one.
      body.appendChild(section(t('secThinking')));
      body.appendChild(reasoningStyle.element);
      body.appendChild(tiers.element);

      body.appendChild(section(t('secWeights'), t('weightsHint')));
      body.appendChild(requestWeight.element);
      body.appendChild(inputWeight.element);
      body.appendChild(outputWeight.element);
      body.appendChild(reasoningWeight.element);
    },
    onConfirm: async (handle) => {
      const grantsRecord = groupAccess.value();
      const groupGrants = Object.entries(grantsRecord).map(([group_id, access]) => ({
        group_id,
        access,
      }));

      const payload: Record<string, unknown> = {
        route_to_id: routeTo.value(),
        reasoning_style: reasoningStyle.value(),
        reasoning_tiers: tiers.value(),
        model_id: modelID.value(),
        api_name: apiName.value(),
        display_name: displayName.value(),
        description: description.value(),
        enabled: enabled.value(),
        hidden: hidden.value(),
        sort_order: sortOrder.value() ?? 0,
        supports_reasoning: reasoning.value(),
        supports_images: images.value(),
        supports_vision: vision.value(),
        supports_streaming: streaming.value(),
        supports_system_prompt: systemPrompt.value(),
        supports_tools: tools.value(),
        context_window: contextWindow.value() ?? 0,
        max_output_tokens: maxOutput.value() ?? 0,
        request_weight: requestWeight.value() ?? 0,
        input_token_weight: inputWeight.value() ?? 1,
        output_token_weight: outputWeight.value() ?? 1,
        reasoning_token_weight: reasoningWeight.value() ?? 1,
        group_grants: groupGrants,
      };
      if (creating) payload['provider_id'] = providerID.value();
      // The avatar has no field in this form, so a copy would silently lose
      // one that had been set through the API.
      if (template) payload['avatar'] = template.avatar;

      handle.setBusy(true);
      try {
        if (creating) await adminApi.createModel(payload);
        else await adminApi.updateModel(existing.id, payload);
        handle.close();
        view.reload();
      } catch (error) {
        handle.setBusy(false);
        handle.setError(error instanceof ApiError ? error.message : String(error));
      }
    },
  });

  modelID.focus({ preventScroll: true });
  void panel;
}

/**
 * Asks the provider what it serves and lets one row fill the form.
 *
 * The provider editor's version of this adds every ticked row at once. This
 * one is a picker: the panel it opens into is already creating exactly one
 * model, and the two fields it fills are the two nobody can guess.
 */
async function pickDetected(
  providerID: string,
  trigger: HTMLButtonElement,
  host: HTMLElement,
  modelID: { set(value: string): void },
  displayName: { set(value: string): void },
): Promise<void> {
  if (!providerID) return;
  trigger.disabled = true;
  trigger.textContent = t('detecting');
  host.querySelector('.oa-detect-panel')?.remove();

  const panel = el('div', 'oa-detect-panel');
  host.appendChild(panel);

  try {
    const { models } = await adminApi.detect(providerID);
    panel.appendChild(el('p', 'oa-detect-status', t('nModelsFound', { count: models.length })));

    const list = el('div', 'oa-detect-list');
    for (const entry of models) {
      const row = el('button', 'oa-detect-row');
      row.type = 'button';
      row.disabled = entry.configured;
      row.appendChild(el('span', null,
        entry.display_name ? `${entry.display_name} — ${entry.model_id}` : entry.model_id));
      if (entry.configured) row.appendChild(el('span', 'oa-detect-known', t('alreadyAdded')));
      row.addEventListener('click', () => {
        modelID.set(entry.model_id);
        displayName.set(entry.display_name || entry.model_id);
        panel.remove();
      });
      list.appendChild(row);
    }
    panel.appendChild(list);
  } catch (error) {
    panel.appendChild(el('p', 'oa-detect-status', error instanceof ApiError ? error.message : String(error)));
  } finally {
    trigger.disabled = false;
    trigger.textContent = t('detect');
  }
}

/**
 * The rows that say how many amounts of thinking a model offers.
 *
 * Not `tierList` from ui/form.ts, which grants a group access to a model:
 * the two words only collide in English.
 *
 * An empty list is the built-in three, so deleting the last row is how an
 * administrator goes back to them rather than a state to be guarded against.
 */
function reasoningTiersField(initial: ReasoningTier[]): {
  element: HTMLElement;
  value(): ReasoningTier[];
} {
  const rows: Array<{ node: HTMLElement; read(): ReasoningTier }> = [];
  const list = el('div', 'oa-thinking-tiers');

  const element = el('div', 'oa-field');
  element.appendChild(el('span', 'oa-field-label', t('reasoningTiers')));
  element.appendChild(list);
  const add = button('oa-btn', t('addTier'), () => {
    addRow({ id: '', name: '', budget: 0 });
  });
  element.appendChild(add);
  element.appendChild(el('span', 'oa-field-hint', t('reasoningTiersHint')));

  function cell(label: string, value: string, className: string): HTMLInputElement {
    const node = el('input', className);
    node.type = 'text';
    node.spellcheck = false;
    node.value = value;
    node.placeholder = label;
    // The column headers are the placeholders, which disappear the moment a
    // row is filled in, so the name has to survive somewhere a screen reader
    // can still reach.
    node.setAttribute('aria-label', label);
    return node;
  }

  function addRow(tier: ReasoningTier): void {
    const node = el('div', 'oa-thinking-tier');
    const name = cell(t('tierName'), tier.name, 'oa-thinking-name');
    const value = cell(t('tierValue'), tier.id, 'oa-thinking-value');
    const budget = cell(t('tierBudget'), tier.budget ? String(tier.budget) : '', 'oa-thinking-budget');
    budget.inputMode = 'numeric';

    const entry = {
      node,
      read: (): ReasoningTier => ({
        id: value.value.trim(),
        name: name.value.trim(),
        budget: Math.max(0, Math.trunc(Number(budget.value.trim()) || 0)),
      }),
    };
    const remove = iconButton('oa-icon-btn', ICONS.close, t('removeTier'), () => {
      const at = rows.indexOf(entry);
      if (at !== -1) rows.splice(at, 1);
      node.remove();
    }, 14);

    node.appendChild(name);
    node.appendChild(value);
    node.appendChild(budget);
    node.appendChild(remove);
    list.appendChild(node);
    rows.push(entry);
  }

  for (const tier of initial) addRow(tier);

  return {
    element,
    // Half-filled rows are dropped here as well as on the server: a tier with
    // no name would be a blank stop on the slider, and one with no value
    // could never be told apart from its neighbour.
    value: () => rows.map((row) => row.read()).filter((tier) => tier.id !== '' && tier.name !== ''),
  };
}

/**
 * Writes back an order a row was dragged into.
 *
 * `reordered` is only what was on screen, which may be a filtered subset. The
 * rows that were filtered out keep the positions they had: the visible ones
 * are dealt back into the slots they occupied, in their new sequence. Moving
 * a row you can see must not move a row you cannot.
 */
async function applyOrder(view: AdminView, all: AdminModel[], reordered: AdminModel[]): Promise<void> {
  const moved = new Set(reordered.map((row) => row.id));
  const queue = [...reordered];
  const next = all.map((row) => (moved.has(row.id) ? queue.shift()! : row));

  try {
    await adminApi.reorderModels(next.map((row) => row.id));
  } catch (error) {
    window.alert(error instanceof ApiError ? error.message : String(error));
  }
  // Either way: on success to show the stored order, and on failure to snap
  // back to it rather than leaving the screen claiming a move that was never
  // written.
  view.reload();
}

async function removeModel(view: AdminView, model: AdminModel, panel: PanelHandle): Promise<void> {
  panel.setBusy(true);
  try {
    await adminApi.deleteModel(model.id);
    panel.close();
    view.reload();
  } catch (error) {
    panel.setBusy(false);
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}

/** A field that shows a value the form cannot change. */
export function readOnly(label: string, value: string): HTMLElement {
  const wrap = el('div', 'oa-field');
  wrap.appendChild(el('span', 'oa-field-label', label));
  wrap.appendChild(el('span', 'oa-field-hint', value));
  return wrap;
}

/**
 * One model as a file can carry it: names where the database has ids, because
 * a ULID means nothing on the instance this is being taken to.
 */
function portable(row: AdminModel, all: AdminModel[], groups: Group[]): Record<string, unknown> {
  const target = row.route_to_id ? all.find((entry) => entry.id === row.route_to_id) : undefined;
  const groupName = (id: string) => groups.find((group) => group.id === id)?.name ?? '';

  return {
    provider: row.provider_name,
    model_id: row.model_id,
    api_name: row.api_name,
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
    context_window: row.context_window,
    max_output_tokens: row.max_output_tokens,
    request_weight: row.request_weight,
    input_token_weight: row.input_token_weight,
    output_token_weight: row.output_token_weight,
    reasoning_token_weight: row.reasoning_token_weight,
    groups: (row.group_grants ?? [])
      .map((grant) => ({ group: groupName(grant.group_id), access: grant.access }))
      .filter((grant) => grant.group),
  };
}

/** What an import did, and every entry it would not take. */
function report(view: AdminView, headline: string, skipped: string[]): void {
  openPanel({
    host: view.host,
    title: headline,
    footer: false,
    build: (body) => {
      if (!skipped.length) {
        body.appendChild(el('p', 'oa-field-hint', t('importModelsClean')));
        return;
      }
      body.appendChild(el('p', 'oa-field-hint', t('importModelsSkipped', { count: skipped.length })));
      const list = el('div', 'oa-code-list');
      for (const line of skipped) list.appendChild(el('code', 'oa-code-line', line));
      body.appendChild(list);
    },
  });
}
