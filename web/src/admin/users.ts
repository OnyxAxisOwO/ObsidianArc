// Users: search, edit, disable, reset, and read their conversations.
//
// That last one is an intrusion even when it is justified, so it sits behind
// its own click, says whose transcript it is, and leaves a line in the server
// log. Nothing about it is incidental to opening the account panel.

import { ApiError } from '../api/client';
import { t, tn } from '../i18n';
import { ICONS, button, clear, confirmable, el, iconButton } from '../ui/dom';
import { openPanel, type PanelHandle } from '../ui/panel';
import { numberField, section, selectField, switchField, textArea, textField } from '../ui/form';
import {
  absoluteTime,
  badge,
  badges,
  compactNumber,
  relativeTime,
  renderTable,
  stacked,
} from '../ui/table';
import { currentUser } from '../session';
import {
  adminApi,
  emptyPolicy,
  type Account,
  type ApiKey,
  type AccountStatus,
  type Group,
  type QuotaWindowKind,
  type Role,
} from './api';
import { failure, type AdminView } from './admin-page';

interface Filters {
  q: string;
  role: string;
  status: string;
  group: string;
}

const state: Filters = { q: '', role: '', status: '', group: '' };

export async function renderUsers(view: AdminView): Promise<void> {
  view.setTitle(t('usersTitle'));

  let groups: Group[];
  try {
    ({ groups } = await adminApi.groups());
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  clear(view.body);

  const filters = el('div', 'oa-filters');
  const search = el('input');
  search.type = 'search';
  search.placeholder = t('searchUsers');
  search.value = state.q;

  const roleSelect = filterSelect([
    { value: '', label: t('anyRole') },
    { value: 'user', label: t('filterUsers') },
    { value: 'admin', label: t('filterAdmins') },
  ], state.role);

  const statusSelect = filterSelect([
    { value: '', label: t('anyStatus') },
    { value: 'active', label: t('filterActive') },
    { value: 'disabled', label: t('filterDisabled') },
  ], state.status);

  const groupSelect = filterSelect([
    { value: '', label: t('anyGroup') },
    ...groups.map((group) => ({ value: group.id, label: group.name })),
  ], state.group);

  filters.appendChild(search);
  filters.appendChild(roleSelect);
  filters.appendChild(statusSelect);
  filters.appendChild(groupSelect);
  view.body.appendChild(filters);

  const results = el('div');
  view.body.appendChild(results);

  // Typing filters after a pause rather than on each keystroke: one request
  // per word, not one per letter.
  let debounce = 0;
  const refresh = () => {
    state.q = search.value.trim();
    state.role = roleSelect.value;
    state.status = statusSelect.value;
    state.group = groupSelect.value;
    void load(view, groups, results);
  };
  search.addEventListener('input', () => {
    window.clearTimeout(debounce);
    debounce = window.setTimeout(refresh, 250);
  });
  for (const select of [roleSelect, statusSelect, groupSelect]) {
    select.addEventListener('change', refresh);
  }

  await load(view, groups, results);
}

function filterSelect(options: Array<{ value: string; label: string }>, value: string): HTMLSelectElement {
  const select = el('select');
  for (const option of options) {
    const node = el('option', null, option.label);
    node.value = option.value;
    select.appendChild(node);
  }
  select.value = value;
  return select;
}

async function load(view: AdminView, groups: Group[], target: HTMLElement): Promise<void> {
  const query = new URLSearchParams();
  if (state.q) query.set('q', state.q);
  if (state.role) query.set('role', state.role);
  if (state.status) query.set('status', state.status);
  if (state.group) query.set('group_id', state.group);

  clear(target);
  target.appendChild(el('p', 'oa-table-empty', t('loading')));

  let users: Account[];
  let total: number;
  try {
    ({ users, total } = await adminApi.users(query.toString() ? `?${query}` : ''));
  } catch (error) {
    clear(target);
    target.appendChild(el('p', 'oa-table-empty', error instanceof ApiError ? error.message : String(error)));
    return;
  }

  const groupName = (id: string) => groups.find((group) => group.id === id)?.name ?? '—';

  clear(target);
  view.setTitle(t('usersTitle'), tn(total, 'accountsCountOne', 'accountsCountOther'));
  target.appendChild(renderTable({
    columns: [
      { header: t('colAccount'), cell: (row) => stacked(row.nickname || row.username, row.qq ? `@${row.username} · QQ ${row.qq}` : `@${row.username}`) },
      { header: t('colEmail'), cell: (row) => row.email || '—', secondary: true, width: '160px' },
      { header: t('colGroup'), cell: (row) => groupName(row.group_id), width: '120px' },
      {
        header: t('colRole'),
        cell: (row) => badges(
          row.role === 'admin' ? badge(t('admin')) : null,
          row.status === 'disabled' ? badge(t('disabled'), 'danger') : null,
        ),
        width: '120px',
      },
      { header: t('colLastSeen'), cell: (row) => relativeTime(row.last_login_at), secondary: true, width: '110px' },
    ],
    rows: users ?? [],
    empty: t('noAccountsMatch'),
    muted: (row) => row.status === 'disabled',
    onSelect: (row) => void openUser(view, groups, row.id),
  }));
}

async function openUser(view: AdminView, groups: Group[], userID: string): Promise<void> {
  let detail;
  try {
    detail = await adminApi.user(userID);
  } catch (error) {
    window.alert(error instanceof ApiError ? error.message : String(error));
    return;
  }

  const account = detail.user;
  const policy = detail.policy.id ? detail.policy : emptyPolicy('user', userID);
  const self = currentUser()?.id === account.id;

  const nickname = textField({ label: t('nickname'), value: account.nickname, maxLength: 32 });
  const email = textField({ label: t('email'), value: account.email, type: 'email' });
  const qq = textField({ label: t('qq'), value: account.qq || '', placeholder: t('qqPlaceholder'), maxLength: 15 });
  const bio = textArea({ label: t('bio'), value: account.bio, rows: 2 });
  const avatar = textField({
    label: t('avatar'),
    value: account.avatar,
    placeholder: t('avatarPlaceholder'),
    hint: t('avatarHint'),
  });

  const role = selectField<Role>({
    label: t('role'),
    value: account.role,
    options: [{ value: 'user', label: t('roleUser') }, { value: 'admin', label: t('roleAdmin') }],
    ...(self ? { hint: t('cannotDemoteSelf') } : {}),
  });

  const status = selectField<AccountStatus>({
    label: t('status'),
    value: account.status,
    options: [{ value: 'active', label: t('statusActive') }, { value: 'disabled', label: t('statusDisabled') }],
    hint: t('disableHint'),
  });

  const group = selectField({
    label: t('group'),
    value: account.group_id,
    options: groups.map((entry) => ({ value: entry.id, label: entry.name })),
  });

  const newPassword = textField({
    label: t('setNewPassword'),
    type: 'password',
    placeholder: t('keepPassword'),
    hint: t('resetPasswordHint'),
  });

  const rpm = numberField({ label: t('requestsPerMinute'), value: policy.rpm, placeholder: t('inherit'), min: 0 });
  const windows = (['5h', '1w', '1m'] as QuotaWindowKind[]).map((kind) => {
    const limits = policy.windows[kind];
    return {
      kind,
      override: switchField({ label: t('overrideWindow', { window: kind }), value: limits?.enabled !== null && limits?.enabled !== undefined }),
      enabled: switchField({ label: t('enforceIt'), value: limits?.enabled === true }),
      requests: numberField({ label: t('limitRequests'), value: limits?.requests ?? null, placeholder: t('noLimit'), min: 0 }),
      tokens: numberField({ label: t('limitTokens'), value: limits?.tokens ?? null, placeholder: t('noLimit'), min: 0 }),
      credits: numberField({ label: t('limitCredits'), value: limits?.credits ?? null, placeholder: t('noLimit'), min: 0, step: 0.1 }),
    };
  });

  openPanel({
    host: view.host,
    title: account.nickname || account.username,
    confirmLabel: t('save'),
    width: 440,
    ...(self
      ? {}
      : {
          destructive: {
            label: t('deleteLabel'),
            confirm: t('confirmDeleteUser', { name: account.username }),
            onSelect: (handle) => removeUser(view, account, handle),
          },
        }),
    build: (body) => {
      body.appendChild(summary(account, detail.lifetime));

      body.appendChild(section(t('secProfile')));
      body.appendChild(nickname.element);
      body.appendChild(email.element);
      body.appendChild(qq.element);
      body.appendChild(bio.element);
      body.appendChild(avatar.element);

      body.appendChild(section(t('secAccess')));
      body.appendChild(role.element);
      body.appendChild(status.element);
      body.appendChild(group.element);
      body.appendChild(newPassword.element);

      body.appendChild(section(t('secAllowanceOverride'), t('allowanceOverrideHint')));
      body.appendChild(rpm.element);
      for (const window of windows) {
        body.appendChild(section(window.kind));
        body.appendChild(window.override.element);
        body.appendChild(window.enabled.element);
        body.appendChild(window.requests.element);
        body.appendChild(window.tokens.element);
        body.appendChild(window.credits.element);
      }

      body.appendChild(section(t('apiKeys'), t('adminKeysHint')));
      body.appendChild(keySection(account.id));

      body.appendChild(section(t('secConversations'), t('conversationsHint')));
      body.appendChild(button('oa-btn', t('viewConversations'), () => {
        void openConversations(view, groups, account);
      }));
    },
    onConfirm: async (handle) => {
      handle.setBusy(true);
      try {
        await adminApi.updateUser(account.id, {
          nickname: nickname.value(),
          email: email.value(),
          qq: qq.value(),
          bio: bio.value(),
          avatar: avatar.value(),
          role: role.value(),
          status: status.value(),
          group_id: group.value(),
        });

        if (newPassword.value()) {
          await adminApi.resetPassword(account.id, newPassword.value());
        }

        await adminApi.savePolicy({
          scope: 'user',
          scope_id: account.id,
          rpm: rpm.value(),
          tpm: null,
          windows: Object.fromEntries(windows.map((window) => [window.kind, window.override.value()
            ? {
                enabled: window.enabled.value(),
                requests: window.requests.value(),
                tokens: window.tokens.value(),
                credits: window.credits.value(),
              }
            : { enabled: null, requests: null, tokens: null, credits: null }])),
        });

        handle.close();
        view.reload();
      } catch (error) {
        handle.setBusy(false);
        handle.setError(error instanceof ApiError ? error.message : String(error));
      }
    },
  });
}

/**
 * The keys this account has issued.
 *
 * The list and nothing else: the server keeps a digest, so there is no token
 * to show and no endpoint that could produce one. What an operator needs here
 * is to see that a key exists and to be able to revoke it — which is the thing
 * that matters when an account is compromised or someone leaves.
 */
function keySection(userID: string): HTMLElement {
  const wrap = el('div', 'oa-keys-list');
  wrap.appendChild(el('p', 'oa-menu-empty', t('loading')));

  void adminApi.userKeys(userID)
    .then(({ keys }) => {
      clear(wrap);
      if (!keys.length) {
        wrap.appendChild(el('p', 'oa-menu-empty', t('keysEmpty')));
        return;
      }
      for (const key of keys) wrap.appendChild(keyRow(userID, key));
    })
    .catch(() => {
      clear(wrap);
      wrap.appendChild(el('p', 'oa-menu-empty', t('failed')));
    });

  return wrap;
}

function keyRow(userID: string, key: ApiKey): HTMLElement {
  const item = el('div', 'oa-key-row');

  const info = el('div', 'oa-key-info');
  const title = el('div', 'oa-key-title');
  title.appendChild(el('span', 'oa-key-name', key.name));
  if (key.expires_at > 0 && key.expires_at <= Date.now()) {
    title.appendChild(badge(t('keyExpired'), 'danger'));
  }
  info.appendChild(title);

  const meta = el('div', 'oa-key-meta');
  meta.appendChild(el('code', 'oa-key-prefix', `${key.prefix}…`));
  meta.appendChild(el('span', null, key.expires_at
    ? t('keyExpiresAt', { when: absoluteTime(key.expires_at) })
    : t('keyNoExpiry')));
  meta.appendChild(el('span', null, key.last_used_at
    ? t('keyLastUsed', { when: relativeTime(key.last_used_at) })
    : t('keyNeverUsed')));
  info.appendChild(meta);
  item.appendChild(info);

  // Revoking someone else's credential asks first, in place, the way every
  // other irreversible action in this interface does.
  const actions = el('div', 'oa-key-actions');
  actions.appendChild(confirmable(
    iconButton('oa-icon-btn danger', ICONS.trash, t('keyRevoke'), () => {}, 15),
    { label: t('keyRevokeConfirm'), title: t('keyRevoke') },
    () => {
      void adminApi.revokeUserKey(userID, key.id)
        .then(() => item.remove())
        .catch(() => item.classList.add('oa-key-row-failed'));
    },
  ));
  item.appendChild(actions);
  return item;
}

function summary(account: Account, lifetime: { requests: number; total_tokens: number; credits: number }): HTMLElement {
  const wrap = el('div', 'oa-stat-grid');
  const stat = (label: string, value: string, note?: string) => {
    const card = el('div', 'oa-stat');
    card.appendChild(el('span', 'oa-stat-label', label));
    card.appendChild(el('span', 'oa-stat-value', value));
    if (note) card.appendChild(el('span', 'oa-stat-note', note));
    wrap.appendChild(card);
  };
  stat(t('statRequests'), compactNumber(lifetime.requests), t('lifetimeAllTime'));
  stat(t('statTokens'), compactNumber(lifetime.total_tokens));
  stat(t('statCredits'), compactNumber(lifetime.credits));
  stat(t('joined'), new Date(account.created_at).toLocaleDateString(),
    account.last_login_at ? t('lastSeenAt', { when: relativeTime(account.last_login_at) }) : t('neverSignedIn'));
  return wrap;
}

// Stepping in rather than stacking: the panel replaces itself and offers a
// way back, so the layout never grows a fourth column.
async function openConversations(view: AdminView, groups: Group[], account: Account): Promise<void> {
  const panel = openPanel({
    host: view.host,
    title: t('someonesConversations', { name: account.nickname || account.username }),
    width: 440,
    cancelLabel: t('close'),
    onBack: () => void openUser(view, groups, account.id),
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', t('loading')));
    },
  });

  let conversations;
  try {
    ({ conversations } = await adminApi.userConversations(account.id));
  } catch (error) {
    panel.setError(error instanceof ApiError ? error.message : String(error));
    return;
  }

  clear(panel.body);
  if (!conversations.length) {
    panel.body.appendChild(el('p', 'oa-field-hint', t('noConversations')));
    return;
  }

  panel.body.appendChild(renderTable({
    columns: [
      { header: t('colTitle'), cell: (row) => row.title || t('untitled') },
      { header: t('colMessages'), cell: (row) => String(row.message_count), numeric: true },
      { header: t('colUpdated'), cell: (row) => relativeTime(row.updated_at) },
    ],
    rows: conversations,
    empty: t('noConversations'),
    onSelect: (row) => void openTranscript(view, groups, account, row.id, row.title),
  }));
}

async function openTranscript(
  view: AdminView,
  groups: Group[],
  account: Account,
  conversationID: string,
  title: string,
): Promise<void> {
  const panel = openPanel({
    host: view.host,
    title: title || t('conversationFallback'),
    width: 480,
    cancelLabel: t('close'),
    onBack: () => void openConversations(view, groups, account),
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', t('loading')));
    },
  });

  try {
    const { messages } = await adminApi.userTranscript(account.id, conversationID);
    clear(panel.body);

    const transcript = el('div', 'oa-transcript');
    for (const message of messages) {
      const turn = el('div', 'oa-transcript-turn');
      turn.appendChild(el('span', 'oa-transcript-role',
        `${message.role}${message.model_name ? ` · ${message.model_name}` : ''}${message.created_at ? ` · ${absoluteTime(message.created_at)}` : ''}`));
      // Plain text, not markdown: this is an audit view of what was stored,
      // and a renderer would be interpreting it.
      turn.appendChild(document.createTextNode(message.error || message.content || t('emptyMessage')));
      transcript.appendChild(turn);
    }
    panel.body.appendChild(transcript);
  } catch (error) {
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}

async function removeUser(view: AdminView, account: Account, panel: PanelHandle): Promise<void> {
  panel.setBusy(true);
  try {
    await adminApi.deleteUser(account.id);
    panel.close();
    view.reload();
  } catch (error) {
    panel.setBusy(false);
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}
