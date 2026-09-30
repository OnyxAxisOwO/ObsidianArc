// A plugin as the core's tests need one: a declaration of every kind of thing
// the core knows how to draw for a plugin, standing for no real feature.
//
// The core ships no plugins, but its registry, its forms and its backoffice
// pages exist for them, and a renderer nobody feeds is a renderer nothing
// tests. Each real plugin's own tests live with it; these are for the
// mechanism, so the words and the endpoints are made up and stay that way.

import { api } from '@/api/client';
import { IconKey, IconUsers } from '@/icons';
import { pluginStrings } from '@/plugins/registry';
import type { ArcPlugin, GuardAction, PluginConfig } from '@/plugins/types';

const s = pluginStrings({
  ref: 'Reference',
  refOptional: 'Reference (optional)',
  refPlaceholder: 'e.g. 10001',
  refRequired: 'A reference number is required to register here.',
  refInvalid: 'Reference must be 5–15 digits.',
  refTaken: 'That reference is already registered.',
  oauthRefRequired: 'This server requires a reference number, which a provider sign-in cannot supply.',
  pinned: 'This connection cannot be removed — it is what proves the account\'s reference.',
  pinnedHint: 'Bound for the life of the account.',
  blocked: 'This request was rejected by the example check.',
  processed: 'Already processed.',
  checkMode: 'Example check only',
  checking: 'Running the check…',
  failed: 'The check did not pass.',
  title: 'Example check',
  hint: 'A check the browser runs before an account is created.',
  baseURL: 'Service address',
  site: 'Site key',
  secret: 'Site secret',
  onLogin: 'Check on sign-in',
  pageTitle: 'Example',
  pageHint: 'A page the plugin brings.',
  groupTitle: 'Example group',
  requirement: 'Reference',
  requirementOff: 'Not required',
  requirementOptional: 'Optional',
  requirementRequired: 'Required',
  token: 'Example webhook token',
  mode: 'Default mode',
  modeDisable: 'Disabled',
  modeDelete: 'Deleted',
  listTitle: 'Processed records',
  listEmpty: 'No records yet.',
  colMember: 'Member',
  colMode: 'Mode',
  notifyTitle: 'A member left',
  notifyBody: '{username} left. {count} item(s) taken back.',
  noteLeft: 'Left · account removed',
  actionTitle: 'Process a member',
  actionHint: 'Ends the account.',
  disableLabel: 'Disable account',
  disableConfirm: 'Disable {name}?',
  deleteLabel: 'Delete account',
  deleteConfirm: 'Delete {name} permanently?',
  done: 'Processed · {due} due, {taken} taken back.',
}, {
  ref: '编号',
  refOptional: '编号（选填）',
  refPlaceholder: '例如 10001',
  refRequired: '这台服务器要求填写编号。',
  refInvalid: '编号应为 5–15 位数字。',
  refTaken: '该编号已被注册。',
  oauthRefRequired: '这台服务器要求填写编号，第三方登录提供不了。',
  pinned: '这个连接无法解绑。',
  pinnedHint: '账户存续期间保持绑定。',
  blocked: '本次请求被示例检查拒绝。',
  processed: '已经处理过。',
  checkMode: '仅示例检查',
  checking: '正在检查…',
  failed: '检查未通过。',
  title: '示例检查',
  hint: '创建账户前由浏览器运行的检查。',
  baseURL: '服务地址',
  site: '站点标识',
  secret: '站点密钥',
  onLogin: '登录时检查',
  pageTitle: '示例',
  pageHint: '插件自带的页面。',
  groupTitle: '示例分组',
  requirement: '编号',
  requirementOff: '不要求',
  requirementOptional: '选填',
  requirementRequired: '必填',
  token: '示例 Webhook 令牌',
  mode: '默认方式',
  modeDisable: '封禁',
  modeDelete: '删除',
  listTitle: '处理记录',
  listEmpty: '暂无记录。',
  colMember: '成员',
  colMode: '方式',
  notifyTitle: '有成员离开',
  notifyBody: '{username} 已离开，收回 {count} 项。',
  noteLeft: '已离开 · 账号已删除',
  actionTitle: '处理成员',
  actionHint: '结束该账户。',
  disableLabel: '封禁账号',
  disableConfirm: '封禁 {name}？',
  deleteLabel: '删除账号',
  deleteConfirm: '永久删除 {name}？',
  done: '处理完成 · 应收回 {due}，实际收回 {taken}。',
});

/** What an in-page check service leaves on window, as the fixture's guard drives it. */
export interface ExampleCheck {
  init(options: { site: string }): void;
  execute(action: string, form?: HTMLFormElement): Promise<string>;
}

declare global {
  interface Window {
    ExampleCheck?: ExampleCheck;
  }
}

const str = (value: unknown): string => (typeof value === 'string' ? value : '');
const num = (value: unknown): number => (typeof value === 'number' ? value : 0);

function guarded(action: GuardAction, config: PluginConfig): boolean {
  return action === 'register' ? config['on_signup'] === true : config['on_login'] === true;
}

async function process(userId: string, mode: 'disable' | 'delete'): Promise<string> {
  const { result } = await api.post<{ result: { due: number; taken: number } }>(
    `/api/admin/users/${encodeURIComponent(userId)}/process`, { mode, note: '' });
  return s('done', { due: result.due, taken: result.taken });
}

const plugin: ArcPlugin = {
  name: 'example',
  icon: IconUsers,
  adminPages: [{
    slug: 'example',
    title: () => s('pageTitle'),
    hint: () => s('pageHint'),
    icon: IconUsers,
    permission: 'security,invites',
    keywords: ['example'],
  }],
  fields: {
    ref: {
      label: () => s('ref'),
      optionalLabel: () => s('refOptional'),
      placeholder: () => s('refPlaceholder'),
      maxLength: 15,
      inputMode: 'numeric',
      pattern: /^[1-9][0-9]{4,14}$/,
      invalid: () => s('refInvalid'),
      required: () => s('refRequired'),
      taken: () => s('refTaken'),
    },
  },
  oauthErrors: { ref_required: () => s('oauthRefRequired') },
  refusals: {
    example_blocked: () => s('blocked'),
    already_processed: () => s('processed'),
  },
  pinnedProviders: { oidc: { hint: () => s('pinnedHint'), refused: () => s('pinned') } },
  notifications: {
    example_departed: (params) => ({
      title: s('notifyTitle'),
      body: s('notifyBody', { username: str(params['username']), count: num(params['count']) }),
    }),
  },
  inviteeNote: (invitee) => (invitee['flagged'] === true ? s('noteLeft') : null),
  guards: [{
    name: 'example',
    active: (action, config) => guarded(action, config) && !!str(config['site']),
    prepare: (_action, config) => { window.ExampleCheck?.init({ site: str(config['site']) }); },
    async token(action, config, form) {
      const check = window.ExampleCheck;
      if (!check) throw new Error('the check could not be loaded');
      check.init({ site: str(config['site']) });
      return check.execute(action, form);
    },
    checking: () => s('checking'),
    failed: () => s('failed'),
  }],
  captchaModes: [{ value: 'example', label: () => s('checkMode') }],
  settings: [
    {
      id: 'secExample',
      page: 'security',
      category: 'verification',
      column: 0,
      title: () => s('title'),
      hint: () => s('hint'),
      icon: IconKey,
      defaults: { 'example.on_login': 'false' },
      controls: [
        { kind: 'text', key: 'example.base_url', label: () => s('baseURL') },
        { kind: 'text', key: 'example.site', label: () => s('site') },
        { kind: 'secret', key: 'example.secret_key', label: () => s('secret'), placeholder: '••••••••' },
        { kind: 'switch', key: 'example.on_login', label: () => s('onLogin') },
      ],
    },
    {
      id: 'secExampleGroup',
      page: 'plugin:example',
      category: 'accounts',
      column: 0,
      title: () => s('groupTitle'),
      icon: IconUsers,
      defaults: { 'example.requirement': 'off', 'example.mode': 'disable' },
      controls: [
        {
          kind: 'select', key: 'example.requirement', label: () => s('requirement'),
          options: [
            { value: 'off', label: () => s('requirementOff') },
            { value: 'optional', label: () => s('requirementOptional') },
            { value: 'required', label: () => s('requirementRequired') },
          ],
        },
        { kind: 'secret', key: 'example.webhook_token', label: () => s('token'), placeholder: '••••••••' },
        {
          kind: 'select', key: 'example.mode', label: () => s('mode'),
          options: [
            { value: 'disable', label: () => s('modeDisable') },
            { value: 'delete', label: () => s('modeDelete') },
          ],
        },
      ],
    },
  ],
  lists: [{
    id: 'secExampleRecords',
    page: 'plugin:example',
    title: () => s('listTitle'),
    empty: () => s('listEmpty'),
    icon: IconUsers,
    columns: [
      { key: 'member', header: () => s('colMember') },
      { key: 'mode', header: () => s('colMode'), width: '100px' },
    ],
    async load(offset, limit) {
      const result = await api.get<{ records: Array<Record<string, unknown>>; total: number }>(
        `/api/admin/example/records?limit=${limit}&offset=${offset}`);
      return { rows: result.records ?? [], total: result.total };
    },
    cell(key, row) {
      switch (key) {
        case 'member':
          return { title: str(row['username']), mask: true };
        case 'mode':
          return row['mode'] === 'delete'
            ? { title: s('modeDelete'), badge: { tone: 'danger' } }
            : { title: s('modeDisable'), badge: { tone: 'muted' } };
        default:
          return { title: '' };
      }
    },
  }],
  userActions: [{
    id: 'process',
    title: () => s('actionTitle'),
    hint: () => s('actionHint'),
    buttons: [
      {
        label: () => s('disableLabel'),
        confirm: (name) => s('disableConfirm', { name }),
        run: (userId) => process(userId, 'disable'),
      },
      {
        label: () => s('deleteLabel'),
        confirm: (name) => s('deleteConfirm', { name }),
        danger: true,
        closesPanel: true,
        run: (userId) => process(userId, 'delete'),
      },
    ],
  }],
};

export default plugin;
