// The screen where an account issues keys for the API.
//
// It is a panel over the chat rather than a page, for the same reason
// settings is: it is something you open, do one thing in, and close.
//
// The shape of it is decided by one fact — the token exists once. The server
// keeps a digest and nothing else, so there is no "show key" button to build
// and no way to recover one that was not copied. That makes the moment of
// creation the whole design: the new token is shown in a panel of its own,
// selected and ready to copy, with the warning next to it rather than after
// it. Everything else here — renaming, re-expiring, pausing, model restrictions,
// revoking — is housekeeping on rows that no longer contain a secret.

import { api, ApiError } from '../api/client';
import { createKey, deleteKey, listKeys, updateKey, type ApiKey } from '../api/keys';
import type { AvailableModel } from '../chat/model-picker';
import { renderChatPage } from '../chat/chat-page';
import { t } from '../i18n';
import { navigate } from '../router';
import { ICONS, button, clear, el, icon, iconButton } from '../ui/dom';
import { checkboxList, selectField, textField } from '../ui/form';
import { openPanel, type PanelHandle } from '../ui/panel';
import { absoluteTime, badge, relativeTime } from '../ui/table';

/** Expiry choices, as milliseconds from now. Zero is "never". */
const LIFETIMES = [
  { days: 0, label: 'keyNever' },
  { days: 7, label: 'keyDays7' },
  { days: 30, label: 'keyDays30' },
  { days: 90, label: 'keyDays90' },
  { days: 365, label: 'keyDays365' },
] as const;

const DAY_MS = 24 * 60 * 60 * 1000;

// How long a revoke button stays armed. Long enough to read the question and
// decide; short enough that an armed button left alone goes back to being a
// safe one.
const ARM_MS = 6000;

export function renderKeysPage(root: HTMLElement): void {
  const host = renderChatPage(root);

  let keys: ApiKey[] = [];
  let models: AvailableModel[] = [];
  let enabled = false;
  let max = 0;
  let loaded = false;
  // The token from the most recent creation, held only until the panel is
  // repainted past it. Never written anywhere else.
  let issued: { key: ApiKey; token: string } | null = null;

  const panel = openPanel({
    host,
    title: t('apiKeys'),
    footer: false,
    width: 520,
    build: (body) => {
      if (issued) {
        body.appendChild(issuedSection(issued, () => {
          issued = null;
          panel.rebuild();
        }));
        return;
      }
      if (!loaded) {
        body.appendChild(el('p', 'oa-menu-empty', t('loading')));
        return;
      }
      body.appendChild(introSection(enabled));
      if (enabled) body.appendChild(createSection(panel, keys.length >= max, max));
      body.appendChild(listSection(panel, keys));
    },
    onClose: () => {
      if (window.location.pathname === '/keys') navigate('/', { replace: true });
    },
  });

  void refresh();

  async function refresh(): Promise<void> {
    try {
      const [result, modelsRes] = await Promise.all([
        listKeys(),
        api.get<{ models: AvailableModel[] }>('/api/models').catch(() => ({ models: [] })),
      ]);
      keys = result.keys;
      enabled = result.enabled;
      max = result.max;
      models = modelsRes.models ?? [];
    } catch (error) {
      panel.setError(error instanceof ApiError ? error.message : t('failed'));
    } finally {
      loaded = true;
      panel.rebuild();
    }
  }

  function getModelName(id: string): string {
    const found = models.find((m) => m.id === id);
    if (found) return found.display_name || found.id;
    return id;
  }

  // --- sections ---------------------------------------------------------------

  function createSection(handle: PanelHandle, full: boolean, ceiling: number): HTMLElement {
    const wrap = el('section', 'oa-keys-create');
    wrap.appendChild(el('h3', 'oa-panel-section-title', t('keyNew')));

    if (full) {
      wrap.appendChild(el('p', 'oa-field-hint', t('keyLimitReached', { count: ceiling })));
      return wrap;
    }

    const name = textField({ label: t('keyName'), placeholder: t('keyNamePlaceholder'), maxLength: 60 });
    const lifetime = selectField({
      label: t('keyExpires'),
      value: '0',
      options: LIFETIMES.map((entry) => ({ value: String(entry.days), label: t(entry.label) })),
    });

    const modelChecks = checkboxList({
      label: t('keyModel'),
      hint: t('keyModelHint'),
      items: modelItems(models),
      selected: [],
      emptyText: t('keyNoModels'),
    });

    const submit = button('oa-btn primary', t('keyCreate'), () => {
      const label = name.value().trim();
      if (!label) {
        handle.setError(t('keyNameRequired'));
        name.focus();
        return;
      }
      const days = Number(lifetime.value());
      const modelIDs = modelChecks.value();
      submit.disabled = true;
      handle.setBusy(true);

      void createKey(label, days > 0 ? Date.now() + days * DAY_MS : 0, modelIDs)
        .then((result) => {
          issued = result;
          return refresh();
        })
        .catch((error: unknown) => {
          handle.setError(error instanceof ApiError ? error.message : t('failed'));
          submit.disabled = false;
        })
        .finally(() => handle.setBusy(false));
    });

    wrap.appendChild(name.element);
    wrap.appendChild(lifetime.element);
    wrap.appendChild(modelChecks.element);
    wrap.appendChild(submit);
    return wrap;
  }

  function listSection(handle: PanelHandle, rows: ApiKey[]): HTMLElement {
    const wrap = el('section', 'oa-keys-list-wrap');
    wrap.appendChild(el('h3', 'oa-panel-section-title', t('keyYours')));

    if (!rows.length) {
      wrap.appendChild(el('p', 'oa-menu-empty', t('keysEmpty')));
      return wrap;
    }

    const list = el('div', 'oa-keys-list');
    rows.forEach((row, index) => {
      list.appendChild(keyRow(handle, row, index));
    });
    wrap.appendChild(list);
    return wrap;
  }

  function keyRow(handle: PanelHandle, row: ApiKey, index: number): HTMLElement {
    const item = el('div', row.disabled ? 'oa-key-row oa-key-row-paused' : 'oa-key-row');
    item.style.setProperty('--item-idx', String(index));

    const info = el('div', 'oa-key-info');
    const title = el('div', 'oa-key-title');
    title.appendChild(el('span', 'oa-key-name', row.name));
    if (row.disabled) {
      title.appendChild(badge(t('keyPaused'), 'warning'));
    }
    if (expired(row)) {
      title.appendChild(badge(t('keyExpired'), 'danger'));
    }
    info.appendChild(title);

    const meta = el('div', 'oa-key-meta');
    meta.appendChild(el('code', 'oa-key-prefix', `${row.prefix}…`));
    const rowModelIDs = modelIDsFor(row);
    if (rowModelIDs.length) {
      const modelNames = rowModelIDs.map(getModelName).join(', ');
      const modelPill = el('span', 'oa-key-model-pill', t('keyOnlyModel', { model: modelNames }));
      modelPill.title = rowModelIDs.join(', ');
      meta.appendChild(modelPill);
    } else {
      meta.appendChild(el('span', 'oa-key-model-all', t('keyAllModels')));
    }
    meta.appendChild(el('span', null, expiryLabel(row)));
    meta.appendChild(el('span', null, row.last_used_at
      ? t('keyLastUsed', { when: relativeTime(row.last_used_at) })
      : t('keyNeverUsed')));
    info.appendChild(meta);
    item.appendChild(info);

    const actions = el('div', 'oa-key-actions');

    const toggle = iconButton(
      row.disabled ? 'oa-icon-btn active oa-key-toggle-btn' : 'oa-icon-btn oa-key-toggle-btn',
      row.disabled ? ICONS.play : ICONS.pause,
      row.disabled ? t('keyResume') : t('keyPause'),
      () => {
        toggle.disabled = true;
        handle.setBusy(true);
        void updateKey(row.id, { disabled: !row.disabled })
          .then(refresh)
          .catch((error: unknown) => {
            handle.setError(error instanceof ApiError ? error.message : t('failed'));
            toggle.disabled = false;
          })
          .finally(() => handle.setBusy(false));
      },
      15,
    );
    actions.appendChild(toggle);

    const edit = iconButton('oa-icon-btn', ICONS.gear, t('edit'), () => {
      editKey(handle, row);
    }, 15);
    actions.appendChild(edit);

    // Revoking is immediate and cannot be undone, so the button asks first —
    // in place, because a dialog the browser may suppress is a button that
    // silently does nothing.
    const remove = iconButton('oa-icon-btn danger', ICONS.trash, t('keyRevoke'), () => {}, 15);
    let armed = false;
    remove.addEventListener('click', () => {
      if (!armed) {
        armed = true;
        clear(remove);
        remove.appendChild(el('span', 'oa-key-confirm', t('keyRevokeConfirm')));
        toggle.hidden = true;
        edit.hidden = true;
        window.setTimeout(() => {
          if (!armed) return;
          armed = false;
          toggle.hidden = false;
          edit.hidden = false;
          clear(remove);
          remove.appendChild(icon(ICONS.trash, 15));
        }, ARM_MS);
        return;
      }
      handle.setBusy(true);
      void deleteKey(row.id)
        .then(refresh)
        .catch((error: unknown) => {
          handle.setError(error instanceof ApiError ? error.message : t('failed'));
        })
        .finally(() => handle.setBusy(false));
    });
    actions.appendChild(remove);

    item.appendChild(actions);
    return item;
  }

  function editKey(handle: PanelHandle, row: ApiKey): void {
    const body = handle.body;
    clear(body);

    const wrap = el('section', 'oa-keys-create oa-keys-edit-animated');
    wrap.appendChild(el('h3', 'oa-panel-section-title', t('keyEdit')));

    const name = textField({ label: t('keyName'), value: row.name, maxLength: 60 });
    // "Leave as it is" is a real choice here, and the only one that does not
    // silently move an expiry the owner set deliberately.
    const lifetime = selectField({
      label: t('keyExpires'),
      value: 'keep',
      options: [
        { value: 'keep', label: t('keyKeepExpiry', { when: expiryLabel(row) }) },
        ...LIFETIMES.map((entry) => ({ value: String(entry.days), label: t(entry.label) })),
      ],
    });

    const statusField = selectField({
      label: t('keyStatus'),
      value: row.disabled ? 'paused' : 'active',
      options: [
        { value: 'active', label: t('keyStatusActive') },
        { value: 'paused', label: t('keyStatusPaused') },
      ],
    });

    const selectedModelIDs = modelIDsFor(row);
    const modelItems = modelItemsFor(models, selectedModelIDs);
    const modelChecks = checkboxList({
      label: t('keyModel'),
      hint: t('keyModelHint'),
      items: modelItems,
      selected: selectedModelIDs,
      emptyText: t('keyNoModels'),
    });

    const save = button('oa-btn primary', t('save'), () => {
      const label = name.value().trim();
      if (!label) {
        handle.setError(t('keyNameRequired'));
        name.focus();
        return;
      }
      const choice = lifetime.value();
      const changes: { name?: string; expires_at?: number; disabled?: boolean; model_ids?: string[] } = {
        name: label,
        disabled: statusField.value() === 'paused',
        model_ids: modelChecks.value(),
      };
      if (choice !== 'keep') {
        const days = Number(choice);
        changes.expires_at = days > 0 ? Date.now() + days * DAY_MS : 0;
      }
      save.disabled = true;
      handle.setBusy(true);
      void updateKey(row.id, changes)
        .then(refresh)
        .catch((error: unknown) => {
          handle.setError(error instanceof ApiError ? error.message : t('failed'));
          save.disabled = false;
        })
        .finally(() => handle.setBusy(false));
    });

    const row2 = el('div', 'oa-key-edit-actions');
    row2.appendChild(button('oa-btn', t('cancel'), () => handle.rebuild()));
    row2.appendChild(save);

    wrap.appendChild(name.element);
    wrap.appendChild(statusField.element);
    wrap.appendChild(lifetime.element);
    wrap.appendChild(modelChecks.element);
    wrap.appendChild(row2);
    body.appendChild(wrap);
  }
}

function modelItems(models: AvailableModel[]): Array<{ value: string; label: string }> {
  return modelsForSelection(models).map((m) => ({
    value: m.id,
    label: m.display_name || m.id,
  }));
}

function modelItemsFor(models: AvailableModel[], selected: string[]): Array<{ value: string; label: string }> {
  const items = modelItems(models);
  const known = new Set(items.map((item) => item.value));
  for (const modelID of selected) {
    if (!known.has(modelID)) items.unshift({ value: modelID, label: modelID });
  }
  return items;
}

function modelsForSelection(models: AvailableModel[]): AvailableModel[] {
  return models.filter((model) => model.usable !== false);
}

function modelIDsFor(row: ApiKey): string[] {
  if (row.model_ids?.length) return row.model_ids;
  return row.model_id ? [row.model_id] : [];
}

// --- the one moment the token exists ---------------------------------------------

function issuedSection(result: { key: ApiKey; token: string }, done: () => void): HTMLElement {
  const wrap = el('section', 'oa-key-issued oa-key-issued-animated');
  wrap.appendChild(el('h3', 'oa-panel-section-title', t('keyCreated', { name: result.key.name })));
  wrap.appendChild(el('p', 'oa-key-warning', t('keyShownOnce')));

  // An input rather than a block of text: it can be selected with one
  // gesture, and copied by a keyboard on a browser whose clipboard API is
  // unavailable or refused.
  const box = el('div', 'oa-key-token oa-key-token-pulse');
  const value = el('input', 'oa-key-token-input');
  value.type = 'text';
  value.readOnly = true;
  value.value = result.token;
  value.addEventListener('focus', () => value.select());
  box.appendChild(value);

  const copy = iconButton('oa-icon-btn oa-key-copy-btn', ICONS.copy, t('copy'), () => {
    value.select();
    void navigator.clipboard?.writeText(result.token)
      .then(() => {
        copy.classList.add('copied');
        clear(copy);
        copy.appendChild(icon(ICONS.check, 15));
        window.setTimeout(() => {
          copy.classList.remove('copied');
          clear(copy);
          copy.appendChild(icon(ICONS.copy, 15));
        }, 1600);
      })
      .catch(() => {
        // Refused or unavailable: the field is selected either way, which is
        // all a manual copy needs.
      });
  }, 15);
  box.appendChild(copy);
  wrap.appendChild(box);

  wrap.appendChild(usageHint());
  wrap.appendChild(button('oa-btn primary', t('keyCopied'), done));
  return wrap;
}

function introSection(enabled: boolean): HTMLElement {
  const wrap = el('section', 'oa-keys-intro');
  wrap.appendChild(el('p', 'oa-field-hint', t('apiKeysIntro')));
  if (!enabled) {
    wrap.appendChild(el('p', 'oa-key-warning', t('apiDisabledForYou')));
    return wrap;
  }
  wrap.appendChild(usageHint());
  return wrap;
}

/** The base URL to paste into a client, which is the other half of a key. */
function usageHint(): HTMLElement {
  const wrap = el('div', 'oa-key-endpoint');
  wrap.appendChild(el('span', 'oa-field-label', t('apiBaseUrl')));
  wrap.appendChild(el('code', null, `${window.location.origin}/v1`));
  return wrap;
}

// --- small helpers ----------------------------------------------------------------

function expired(row: ApiKey): boolean {
  return row.expires_at > 0 && row.expires_at <= Date.now();
}

function expiryLabel(row: ApiKey): string {
  if (!row.expires_at) return t('keyNoExpiry');
  if (expired(row)) return t('keyExpiredAt', { when: absoluteTime(row.expires_at) });
  return t('keyExpiresAt', { when: absoluteTime(row.expires_at) });
}
