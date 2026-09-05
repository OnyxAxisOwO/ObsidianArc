// Models: what a provider is asked for, what it can do, and what it costs.
//
// Capabilities are declared rather than discovered because no endpoint will
// tell you. Whether a model reads images or reasons before answering is the
// administrator's answer, and getting it wrong is visible immediately — the
// composer stops offering attachments, or the thinking toggle disappears.

import { ApiError } from '../api/client';
import { t } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, section, selectField, switchField, textArea, textField, tierList } from '../ui/form';
import { badge, badges, compactNumber, renderTable, stacked } from '../ui/table';
import { adminApi, type AdminModel, type Group, type Meta, type Provider, type ReasoningStyle } from './api';
import { failure, type AdminView } from './admin-page';
import { reasoningLabel } from './providers';

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
  view.actions.appendChild(add);

  clear(view.body);
  view.body.appendChild(renderTable({
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
      },
      { header: t('colProvider'), cell: (row) => row.provider_name, secondary: true, width: '130px' },
      { header: t('colCan'), cell: (row) => capabilityBadges(row), width: '140px' },
      { header: t('colWeights'), cell: (row) => weightLabel(row), numeric: true, secondary: true, width: '80px' },
      {
        header: t('colState'),
        cell: (row) => badges(
          row.enabled ? badge(t('enabled'), 'muted') : badge(t('disabled'), 'danger'),
          row.hidden ? badge(t('hiddenBadge'), 'muted') : null,
        ),
        width: '110px',
      },
    ],
    rows: models,
    empty: providers.length ? t('noModels') : t('addProviderFirst'),
    muted: (row) => !row.enabled,
    onSelect: (row) => editModel(view, providers, models, groups, meta, row),
  }));
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
): void {
  const creating = existing === null;

  const providerID = selectField({
    label: t('colProvider'),
    value: existing?.provider_id ?? providers[0]?.id ?? '',
    options: providers.map((provider) => ({ value: provider.id, label: provider.name })),
  });

  const modelID = textField({
    label: t('modelIDLabel'),
    value: existing?.model_id ?? '',
    placeholder: 'anthropic/claude-opus-5',
    hint: t('modelIDHint'),
    monospace: true,
  });

  const displayName = textField({
    label: t('displayName'),
    value: existing?.display_name ?? '',
    placeholder: 'Claude Opus 5',
    hint: t('displayNameHint'),
    maxLength: 80,
  });

  const description = textArea({
    label: t('description'),
    value: existing?.description ?? '',
    placeholder: t('modelDescriptionPlaceholder'),
    rows: 2,
    hint: t('modelDescriptionHint'),
  });

  const enabled = switchField({ label: t('enabled'), value: existing?.enabled ?? true });
  const hidden = switchField({
    label: t('modelHidden'),
    value: existing?.hidden ?? false,
    hint: t('modelHiddenHint'),
  });
  const sortOrder = numberField({ label: t('sortOrder'), value: existing?.sort_order ?? 0 });

  const initialGroupGrants: Record<string, 'use' | 'view'> = {};
  if (existing?.group_grants) {
    for (const grant of existing.group_grants) {
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
    value: existing?.supports_reasoning ?? false,
    hint: t('capReasoningHint'),
  });
  const images = switchField({
    label: t('capImages'),
    value: existing?.supports_images ?? false,
    hint: t('capImagesHint'),
  });
  const vision = switchField({
    label: t('capVision'),
    value: existing?.supports_vision ?? false,
  });
  const streaming = switchField({ label: t('capStreams'), value: existing?.supports_streaming ?? true });
  const systemPrompt = switchField({ label: t('capSystemPrompt'), value: existing?.supports_system_prompt ?? true });
  const tools = switchField({ label: t('capTools'), value: existing?.supports_tools ?? false });

  const contextWindow = numberField({
    label: t('contextWindow'),
    value: existing?.context_window ?? null,
    placeholder: '200000',
    min: 0,
  });
  const maxOutput = numberField({
    label: t('maxOutputTokens'),
    value: existing?.max_output_tokens ?? null,
    placeholder: '8192',
    min: 0,
  });

  // Every other model is a candidate except this one and any that is
  // already routed: resolution is a single hop, so a chain would not do
  // what the second link says. The server refuses both as well.
  const routeTo = selectField({
    label: t('routeTo'),
    value: existing?.route_to_id ?? '',
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

  const reasoningStyle = selectField<ReasoningStyle | ''>({
    label: t('reasoningStyleModel'),
    value: existing?.reasoning_style ?? '',
    hint: t('reasoningStyleModelHint'),
    options: [
      { value: '', label: t('styleInherit') },
      ...meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) })),
    ],
  });

  const requestWeight = numberField({
    label: t('perRequest'),
    value: existing?.request_weight ?? 0,
    step: 0.1,
    min: 0,
    hint: t('perRequestHint'),
  });
  const inputWeight = numberField({
    label: t('per1kInput'),
    value: existing?.input_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });
  const outputWeight = numberField({
    label: t('per1kOutput'),
    value: existing?.output_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });
  const reasoningWeight = numberField({
    label: t('per1kReasoning'),
    value: existing?.reasoning_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });

  const panel = openPanel({
    host: view.host,
    title: creating ? t('addModel') : existing.display_name,
    confirmLabel: creating ? t('add') : t('save'),
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
      if (creating) body.appendChild(providerID.element);
      else body.appendChild(readOnly(t('colProvider'), existing.provider_name));
      body.appendChild(modelID.element);
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
      body.appendChild(reasoningStyle.element);

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
        model_id: modelID.value(),
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
