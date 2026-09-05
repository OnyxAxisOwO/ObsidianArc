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
import { button, clear, el } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { checkboxList, numberField, section, switchField, textArea, textField } from '../ui/form';
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

const WINDOW_LABELS: Record<QuotaWindowKind, string> = {
  '5h': 'Every 5 hours',
  '1w': 'Every week',
  '1m': 'Every month',
};

export async function renderGroups(view: AdminView): Promise<void> {
  view.setTitle('Groups', 'Which models a set of people may use, and what they may spend.');

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
  view.actions.appendChild(button('oa-btn primary', 'Add a group', () => {
    editGroup(view, models, null, emptyPolicy('group', ''));
  }));

  clear(view.body);
  view.body.appendChild(renderTable({
    columns: [
      { header: 'Group', cell: (row) => stacked(row.name, row.description || undefined) },
      { header: 'Members', cell: (row) => String(row.members), numeric: true },
      {
        header: 'Models',
        cell: (row) => (row.allow_all_models
          ? badge('all', 'muted')
          : badge(`${row.model_ids.length} allowed`, 'muted')),
      },
      { header: 'Limits', cell: (row) => limitSummary(policyFor(row.id)), secondary: true },
      { header: '', cell: (row) => (row.is_default ? badge('default') : el('span')) },
    ],
    rows: groups,
    empty: 'No groups yet.',
    onSelect: (row) => editGroup(view, models, row, policyFor(row.id)),
  }));

  view.body.appendChild(el('p', 'oa-field-hint',
    'A user override, and the instance-wide default, are set on the Usage page.'));
}

function limitSummary(policy: QuotaPolicy): HTMLElement {
  const parts: Array<HTMLElement | null> = [];
  if (policy.rpm) parts.push(badge(`${policy.rpm}/min`, 'muted'));
  for (const kind of ['5h', '1w', '1m'] as QuotaWindowKind[]) {
    const window = policy.windows[kind];
    if (!window?.enabled) continue;
    const figure = window.credits ?? window.tokens ?? window.requests;
    parts.push(badge(figure === null || figure === undefined ? kind : `${kind}: ${figure}`, 'muted'));
  }
  if (!parts.length) parts.push(badge('inherits', 'muted'));
  return badges(...parts);
}

function editGroup(
  view: AdminView,
  models: AdminModel[],
  existing: Group | null,
  policy: QuotaPolicy,
): void {
  const creating = existing === null;

  const name = textField({ label: 'Name', value: existing?.name ?? '', placeholder: 'Pro', maxLength: 40 });
  const description = textArea({
    label: 'Description',
    value: existing?.description ?? '',
    rows: 2,
    hint: 'For your own reference; users never see it.',
  });
  const isDefault = switchField({
    label: 'New accounts join this group',
    value: existing?.is_default ?? false,
    hint: 'Exactly one group has this. Turning it on here turns it off elsewhere.',
  });
  const allowAll = switchField({
    label: 'May use every enabled model',
    value: existing?.allow_all_models ?? false,
    hint: 'A shortcut, so adding a model does not mean revisiting every group.',
    onChange: () => panel.rebuild(),
  });
  const sortOrder = numberField({ label: 'Sort order', value: existing?.sort_order ?? 0 });

  const allowed = checkboxList({
    label: 'Allowed models',
    items: models.map((model) => ({
      value: model.id,
      label: model.display_name,
      sub: `${model.provider_name} · ${model.model_id}`,
    })),
    selected: existing?.model_ids ?? [],
    emptyText: 'No models configured yet.',
  });

  const rpm = numberField({
    label: 'Requests per minute',
    value: policy.rpm,
    placeholder: 'inherit',
    min: 0,
    hint: 'Leave empty to inherit the instance default.',
  });
  const tpm = numberField({ label: 'Tokens per minute', value: policy.tpm, placeholder: 'inherit', min: 0 });

  const windows = (['5h', '1w', '1m'] as QuotaWindowKind[]).map((kind) => {
    const limits: QuotaLimits = policy.windows[kind] ?? { enabled: null, requests: null, tokens: null, credits: null };
    return {
      kind,
      enabled: switchField({ label: `Enforce ${WINDOW_LABELS[kind].toLowerCase()}`, value: limits.enabled === true }),
      requests: numberField({ label: 'Requests', value: limits.requests, placeholder: 'no limit', min: 0 }),
      tokens: numberField({ label: 'Tokens', value: limits.tokens, placeholder: 'no limit', min: 0 }),
      credits: numberField({ label: 'Credits', value: limits.credits, placeholder: 'no limit', min: 0, step: 0.1 }),
    };
  });

  const panel = openPanel({
    host: view.host,
    title: creating ? 'Add a group' : existing.name,
    confirmLabel: creating ? 'Add' : 'Save',
    width: 460,
    ...(existing && !existing.is_default
      ? { destructive: { label: 'Delete', onSelect: (handle) => removeGroup(view, existing, handle) } }
      : {}),
    build: (body) => {
      body.appendChild(name.element);
      body.appendChild(description.element);
      body.appendChild(isDefault.element);
      body.appendChild(sortOrder.element);

      body.appendChild(section('Model access'));
      body.appendChild(allowAll.element);
      if (!allowAll.value()) body.appendChild(allowed.element);

      body.appendChild(section('Allowance',
        'Empty means inherit from the instance default. A window that is not enforced is still measured.'));
      body.appendChild(rpm.element);
      body.appendChild(tpm.element);

      for (const window of windows) {
        body.appendChild(section(WINDOW_LABELS[window.kind]));
        body.appendChild(window.enabled.element);
        body.appendChild(window.requests.element);
        body.appendChild(window.tokens.element);
        body.appendChild(window.credits.element);
      }
    },
    onConfirm: async (handle) => {
      handle.setBusy(true);
      try {
        const payload: Record<string, unknown> = {
          name: name.value(),
          description: description.value(),
          is_default: isDefault.value(),
          allow_all_models: allowAll.value(),
          sort_order: sortOrder.value() ?? 0,
          model_ids: allowAll.value() ? [] : allowed.value(),
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
  if (!window.confirm(`Delete ${group.name}? Its ${group.members} member(s) move to the default group.`)) return;
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
