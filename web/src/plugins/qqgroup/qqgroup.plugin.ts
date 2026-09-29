// The browser's half of plugins/qqgroup: the QQ number as an account field,
// the settings for it and for the group bot, the backoffice's departure
// action and audit list, and the words for what a departure leaves behind.

import { api } from '@/api/client';
import { IconUsers } from '@/icons';
import { absoluteTime } from '@/lib/format';
import { pluginStrings } from '../registry';
import type { ArcPlugin } from '../types';

const s = pluginStrings({
  qq: 'QQ',
  qqOptional: 'QQ (optional)',
  qqPlaceholder: 'e.g. 10001',
  qqRequired: 'A QQ number is required to register here.',
  qqInvalid: 'QQ number must be 5–15 digits.',
  qqTaken: 'That QQ number is already registered.',
  oauthQQRequired: 'This server requires a QQ number, which a provider sign-in cannot supply. Register the usual way.',
  pinned: 'An OpenID Connect connection cannot be removed — it is what proves this account\'s QQ number.',
  pinnedHint: 'Bound for the life of the account — it proves the QQ number.',
  settingsTitle: 'QQ group',
  pageTitle: 'QQ group',
  pageHint: 'The QQ number on accounts, the group bot, and every processed departure.',
  settingsHint: 'Members carry their QQ number, and a member leaving the group ends their account.',
  requirement: 'QQ number',
  requirementHint: 'Whether new accounts must provide a QQ number upon registration.',
  requirementOff: 'Not required',
  requirementOptional: 'Optional',
  requirementRequired: 'Required',
  botToken: 'QQ bot webhook token',
  botTokenHint: 'The bearer token POST /api/bot/departure expects. Empty switches the endpoint off.',
  botMode: 'Bot departure mode',
  botModeHint: 'What a bot-reported departure does when the event itself does not say.',
  modeDisable: 'Disabled',
  modeDelete: 'Deleted',
  inviteeDeparted: 'Left the group',
  inviteeDepartedDeleted: 'Left the group · account removed',
  inviteeCardsOne: '{cards} reset card taken back',
  inviteeCardsOther: '{cards} reset cards taken back',
  notifyTitle: 'Invitee left the group',
  notifyCardsOne: '{username} left the group. {cards} reset card was taken back.',
  notifyCardsOther: '{username} left the group. {cards} reset cards were taken back.',
  notifySpent: '{username} left the group. Their reward cards had already been spent.',
  notifyPlain: '{username} left the group.',
  departureTitle: 'Group departure',
  departureHint: 'The member has left the QQ group. This ends the account and takes back the reset cards this invite earned.',
  disableLabel: 'Disable account, take cards back',
  disableConfirm: 'Disable {name}? Their sessions end now, and the cards this invite earned are taken back.',
  deleteLabel: 'Delete account, take cards back',
  deleteConfirm: 'Delete {name} permanently? Everything they own is removed, and the cards this invite earned are taken back.',
  departureDone: 'Departure processed · {due} card(s) due, {revoked} taken back.',
  listTitle: 'Group departures',
  listHint: 'Every processed departure, newest first — who left, whose invite it was, and what was clawed back.',
  listEmpty: 'No departures processed yet.',
  colMember: 'Member',
  colInviter: 'Inviter',
  colMode: 'Mode',
  colCards: 'Cards due / taken back',
  colSource: 'Processed by',
  inviterGone: 'Account removed',
  sourceAdmin: 'Backoffice',
  sourceBot: 'QQ bot',
  eventDeparture: 'Group departure',
  alreadyDeparted: 'This account has already been processed for a departure.',
  departureRefused: 'An administrator account can only be processed by an administrator, and never by the bot.',
}, {
  qq: 'QQ 号',
  qqOptional: 'QQ 号（选填）',
  qqPlaceholder: '例如 10001',
  qqRequired: '这台服务器要求填写 QQ 号。',
  qqInvalid: 'QQ 号格式不正确，应为 5–15 位数字。',
  qqTaken: '该 QQ 号已被注册。',
  oauthQQRequired: '这台服务器要求填写 QQ 号，第三方登录提供不了，请用常规方式注册。',
  pinned: 'OIDC 连接无法解绑——它就是这个账户 QQ 号的凭证。',
  pinnedHint: '账户存续期间保持绑定——它是 QQ 号的凭证。',
  settingsTitle: 'QQ 群',
  pageTitle: 'QQ 群',
  pageHint: '账户上的 QQ 号、群机器人，以及所有已处理的退群。',
  settingsHint: '成员账户带有 QQ 号；成员退群时结束其账户。',
  requirement: 'QQ 号',
  requirementHint: '新账户注册时是否必须填写 QQ 号。',
  requirementOff: '不要求',
  requirementOptional: '选填',
  requirementRequired: '必填',
  botToken: 'QQ 机器人 Webhook 令牌',
  botTokenHint: 'POST /api/bot/departure 所需的 Bearer 令牌。留空表示关闭该接口。',
  botMode: '机器人退群默认方式',
  botModeHint: '机器人上报的退群事件未指定方式时，按此方式处理。',
  modeDisable: '封禁',
  modeDelete: '删除',
  inviteeDeparted: '已退群',
  inviteeDepartedDeleted: '已退群 · 账号已删除',
  inviteeCardsOne: '收回 {cards} 张重置卡',
  inviteeCardsOther: '收回 {cards} 张重置卡',
  notifyTitle: '邀请的人已退群',
  notifyCardsOne: '{username} 已退群，收回 {cards} 张重置卡。',
  notifyCardsOther: '{username} 已退群，收回 {cards} 张重置卡。',
  notifySpent: '{username} 已退群；奖励卡已被用掉，无法收回。',
  notifyPlain: '{username} 已退群。',
  departureTitle: '退群处理',
  departureHint: '该成员已退出 QQ 群。此操作会结束其账号，并收回这次邀请发放的重置卡。',
  disableLabel: '封禁账号并收回卡片',
  disableConfirm: '封禁 {name}？其会话将立即失效，且这次邀请发放的重置卡会被收回。',
  deleteLabel: '删除账号并收回卡片',
  deleteConfirm: '永久删除 {name}？其全部数据将被移除，且这次邀请发放的重置卡会被收回。',
  departureDone: '退群处理完成 · 应收回 {due} 张，实际收回 {revoked} 张。',
  listTitle: '退群记录',
  listHint: '所有已处理的退群，按时间倒序——谁退了群、来自谁的邀请、收回了多少。',
  listEmpty: '暂无退群记录。',
  colMember: '成员',
  colInviter: '邀请人',
  colMode: '处理方式',
  colCards: '应收回 / 实收回',
  colSource: '处理方',
  inviterGone: '账号已删除',
  sourceAdmin: '后台操作',
  sourceBot: 'QQ 机器人',
  eventDeparture: '退群处理',
  alreadyDeparted: '该账户已经处理过退群。',
  departureRefused: '管理员账户只能由管理员处理，机器人无权处理。',
});

const str = (value: unknown): string => (typeof value === 'string' ? value : '');
const num = (value: unknown): number => (typeof value === 'number' ? value : 0);

/** Two keys for one and many, the way tn() picks in the core dictionary. */
function cards(count: number, one: 'inviteeCardsOne' | 'notifyCardsOne', other: 'inviteeCardsOther' | 'notifyCardsOther', vars: Record<string, string | number>): string {
  return s(count === 1 ? one : other, vars);
}

interface Departure {
  cards_due: number;
  cards_revoked: number;
}

async function depart(userId: string, mode: 'disable' | 'delete'): Promise<string> {
  const { departure } = await api.post<{ departure: Departure }>(
    `/api/admin/users/${encodeURIComponent(userId)}/departure`, { mode, note: '' });
  return s('departureDone', { due: departure.cards_due, revoked: departure.cards_revoked });
}

const plugin: ArcPlugin = {
  name: 'qqgroup',
  icon: IconUsers,
  // Its own page rather than a card on the security page and a list on the
  // invites one: everything this plugin does is one operator's concern, and
  // it now reads as one place — which leaves when the plugin is switched off.
  // Either grant opens it; the server still gives each only its own keys and
  // its own list, as it did when the two lived apart.
  adminPages: [{
    slug: 'qqgroup',
    title: () => s('pageTitle'),
    hint: () => s('pageHint'),
    icon: IconUsers,
    permission: 'security,invites',
    keywords: ['QQ', 'QQ群', '退群', 'group departure'],
  }],
  fields: {
    qq: {
      label: () => s('qq'),
      optionalLabel: () => s('qqOptional'),
      placeholder: () => s('qqPlaceholder'),
      maxLength: 15,
      inputMode: 'numeric',
      pattern: /^[1-9][0-9]{4,14}$/,
      invalid: () => s('qqInvalid'),
      required: () => s('qqRequired'),
      taken: () => s('qqTaken'),
    },
  },
  oauthErrors: { qq_required: () => s('oauthQQRequired') },
  refusals: {
    already_departed: () => s('alreadyDeparted'),
    departure_refused: () => s('departureRefused'),
  },
  // The community sign-in's subject is the member's QQ number, so the
  // connection is its proof and stays for the life of the account.
  pinnedProviders: { oidc: { hint: () => s('pinnedHint'), refused: () => s('pinned') } },
  securityEvents: { account_departure: () => s('eventDeparture') },
  notifications: {
    // Three shapes, by what the claw-back managed: some cards came back,
    // none did because the inviter had already spent them, or there was
    // never a reward on this invite at all.
    invite_departed: (params) => {
      const username = str(params['username']);
      const taken = num(params['cards_revoked']);
      const due = num(params['cards_due']);
      const body = taken > 0
        ? cards(taken, 'notifyCardsOne', 'notifyCardsOther', { username, cards: taken })
        : due > 0 ? s('notifySpent', { username }) : s('notifyPlain', { username });
      return { title: s('notifyTitle'), body };
    },
  },
  // What the inviter sees about an invitee who left: how it ended, and what
  // was taken back.
  inviteeNote: (invitee) => {
    if (invitee['departed'] !== true) return null;
    const base = invitee['departure_mode'] === 'delete' ? s('inviteeDepartedDeleted') : s('inviteeDeparted');
    const taken = num(invitee['cards_revoked']);
    return taken > 0 ? `${base} · ${cards(taken, 'inviteeCardsOne', 'inviteeCardsOther', { cards: taken })}` : base;
  },
  settings: [{
    id: 'secQQGroup',
    page: 'plugin:qqgroup',
    category: 'accounts',
    column: 0,
    title: () => s('settingsTitle'),
    hint: () => s('settingsHint'),
    icon: IconUsers,
    keywords: ['QQ号验证', '退群', 'QQ机器人', 'bot webhook', 'group departure', 'qq number'],
    defaults: { 'registration.qq_requirement': 'off', 'bot.departure_mode': 'disable' },
    controls: [
      {
        kind: 'select', key: 'registration.qq_requirement', label: () => s('requirement'), hint: () => s('requirementHint'),
        options: [
          { value: 'off', label: () => s('requirementOff') },
          { value: 'optional', label: () => s('requirementOptional') },
          { value: 'required', label: () => s('requirementRequired') },
        ],
      },
      { kind: 'secret', key: 'bot.webhook_token', label: () => s('botToken'), hint: () => s('botTokenHint'), placeholder: '••••••••' },
      {
        kind: 'select', key: 'bot.departure_mode', label: () => s('botMode'), hint: () => s('botModeHint'),
        options: [
          { value: 'disable', label: () => s('modeDisable') },
          { value: 'delete', label: () => s('modeDelete') },
        ],
      },
    ],
  }],
  lists: [{
    id: 'secDepartures',
    page: 'plugin:qqgroup',
    title: () => s('listTitle'),
    hint: () => s('listHint'),
    empty: () => s('listEmpty'),
    icon: IconUsers,
    keywords: ['退群', '退群记录', '收回重置卡', 'QQ机器人', 'group departure', 'claw back'],
    columns: [
      { key: 'member', header: () => s('colMember') },
      { key: 'inviter', header: () => s('colInviter') },
      { key: 'mode', header: () => s('colMode'), width: '100px' },
      { key: 'cards', header: () => s('colCards'), secondary: true, width: '130px' },
      { key: 'source', header: () => s('colSource'), secondary: true, width: '110px' },
    ],
    async load(offset, limit) {
      const result = await api.get<{ departures: Array<Record<string, unknown>>; total: number }>(
        `/api/admin/departures?limit=${limit}&offset=${offset}`);
      return { rows: result.departures ?? [], total: result.total };
    },
    cell(key, row) {
      switch (key) {
        case 'member': {
          const qq = str(row['qq']);
          return { title: str(row['username']), ...(qq ? { sub: qq } : {}), mask: true };
        }
        case 'inviter': {
          const name = str(row['inviter_name']);
          return name ? { title: name, sub: `@${name}`, mask: true } : { title: s('inviterGone') };
        }
        case 'mode':
          return row['mode'] === 'delete'
            ? { title: s('modeDelete'), badge: { tone: 'danger' } }
            : { title: s('modeDisable'), badge: { tone: 'muted' } };
        case 'cards':
          return { title: `${num(row['reward_cards_due'])} / ${num(row['cards_revoked'])}` };
        case 'source': {
          const at = num(row['created_at']);
          return {
            title: row['source'] === 'bot' ? s('sourceBot') : s('sourceAdmin'),
            ...(at ? { sub: absoluteTime(at) } : {}),
          };
        }
        default:
          return { title: '' };
      }
    },
  }],
  userActions: [{
    id: 'departure',
    title: () => s('departureTitle'),
    hint: () => s('departureHint'),
    // Two buttons because the two ends are different decisions, not two
    // flavours of one: disable is reversible and holds the QQ number against
    // a quick re-registration, delete is not and does not.
    buttons: [
      {
        label: () => s('disableLabel'),
        confirm: (name) => s('disableConfirm', { name }),
        run: (userId) => depart(userId, 'disable'),
      },
      {
        label: () => s('deleteLabel'),
        confirm: (name) => s('deleteConfirm', { name }),
        danger: true,
        closesPanel: true,
        run: (userId) => depart(userId, 'delete'),
      },
    ],
  }],
};

export default plugin;
