// Groups: who may use which models, and what allowance they share.
//
// Nothing is named in advance. There is no Free and no Pro anywhere in the
// source; an instance starts with one group called Default and an
// administrator makes whatever the deployment actually needs.
//
// A group's quota lives in the same panel as its permissions, because they
// are the same decision — "what does this tier get" — and splitting them
// across two screens makes an operator hold half the answer in their head.

import { ApiError } from '../api/client';
import { t, tn } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, section, switchField, textArea, textField, tierList } from '../ui/form';
import { badge, badges, renderTable, stacked } from '../ui/table';
import {
  adminApi,
  emptyPolicy,
  type AdminModel,
  type Group,
  type QuotaLimits,
  type QuotaPolicy,
  type QuotaWindowKind,
} from './api';
import { failure, type AdminView } from './admin-page';

function windowLabel(kind: QuotaWindowKind): string {
  return kind === '5h' ? t('every5h') : kind === '1w' ? t('everyWeek') : t('everyMonth');
}

export async function renderGroups(view: AdminView): Promise<void> {
  view.setTitle(t('groupsTitle'), t('groupsSubtitle'));

  let groups: Group[];
  let policies: QuotaPolicy[];
  let models: AdminModel[];
  try {
    [{ groups, policies }, { models }] = await Promise.all([adminApi.groups(), adminApi.models()]);
  } catch (error) {
    failure(view, error);
    return;
  }

  const policyFor = (groupID: string) =>
    policies.find((policy) => policy.scope === 'group' && policy.scope_id === groupID)
    ?? emptyPolicy('group', groupID);

  clear(view.actions);
  view.actions.appendChild(button('oa-btn primary', t('addGroup'), () => {
    editGroup(view, models, null, emptyPolicy('group', ''));
  }));

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      { header: t('colGroup'), cell: (row) => stacked(row.name, row.description || undefined) },
      { header: t('colMembers'), cell: (row) => String(row.members), numeric: true },
      {
        header: t('colModels'),
        cell: (row) => (row.allow_all_models
          ? badge(t('allModels'), 'muted')
          : badge(t('nAllowed', { count: row.model_ids.length }), 'muted')),
      },
      { header: t('colLimits'), cell: (row) => limitSummary(policyFor(row.id)), secondary: true },
      { header: '', cell: (row) => (row.is_default ? badge(t('defaultBadge')) : el('span')) },
    ],
    rows: groups,
    empty: t('noGroups'),
    onSelect: (row) => editGroup(view, models, row, policyFor(row.id)),
  }));

  view.body.appendChild(el('p', 'oa-field-hint', t('groupsFooterHint')));
}

function limitSummary(policy: QuotaPolicy): HTMLElement {
  const parts: Array<HTMLElement | null> = [];
  if (policy.rpm) parts.push(badge(t('perMinute', { count: policy.rpm }), 'muted'));
  for (const kind of ['5h', '1w', '1m'] as QuotaWindowKind[]) {
    const window = policy.windows[kind];
    if (!window?.enabled) continue;
    const figure = window.credits ?? window.tokens ?? window.requests;
    parts.push(badge(figure === null || figure === undefined ? kind : `${kind}: ${figure}`, 'muted'));
  }
  if (!parts.length) parts.push(badge(t('inherits'), 'muted'));
  return badges(...parts);
}

function editGroup(
  view: AdminView,
  models: AdminModel[],
  existing: Group | null,
  policy: QuotaPolicy,
): void {
  const creating = existing === null;

  const name = textField({ label: t('name'), value: existing?.name ?? '', placeholder: t('groupNamePlaceholder'), maxLength: 40 });
  const description = textArea({
    label: t('description'),
    value: existing?.description ?? '',
    rows: 2,
    hint: t('groupDescriptionHint'),
  });
  const isDefault = switchField({
    label: t('isDefault'),
    value: existing?.is_default ?? false,
    hint: t('isDefaultHint'),
  });
  const allowAll = switchField({
    label: t('allowAllModels'),
    value: existing?.allow_all_models ?? false,
    hint: t('allowAllModelsHint'),
    onChange: () => panel.rebuild(),
  });
  const sortOrder = numberField({ label: t('sortOrder'), value: existing?.sort_order ?? 0 });

  const initialGrants: Record<string, 'use' | 'view'> = {};
  if (existing?.model_grants && existing.model_grants.length > 0) {
    for (const grant of existing.model_grants) {
      if (grant.access === 'use' || grant.access === 'view') {
        initialGrants[grant.model_id] = grant.access;
      }
    }
  } else if (existing?.model_ids) {
    for (const id of existing.model_ids) {
      initialGrants[id] = 'use';
    }
  }

  const allowed = tierList({
    label: t('allowedModels'),
    hint: t('modelAccessTiersHint'),
    items: models.map((model) => ({
      value: model.id,
      label: model.display_name,
      sub: `${model.provider_name} · ${model.model_id}`,
    })),
    selected: initialGrants,
    emptyText: t('noModelsConfigured'),
  });

  const rpm = numberField({
    label: t('requestsPerMinute'),
    value: policy.rpm,
    placeholder: t('inherit'),
    min: 0,
    hint: t('inheritHint'),
  });
  const tpm = numberField({ label: t('tokensPerMinute'), value: policy.tpm, placeholder: t('inherit'), min: 0 });

  const windows = (['5h', '1w', '1m'] as QuotaWindowKind[]).map((kind) => {
    const limits: QuotaLimits = policy.windows[kind] ?? { enabled: null, requests: null, tokens: null, credits: null };
    return {
      kind,
      enabled: switchField({ label: t('enforceWindow', { window: windowLabel(kind).toLowerCase() }), value: limits.enabled === true }),
      requests: numberField({ label: t('limitRequests'), value: limits.requests, placeholder: t('noLimit'), min: 0 }),
      tokens: numberField({ label: t('limitTokens'), value: limits.tokens, placeholder: t('noLimit'), min: 0 }),
      credits: numberField({ label: t('limitCredits'), value: limits.credits, placeholder: t('noLimit'), min: 0, step: 0.1 }),
    };
  });

  const panel = openPanel({
    host: view.host,
    title: creating ? t('addGroup') : existing.name,
    confirmLabel: creating ? t('add') : t('save'),
    width: 460,
    ...(existing && !existing.is_default
      ? {
          destructive: {
            label: t('deleteLabel'),
            confirm: tn(existing.members, 'confirmDeleteGroupOne', 'confirmDeleteGroupOther', { name: existing.name }),
            onSelect: (handle) => removeGroup(view, existing, handle),
          },
        }
      : {}),
    build: (body) => {
      body.appendChild(name.element);
      body.appendChild(description.element);
      body.appendChild(isDefault.element);
      body.appendChild(sortOrder.element);

      body.appendChild(section(t('secModelAccess')));
      body.appendChild(allowAll.element);
      if (!allowAll.value()) body.appendChild(allowed.element);

      body.appendChild(section(t('secAllowance'), t('allowanceHint')));
      body.appendChild(rpm.element);
      body.appendChild(tpm.element);

      for (const window of windows) {
        body.appendChild(section(windowLabel(window.kind)));
        body.appendChild(window.enabled.element);
        body.appendChild(window.requests.element);
        body.appendChild(window.tokens.element);
        body.appendChild(window.credits.element);
      }
    },
    onConfirm: async (handle) => {
      handle.setBusy(true);
      try {
        const grantsRecord = allowed.value();
        const modelGrants = allowAll.value()
          ? []
          : Object.entries(grantsRecord).map(([model_id, access]) => ({ model_id, access }));
        const modelIDs = allowAll.value()
          ? []
          : Object.entries(grantsRecord)
              .filter(([, access]) => access === 'use')
              .map(([model_id]) => model_id);

        const payload: Record<string, unknown> = {
          name: name.value(),
          description: description.value(),
          is_default: isDefault.value(),
          allow_all_models: allowAll.value(),
          sort_order: sortOrder.value() ?? 0,
          model_ids: modelIDs,
          model_grants: modelGrants,
        };

        const saved = creating
          ? (await adminApi.createGroup(payload)).group
          : (await adminApi.updateGroup(existing.id, payload)).group;

        await adminApi.savePolicy({
          scope: 'group',
          scope_id: saved.id,
          rpm: rpm.value(),
          tpm: tpm.value(),
          windows: Object.fromEntries(windows.map((window) => [window.kind, {
            enabled: window.enabled.value() ? true : null,
            requests: window.requests.value(),
            tokens: window.tokens.value(),
            credits: window.credits.value(),
          }])),
        });

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

async function removeGroup(view: AdminView, group: Group, panel: PanelHandle): Promise<void> {
  panel.setBusy(true);
  try {
    await adminApi.deleteGroup(group.id);
    panel.close();
    view.reload();
  } catch (error) {
    panel.setBusy(false);
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}
