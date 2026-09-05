// Users: search, edit, disable, reset, and read their conversations.
//
// That last one is an intrusion even when it is justified, so it sits behind
// its own click, says whose transcript it is, and leaves a line in the server
// log. Nothing about it is incidental to opening the account panel.

import { ApiError } from '../api/client';
import { button, clear, el } from '../ui/dom';
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
  view.setTitle('Users');

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
  search.placeholder = 'Search name, nickname or email';
  search.value = state.q;

  const roleSelect = filterSelect([
    { value: '', label: 'Any role' },
    { value: 'user', label: 'Users' },
    { value: 'admin', label: 'Administrators' },
  ], state.role);

  const statusSelect = filterSelect([
    { value: '', label: 'Any status' },
    { value: 'active', label: 'Active' },
    { value: 'disabled', label: 'Disabled' },
  ], state.status);

  const groupSelect = filterSelect([
    { value: '', label: 'Any group' },
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
  target.appendChild(el('p', 'oa-table-empty', 'Loading…'));

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
  view.setTitle('Users', `${total} account${total === 1 ? '' : 's'}`);
  target.appendChild(renderTable({
    columns: [
      { header: 'Account', cell: (row) => stacked(row.nickname || row.username, `@${row.username}`) },
      { header: 'Email', cell: (row) => row.email || '—', secondary: true },
      { header: 'Group', cell: (row) => groupName(row.group_id) },
      {
        header: 'Role',
        cell: (row) => badges(
          row.role === 'admin' ? badge('admin') : null,
          row.status === 'disabled' ? badge('disabled', 'danger') : null,
        ),
      },
      { header: 'Last seen', cell: (row) => relativeTime(row.last_login_at), secondary: true },
    ],
    rows: users ?? [],
    empty: 'No accounts match.',
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

  const nickname = textField({ label: 'Nickname', value: account.nickname, maxLength: 32 });
  const email = textField({ label: 'Email', value: account.email, type: 'email' });
  const bio = textArea({ label: 'Bio', value: account.bio, rows: 2 });
  const avatar = textField({
    label: 'Avatar',
    value: account.avatar,
    placeholder: '/uploads/… or a data: URI',
    hint: 'A same-origin path or an inline image.',
  });

  const role = selectField<Role>({
    label: 'Role',
    value: account.role,
    options: [{ value: 'user', label: 'User' }, { value: 'admin', label: 'Administrator' }],
    ...(self ? { hint: 'You cannot demote yourself while you are the last administrator.' } : {}),
  });

  const status = selectField<AccountStatus>({
    label: 'Status',
    value: account.status,
    options: [{ value: 'active', label: 'Active' }, { value: 'disabled', label: 'Disabled' }],
    hint: 'Disabling signs the account out everywhere, immediately.',
  });

  const group = selectField({
    label: 'Group',
    value: account.group_id,
    options: groups.map((entry) => ({ value: entry.id, label: entry.name })),
  });

  const newPassword = textField({
    label: 'Set a new password',
    type: 'password',
    placeholder: 'Leave empty to keep it',
    hint: 'Setting one signs the account out everywhere.',
  });

  const rpm = numberField({ label: 'Requests per minute', value: policy.rpm, placeholder: 'inherit', min: 0 });
  const windows = (['5h', '1w', '1m'] as QuotaWindowKind[]).map((kind) => {
    const limits = policy.windows[kind];
    return {
      kind,
      override: switchField({ label: `Override the ${kind} window`, value: limits?.enabled !== null && limits?.enabled !== undefined }),
      enabled: switchField({ label: 'Enforce it', value: limits?.enabled === true }),
      requests: numberField({ label: 'Requests', value: limits?.requests ?? null, placeholder: 'no limit', min: 0 }),
      tokens: numberField({ label: 'Tokens', value: limits?.tokens ?? null, placeholder: 'no limit', min: 0 }),
      credits: numberField({ label: 'Credits', value: limits?.credits ?? null, placeholder: 'no limit', min: 0, step: 0.1 }),
    };
  });

  openPanel({
    host: view.host,
    title: account.nickname || account.username,
    confirmLabel: 'Save',
    width: 440,
    ...(self ? {} : { destructive: { label: 'Delete', onSelect: (handle) => removeUser(view, account, handle) } }),
    build: (body) => {
      body.appendChild(summary(account, detail.lifetime));

      body.appendChild(section('Profile'));
      body.appendChild(nickname.element);
      body.appendChild(email.element);
      body.appendChild(bio.element);
      body.appendChild(avatar.element);

      body.appendChild(section('Access'));
      body.appendChild(role.element);
      body.appendChild(status.element);
      body.appendChild(group.element);
      body.appendChild(newPassword.element);

      body.appendChild(section('Allowance override',
        'Anything set here beats the group. Leave a window un-overridden to inherit it.'));
      body.appendChild(rpm.element);
      for (const window of windows) {
        body.appendChild(section(window.kind));
        body.appendChild(window.override.element);
        body.appendChild(window.enabled.element);
        body.appendChild(window.requests.element);
        body.appendChild(window.tokens.element);
        body.appendChild(window.credits.element);
      }

      body.appendChild(section('Conversations',
        'Reading someone else’s messages is recorded in the server log.'));
      body.appendChild(button('oa-btn', 'View conversations', () => {
        void openConversations(view, groups, account);
      }));
    },
    onConfirm: async (handle) => {
      handle.setBusy(true);
      try {
        await adminApi.updateUser(account.id, {
          nickname: nickname.value(),
          email: email.value(),
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

function summary(account: Account, lifetime: { requests: number; total_tokens: number; credits: number }): HTMLElement {
  const wrap = el('div', 'oa-stat-grid');
  const stat = (label: string, value: string, note?: string) => {
    const card = el('div', 'oa-stat');
    card.appendChild(el('span', 'oa-stat-label', label));
    card.appendChild(el('span', 'oa-stat-value', value));
    if (note) card.appendChild(el('span', 'oa-stat-note', note));
    wrap.appendChild(card);
  };
  stat('Requests', compactNumber(lifetime.requests), 'all time');
  stat('Tokens', compactNumber(lifetime.total_tokens));
  stat('Credits', compactNumber(lifetime.credits));
  stat('Joined', new Date(account.created_at).toLocaleDateString(),
    account.last_login_at ? `last seen ${relativeTime(account.last_login_at)}` : 'never signed in');
  return wrap;
}

// Stepping in rather than stacking: the panel replaces itself and offers a
// way back, so the layout never grows a fourth column.
async function openConversations(view: AdminView, groups: Group[], account: Account): Promise<void> {
  const panel = openPanel({
    host: view.host,
    title: `${account.nickname || account.username}'s conversations`,
    width: 440,
    cancelLabel: 'Close',
    onBack: () => void openUser(view, groups, account.id),
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', 'Loading…'));
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
    panel.body.appendChild(el('p', 'oa-field-hint', 'No conversations.'));
    return;
  }

  panel.body.appendChild(renderTable({
    columns: [
      { header: 'Title', cell: (row) => row.title || 'Untitled' },
      { header: 'Messages', cell: (row) => String(row.message_count), numeric: true },
      { header: 'Updated', cell: (row) => relativeTime(row.updated_at) },
    ],
    rows: conversations,
    empty: 'No conversations.',
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
    title: title || 'Conversation',
    width: 480,
    cancelLabel: 'Close',
    onBack: () => void openConversations(view, groups, account),
    build: (body) => {
      body.appendChild(el('p', 'oa-field-hint', 'Loading…'));
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
      turn.appendChild(document.createTextNode(message.error || message.content || '(empty)'));
      transcript.appendChild(turn);
    }
    panel.body.appendChild(transcript);
  } catch (error) {
    panel.setError(error instanceof ApiError ? error.message : String(error));
  }
}

async function removeUser(view: AdminView, account: Account, panel: PanelHandle): Promise<void> {
  if (!window.confirm(`Delete ${account.username}? Their conversations and usage records go too.`)) return;
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
