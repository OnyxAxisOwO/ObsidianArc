// Models: what a provider is asked for, what it can do, and what it costs.
//
// Capabilities are declared rather than discovered because no endpoint will
// tell you. Whether a model reads images or reasons before answering is the
// administrator's answer, and getting it wrong is visible immediately — the
// composer stops offering attachments, or the thinking toggle disappears.

import { ApiError } from '../api/client';
import { button, clear, el } from '../ui/dom';
import { openDrawer, type DrawerHandle } from '../ui/drawer';
import { numberField, section, selectField, switchField, textArea, textField } from '../ui/form';
import { badge, badges, compactNumber, renderTable, stacked } from '../ui/table';
import { adminApi, type AdminModel, type Provider } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderModels(view: AdminView): Promise<void> {
  view.setTitle('Models', 'What users can pick from, and what each one costs against their allowance.');

  let models: AdminModel[];
  let providers: Provider[];
  try {
    [{ models }, { providers }] = await Promise.all([adminApi.models(), adminApi.providers()]);
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  const add = button('oa-btn primary', 'Add a model', () => editModel(view, providers, null));
  add.disabled = providers.length === 0;
  if (!providers.length) add.title = 'Add a provider first.';
  view.actions.appendChild(add);

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      { header: 'Model', cell: (row) => stacked(row.display_name, row.model_id) },
      { header: 'Provider', cell: (row) => row.provider_name, secondary: true },
      { header: 'Can', cell: (row) => capabilityBadges(row) },
      { header: 'Weights', cell: (row) => weightLabel(row), numeric: true, secondary: true },
      { header: 'State', cell: (row) => (row.enabled ? badge('enabled', 'muted') : badge('disabled', 'danger')) },
    ],
    rows: models,
    empty: providers.length ? 'No models yet. Add one, or detect them from a provider.' : 'Add a provider first.',
    muted: (row) => !row.enabled,
    onSelect: (row) => editModel(view, providers, row),
  }));
}

function capabilityBadges(model: AdminModel): HTMLElement {
  return badges(
    model.supports_reasoning ? badge('thinks', 'muted') : null,
    model.supports_vision ? badge('sees', 'muted') : null,
    model.supports_images && !model.supports_vision ? badge('images', 'muted') : null,
    model.supports_streaming ? null : badge('no stream', 'muted'),
  );
}

function weightLabel(model: AdminModel): string {
  const { input_token_weight: input, output_token_weight: output } = model;
  if (input === 1 && output === 1 && model.request_weight === 0) return '1×';
  return `${compactNumber(input)}× / ${compactNumber(output)}×`;
}

function editModel(view: AdminView, providers: Provider[], existing: AdminModel | null): void {
  const creating = existing === null;

  const providerID = selectField({
    label: 'Provider',
    value: existing?.provider_id ?? providers[0]?.id ?? '',
    options: providers.map((provider) => ({ value: provider.id, label: provider.name })),
  });

  const modelID = textField({
    label: 'Model id',
    value: existing?.model_id ?? '',
    placeholder: 'anthropic/claude-opus-5',
    hint: 'Exactly what the provider calls it.',
    monospace: true,
  });

  const displayName = textField({
    label: 'Display name',
    value: existing?.display_name ?? '',
    placeholder: 'Claude Opus 5',
    hint: 'What users see in the picker.',
    maxLength: 80,
  });

  const description = textArea({
    label: 'Description',
    value: existing?.description ?? '',
    placeholder: 'Best for long, careful answers.',
    rows: 2,
    hint: 'One line, shown under the name in the model menu.',
  });

  const enabled = switchField({ label: 'Enabled', value: existing?.enabled ?? true });
  const sortOrder = numberField({ label: 'Sort order', value: existing?.sort_order ?? 0 });

  const reasoning = switchField({
    label: 'Reasons before answering',
    value: existing?.supports_reasoning ?? false,
    hint: 'Turns on the extended-thinking control for this model.',
  });
  const images = switchField({
    label: 'Accepts images',
    value: existing?.supports_images ?? false,
    hint: 'Enables the attach control. Turn off for text-only models.',
  });
  const vision = switchField({
    label: 'Understands images',
    value: existing?.supports_vision ?? false,
  });
  const streaming = switchField({ label: 'Streams', value: existing?.supports_streaming ?? true });
  const systemPrompt = switchField({ label: 'Takes a system prompt', value: existing?.supports_system_prompt ?? true });
  const tools = switchField({ label: 'Supports tools', value: existing?.supports_tools ?? false });

  const contextWindow = numberField({
    label: 'Context window',
    value: existing?.context_window ?? null,
    placeholder: '200000',
    min: 0,
  });
  const maxOutput = numberField({
    label: 'Max output tokens',
    value: existing?.max_output_tokens ?? null,
    placeholder: '8192',
    min: 0,
  });

  const requestWeight = numberField({
    label: 'Per request',
    value: existing?.request_weight ?? 0,
    step: 0.1,
    min: 0,
    hint: 'Credits charged just for asking.',
  });
  const inputWeight = numberField({
    label: 'Per 1k input tokens',
    value: existing?.input_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });
  const outputWeight = numberField({
    label: 'Per 1k output tokens',
    value: existing?.output_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });
  const reasoningWeight = numberField({
    label: 'Per 1k reasoning tokens',
    value: existing?.reasoning_token_weight ?? 1,
    step: 0.1,
    min: 0,
  });

  const drawer = openDrawer({
    title: creating ? 'Add a model' : existing.display_name,
    confirmLabel: creating ? 'Add' : 'Save',
    ...(existing
      ? { destructive: { label: 'Delete', onSelect: (handle) => removeModel(view, existing, handle) } }
      : {}),
    build: (body) => {
      if (creating) body.appendChild(providerID.element);
      else body.appendChild(readOnly('Provider', existing.provider_name));
      body.appendChild(modelID.element);
      body.appendChild(displayName.element);
      body.appendChild(description.element);
      body.appendChild(enabled.element);
      body.appendChild(sortOrder.element);

      body.appendChild(section('Capabilities', 'No endpoint reports these, so they are declared here.'));
      body.appendChild(reasoning.element);
      body.appendChild(images.element);
      body.appendChild(vision.element);
      body.appendChild(streaming.element);
      body.appendChild(systemPrompt.element);
      body.appendChild(tools.element);
      body.appendChild(contextWindow.element);
      body.appendChild(maxOutput.element);

      body.appendChild(section('Credit weights',
        'What a turn costs against a user allowance. Leave everything at one for a flat rate.'));
      body.appendChild(requestWeight.element);
      body.appendChild(inputWeight.element);
      body.appendChild(outputWeight.element);
      body.appendChild(reasoningWeight.element);
    },
    onConfirm: async (handle) => {
      const payload: Record<string, unknown> = {
        model_id: modelID.value(),
        display_name: displayName.value(),
        description: description.value(),
        enabled: enabled.value(),
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

  modelID.focus();
  void drawer;
}

async function removeModel(view: AdminView, model: AdminModel, drawer: DrawerHandle): Promise<void> {
  if (!window.confirm(`Delete ${model.display_name}? Conversations that used it keep their messages.`)) return;
  drawer.setBusy(true);
  try {
    await adminApi.deleteModel(model.id);
    drawer.close();
    view.reload();
  } catch (error) {
    drawer.setBusy(false);
    drawer.setError(error instanceof ApiError ? error.message : String(error));
  }
}

/** A field that shows a value the form cannot change. */
export function readOnly(label: string, value: string): HTMLElement {
  const wrap = el('div', 'oa-field');
  wrap.appendChild(el('span', 'oa-field-label', label));
  wrap.appendChild(el('span', 'oa-field-hint', value));
  return wrap;
}
