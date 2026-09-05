// Providers: the upstream endpoints an administrator points this server at.
//
// The API key is write-only. The form never receives one — the row carries
// only a hint like ••••1234 — so editing a provider's name cannot leak the
// credential into a response, and leaving the key field empty on an edit
// means "keep the one you have" rather than "clear it".

import { ApiError } from '../api/client';
import { button, clear, el } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, section, selectField, switchField, textField } from '../ui/form';
import { badge, badges, relativeTime, renderTable, stacked } from '../ui/table';
import { adminApi, type Meta, type Provider, type ProviderKind, type ReasoningStyle } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderProviders(view: AdminView): Promise<void> {
  view.setTitle('Providers', 'Where this server sends requests, and with which credential.');

  let providers: Provider[];
  let meta: Meta;
  try {
    [{ providers }, meta] = await Promise.all([adminApi.providers(), adminApi.meta()]);
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  view.actions.appendChild(button('oa-btn primary', 'Add a provider', () => {
    editProvider(view, meta, null);
  }));

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      { header: 'Name', cell: (row) => stacked(row.name, row.base_url) },
      { header: 'Type', cell: (row) => badge(row.kind === 'anthropic' ? 'Anthropic' : 'OpenAI-compatible', 'muted') },
      { header: 'Key', cell: (row) => row.api_key_hint || '—', secondary: true },
      { header: 'Models', cell: (row) => String(row.model_count), numeric: true },
      {
        header: 'State',
        cell: (row) => badges(
          row.enabled ? badge('enabled', 'muted') : badge('disabled', 'danger'),
          row.reasoning_style !== 'auto' ? badge(row.reasoning_style, 'muted') : null,
        ),
      },
      { header: 'Updated', cell: (row) => relativeTime(row.updated_at), secondary: true },
    ],
    rows: providers,
    empty: 'No providers yet. Add one to make models available.',
    muted: (row) => !row.enabled,
    onSelect: (row) => editProvider(view, meta, row),
  }));
}

function editProvider(view: AdminView, meta: Meta, existing: Provider | null): void {
  const creating = existing === null;

  const name = textField({
    label: 'Name',
    value: existing?.name ?? '',
    placeholder: 'OpenRouter',
    hint: 'Shown to users beside the models it serves.',
  });

  const kind = selectField<ProviderKind>({
    label: 'Protocol',
    value: existing?.kind ?? 'openai',
    hint: 'Anthropic speaks the Messages API. Everything else — OpenAI, DeepSeek, xAI, OpenRouter, Groq, Ollama, vLLM — is OpenAI-compatible.',
    options: meta.provider_kinds.map((value) => ({
      value,
      label: value === 'anthropic' ? 'Anthropic' : 'OpenAI-compatible',
    })),
    onChange: () => panel.rebuild(),
  });

  const baseURL = textField({
    label: 'Base URL',
    value: existing?.base_url ?? '',
    placeholder: 'https://openrouter.ai/api/v1',
    hint: 'The full endpoint works too. https only, except for localhost.',
    monospace: true,
  });

  const apiKey = textField({
    label: creating ? 'API key' : 'Replace the API key',
    placeholder: creating ? 'sk-…' : `Currently ${existing.api_key_hint} — leave empty to keep it`,
    hint: 'Stored encrypted. It is never sent back to a browser.',
    type: 'password',
  });

  const reasoning = selectField<ReasoningStyle>({
    label: 'Reasoning style',
    value: existing?.reasoning_style ?? 'auto',
    hint: 'Which flag this endpoint wants when a user turns on extended thinking. Auto picks the protocol default.',
    options: meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) })),
  });

  const timeout = numberField({
    label: 'Timeout (seconds)',
    value: existing?.timeout_seconds ?? 120,
    min: 5,
    max: 900,
  });

  const anthropicVersion = textField({
    label: 'Anthropic version',
    value: existing?.anthropic_version ?? '',
    placeholder: '2023-06-01',
    hint: 'Leave empty for the default.',
    monospace: true,
  });

  const enabled = switchField({
    label: 'Enabled',
    value: existing?.enabled ?? true,
    hint: 'A disabled provider hides its models from everyone.',
  });

  const sortOrder = numberField({ label: 'Sort order', value: existing?.sort_order ?? 0 });

  const panel = openPanel({
    host: view.host,
    title: creating ? 'Add a provider' : existing.name,
    confirmLabel: creating ? 'Add' : 'Save',
    ...(existing
      ? {
          destructive: {
            label: 'Delete',
            onSelect: (handle) => removeProvider(view, existing, handle),
          },
        }
      : {}),
    build: (body) => {
      body.appendChild(name.element);
      body.appendChild(kind.element);
      body.appendChild(baseURL.element);
      body.appendChild(apiKey.element);

      body.appendChild(section('Behaviour'));
      body.appendChild(reasoning.element);
      if (kind.value() === 'anthropic') body.appendChild(anthropicVersion.element);
      body.appendChild(timeout.element);
      body.appendChild(enabled.element);
      body.appendChild(sortOrder.element);

      if (existing) {
        body.appendChild(section('Models'));
        const detect = button('oa-btn', 'Detect what this endpoint serves', () => {
          void detectModels(existing, detect, body);
        });
        body.appendChild(detect);
      }
    },
    onConfirm: async (handle) => {
      const payload: Record<string, unknown> = {
        name: name.value(),
        kind: kind.value(),
        base_url: baseURL.value(),
        reasoning_style: reasoning.value(),
        timeout_seconds: timeout.value() ?? 120,
        enabled: enabled.value(),
        sort_order: sortOrder.value() ?? 0,
        anthropic_version: kind.value() === 'anthropic' ? anthropicVersion.value() : '',
      };
      // An empty key on an edit keeps the stored one; on a create there is
      // nothing to keep.
      if (apiKey.value() || creating) payload['api_key'] = apiKey.value();

      handle.setBusy(true);
      try {
        if (creating) await adminApi.createProvider(payload);
        else await adminApi.updateProvider(existing.id, payload);
        handle.close();
        view.reload();
      } catch (error) {
        handle.setBusy(false);
        handle.setError(error instanceof ApiError ? error.message : String(error));
      }
    },
  });

  name.focus();
}

async function detectModels(provider: Provider, trigger: HTMLButtonElement, body: HTMLElement): Promise<void> {
  trigger.disabled = true;
  trigger.textContent = 'Asking the endpoint…';

  const existingPanel = body.querySelector('.oa-detect-panel');
  existingPanel?.remove();

  const panel = el('div', 'oa-detect-panel');
  body.appendChild(panel);

  try {
    const { models } = await adminApi.detect(provider.id);
    panel.appendChild(el('p', 'oa-detect-status', `${models.length} models found.`));

    const list = el('div', 'oa-detect-list');
    const boxes: Array<{ box: HTMLInputElement; modelID: string; displayName: string }> = [];
    for (const model of models) {
      const row = el('label', 'oa-detect-row');
      const box = el('input');
      box.type = 'checkbox';
      box.disabled = model.configured;
      row.appendChild(box);
      row.appendChild(el('span', null, model.display_name ? `${model.display_name} — ${model.model_id}` : model.model_id));
      if (model.configured) row.appendChild(el('span', 'oa-detect-known', 'added'));
      list.appendChild(row);
      boxes.push({ box, modelID: model.model_id, displayName: model.display_name });
    }
    panel.appendChild(list);

    const actions = el('div', 'oa-detect-actions');
    const add = button('oa-btn primary', 'Add selected', () => {
      const picked = boxes.filter((entry) => entry.box.checked);
      if (!picked.length) return;
      add.disabled = true;
      add.textContent = 'Adding…';
      void Promise.all(picked.map((entry) =>
        adminApi.createModel({
          provider_id: provider.id,
          model_id: entry.modelID,
          display_name: entry.displayName || entry.modelID,
        }),
      )).then(() => {
        add.textContent = `Added ${picked.length}`;
        for (const entry of picked) {
          entry.box.checked = false;
          entry.box.disabled = true;
        }
      }).catch((error: unknown) => {
        add.disabled = false;
        add.textContent = 'Add selected';
        panel.appendChild(el('p', 'oa-detect-status', error instanceof ApiError ? error.message : String(error)));
      });
    });
    actions.appendChild(add);
    panel.appendChild(actions);
  } catch (error) {
    panel.appendChild(el('p', 'oa-detect-status', error instanceof ApiError ? error.message : String(error)));
  } finally {
    trigger.disabled = false;
    trigger.textContent = 'Detect what this endpoint serves';
  }
}

async function removeProvider(view: AdminView, provider: Provider, panel: PanelHandle): Promise<void> {
  if (!window.confirm(`Delete ${provider.name}? Its ${provider.model_count} model(s) go with it.`)) return;
  panel.setBusy(true);
  try {
    await adminApi.deleteProvider(provider.id);
    panel.close();
    view.reload();
  } catch (error) {
    panel.setBusy(false);
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}

function reasoningLabel(style: ReasoningStyle): string {
  switch (style) {
    case 'auto': return 'Auto (protocol default)';
    case 'none': return 'None — the endpoint has no switch';
    case 'anthropic': return 'Anthropic thinking budget';
    case 'openai_effort': return 'reasoning_effort';
    case 'openrouter': return 'reasoning: { effort }';
    case 'qwen': return 'enable_thinking';
  }
}
