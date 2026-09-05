// Providers: the upstream endpoints an administrator points this server at.
//
// The API key is write-only. The form never receives one — the row carries
// only a hint like ••••1234 — so editing a provider's name cannot leak the
// credential into a response, and leaving the key field empty on an edit
// means "keep the one you have" rather than "clear it".

import { ApiError } from '../api/client';
import { t, tn } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, section, selectField, switchField, textField } from '../ui/form';
import { badge, badges, relativeTime, renderTable, stacked } from '../ui/table';
import { adminApi, type Meta, type Provider, type ProviderKind, type ReasoningStyle } from './api';
import { failure, type AdminView } from './admin-page';

export async function renderProviders(view: AdminView): Promise<void> {
  view.setTitle(t('providersTitle'), t('providersSubtitle'));

  let providers: Provider[];
  let meta: Meta;
  try {
    [{ providers }, meta] = await Promise.all([adminApi.providers(), adminApi.meta()]);
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  view.actions.appendChild(button('oa-btn primary', t('addProvider'), () => {
    editProvider(view, meta, null);
  }));

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      { header: t('colName'), cell: (row) => stacked(row.name, row.base_url) },
      { header: t('colType'), cell: (row) => badge(row.kind === 'anthropic' ? t('protocolAnthropic') : t('protocolOpenAI'), 'muted') },
      { header: t('colKey'), cell: (row) => row.api_key_hint || '—', secondary: true },
      { header: t('colModels'), cell: (row) => String(row.model_count), numeric: true },
      {
        header: t('colState'),
        cell: (row) => badges(
          row.enabled ? badge(t('enabled'), 'muted') : badge(t('disabled'), 'danger'),
          row.reasoning_style !== 'auto' ? badge(row.reasoning_style, 'muted') : null,
        ),
      },
      { header: t('colUpdated'), cell: (row) => relativeTime(row.updated_at), secondary: true },
    ],
    rows: providers,
    empty: t('noProviders'),
    muted: (row) => !row.enabled,
    onSelect: (row) => editProvider(view, meta, row),
  }));
}

function editProvider(view: AdminView, meta: Meta, existing: Provider | null): void {
  const creating = existing === null;

  const name = textField({
    label: t('name'),
    value: existing?.name ?? '',
    placeholder: 'OpenRouter',
    hint: t('providerNameHint'),
  });

  const kind = selectField<ProviderKind>({
    label: t('protocol'),
    value: existing?.kind ?? 'openai',
    hint: t('protocolHint'),
    options: meta.provider_kinds.map((value) => ({
      value,
      label: value === 'anthropic' ? t('protocolAnthropic') : t('protocolOpenAI'),
    })),
    onChange: () => panel.rebuild(),
  });

  const baseURL = textField({
    label: t('baseURL'),
    value: existing?.base_url ?? '',
    placeholder: 'https://openrouter.ai/api/v1',
    hint: t('baseURLHint'),
    monospace: true,
  });

  const apiKey = textField({
    label: creating ? t('apiKey') : t('replaceAPIKey'),
    placeholder: creating ? 'sk-…' : t('apiKeyKeepHint', { hint: existing.api_key_hint }),
    hint: t('apiKeyHint'),
    type: 'password',
  });

  const reasoning = selectField<ReasoningStyle>({
    label: t('reasoningStyle'),
    value: existing?.reasoning_style ?? 'auto',
    hint: t('reasoningStyleHint'),
    options: meta.reasoning_styles.map((value) => ({ value, label: reasoningLabel(value) })),
  });

  const timeout = numberField({
    label: t('timeoutSeconds'),
    value: existing?.timeout_seconds ?? 120,
    min: 5,
    max: 900,
  });

  const anthropicVersion = textField({
    label: t('anthropicVersion'),
    value: existing?.anthropic_version ?? '',
    placeholder: '2023-06-01',
    hint: t('anthropicVersionHint'),
    monospace: true,
  });

  const enabled = switchField({
    label: t('enabled'),
    value: existing?.enabled ?? true,
    hint: t('providerEnabledHint'),
  });

  const sortOrder = numberField({ label: t('sortOrder'), value: existing?.sort_order ?? 0 });

  const panel = openPanel({
    host: view.host,
    title: creating ? t('addProvider') : existing.name,
    confirmLabel: creating ? t('add') : t('save'),
    ...(existing
      ? {
          destructive: {
            label: t('deleteLabel'),
            confirm: tn(existing.model_count, 'confirmDeleteProviderOne', 'confirmDeleteProviderOther', { name: existing.name }),
            onSelect: (handle) => removeProvider(view, existing, handle),
          },
        }
      : {}),
    build: (body) => {
      body.appendChild(name.element);
      body.appendChild(kind.element);
      body.appendChild(baseURL.element);
      body.appendChild(apiKey.element);

      body.appendChild(section(t('secBehaviour')));
      body.appendChild(reasoning.element);
      if (kind.value() === 'anthropic') body.appendChild(anthropicVersion.element);
      body.appendChild(timeout.element);
      body.appendChild(enabled.element);
      body.appendChild(sortOrder.element);

      if (existing) {
        body.appendChild(section(t('navModels')));
        const detect = button('oa-btn', t('detect'), () => {
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
  trigger.textContent = t('detecting');

  const existingPanel = body.querySelector('.oa-detect-panel');
  existingPanel?.remove();

  const panel = el('div', 'oa-detect-panel');
  body.appendChild(panel);

  try {
    const { models } = await adminApi.detect(provider.id);
    panel.appendChild(el('p', 'oa-detect-status', t('nModelsFound', { count: models.length })));

    const list = el('div', 'oa-detect-list');
    const boxes: Array<{ box: HTMLInputElement; modelID: string; displayName: string }> = [];
    for (const model of models) {
      const row = el('label', 'oa-detect-row');
      const box = el('input');
      box.type = 'checkbox';
      box.disabled = model.configured;
      row.appendChild(box);
      row.appendChild(el('span', null, model.display_name ? `${model.display_name} — ${model.model_id}` : model.model_id));
      if (model.configured) row.appendChild(el('span', 'oa-detect-known', t('alreadyAdded')));
      list.appendChild(row);
      boxes.push({ box, modelID: model.model_id, displayName: model.display_name });
    }
    panel.appendChild(list);

    const actions = el('div', 'oa-detect-actions');
    const add = button('oa-btn primary', t('addSelected'), () => {
      const picked = boxes.filter((entry) => entry.box.checked);
      if (!picked.length) return;
      add.disabled = true;
      add.textContent = t('adding');
      void Promise.all(picked.map((entry) =>
        adminApi.createModel({
          provider_id: provider.id,
          model_id: entry.modelID,
          display_name: entry.displayName || entry.modelID,
        }),
      )).then(() => {
        add.textContent = t('addedN', { count: picked.length });
        for (const entry of picked) {
          entry.box.checked = false;
          entry.box.disabled = true;
        }
      }).catch((error: unknown) => {
        add.disabled = false;
        add.textContent = t('addSelected');
        panel.appendChild(el('p', 'oa-detect-status', error instanceof ApiError ? error.message : String(error)));
      });
    });
    actions.appendChild(add);
    panel.appendChild(actions);
  } catch (error) {
    panel.appendChild(el('p', 'oa-detect-status', error instanceof ApiError ? error.message : String(error)));
  } finally {
    trigger.disabled = false;
    trigger.textContent = t('detect');
  }
}

async function removeProvider(view: AdminView, provider: Provider, panel: PanelHandle): Promise<void> {
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

// Shared with the models screen, which offers the same list plus an
// "inherit" entry.
export function reasoningLabel(style: ReasoningStyle): string {
  switch (style) {
    case 'auto': return t('styleAuto');
    case 'none': return t('styleNone');
    case 'anthropic': return t('styleAnthropic');
    case 'openai_effort': return t('styleEffort');
    case 'openrouter': return t('styleOpenRouter');
    case 'qwen': return t('styleQwen');
  }
}
