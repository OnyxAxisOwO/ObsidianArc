// The browser half of plugins/cardgrant: mass quota reset card grants to all users.

import { api } from '@/api/client';
import { IconLayers } from '@/icons';
import { absoluteTime } from '@/lib/format';
import { pluginStrings } from '../registry';
import type { ArcPlugin, ListCell } from '../types';

const s = pluginStrings({
  pageTitle: 'Mass card grant',
  pageHint: 'Grant usage reset cards in bulk to all registered users.',
  grantTitle: 'Grant reset cards to all users',
  grantHint: 'Set card name, reset scope and expiration date to grant 1 reset card to every account.',
  cardName: 'Card name',
  cardNameHint: 'The display name shown on users\' reset cards.',
  cardNamePlaceholder: 'e.g. Qwen 系列模型下架补偿',
  cardWindow: 'Reset scope',
  cardWindowHint: 'Which quota window this card resets when spent.',
  window5h: '5-hour card (5h)',
  window1w: 'Weekly card (1w)',
  window1m: 'Monthly card (1m)',
  windowFull: 'Full reset card (all windows)',
  cardExpiry: 'Expiration date',
  cardExpiryHint: 'When unused cards will expire (click quick presets below).',
  grantBtn: 'Grant to all users',
  confirmGrant: 'Are you sure you want to grant 1 "{name}" card ({window}) to all users?',
  grantSuccess: 'Successfully granted "{name}" ({window}) to {count} users!',
  historyTitle: 'Grant history',
  historyHint: 'All mass grant operations, newest first.',
  historyEmpty: 'No mass grant operations recorded yet.',
  colName: 'Card name',
  colWindow: 'Reset scope',
  colRecipients: 'Recipients',
  colExpiry: 'Expires at',
  colCreatedAt: 'Granted at',
  colOperator: 'Operator',
  usersCount: '{count} users',
  defaultCardName: 'Usage reset card',
}, {
  pageTitle: '全员发卡',
  pageHint: '向全体注册用户批量发放用量重置卡。',
  grantTitle: '全员发放重置卡',
  grantHint: '设置卡片名称、重置范围与截止日期，一键向所有账户各发 1 张重置卡。',
  cardName: '卡片名称',
  cardNameHint: '显示在用户重置卡列表中的名称。',
  cardNamePlaceholder: '例如 Qwen 系列模型下架补偿',
  cardWindow: '重置范围',
  cardWindowHint: '使用该卡片时重置哪一个用量周期窗口。',
  window5h: '5 小时卡 (5h)',
  window1w: '周卡 (1w)',
  window1m: '月卡 (1m)',
  windowFull: '完全重置卡 (全部窗口)',
  cardExpiry: '截止日期',
  cardExpiryHint: '卡片未使用的过期时间（可点击下方快速预设）。',
  grantBtn: '发放给所有人',
  confirmGrant: '确定向全员发放【{name}】（{window}）重置卡？',
  grantSuccess: '已成功向 {count} 位用户发放【{name}】（{window}）重置卡！',
  historyTitle: '发放记录',
  historyHint: '所有全员发卡记录，按时间倒序。',
  historyEmpty: '暂无全员发卡记录。',
  colName: '卡片名称',
  colWindow: '重置范围',
  colRecipients: '发放人数',
  colExpiry: '截止日期',
  colCreatedAt: '发放时间',
  colOperator: '操作人',
  usersCount: '{count} 人',
  defaultCardName: '用量重置卡',
});

function resolveWindowLabel(win: string): string {
  switch (win) {
    case '5h':
      return s('window5h');
    case '1w':
      return s('window1w');
    case '1m':
      return s('window1m');
    default:
      return s('windowFull');
  }
}

interface GrantResponse {
  granted: number;
  name: string;
  window: string;
  expires_at: number;
}

interface GrantHistoryItem {
  id: string;
  name: string;
  windows: string;
  expires_at: number;
  recipient_count: number;
  actor_id: string;
  actor_username: string;
  created_at: number;
}

const plugin: ArcPlugin = {
  name: 'cardgrant',
  icon: IconLayers,
  adminPages: [{
    slug: 'cardgrant',
    title: () => s('pageTitle'),
    hint: () => s('pageHint'),
    icon: IconLayers,
    permission: 'users',
    keywords: ['发卡', '重置卡', '全员发卡', 'card', 'grant', 'reset card'],
  }],
  actionCards: [{
    id: 'grant-action',
    page: 'plugin:cardgrant',
    title: () => s('grantTitle'),
    hint: () => s('grantHint'),
    icon: IconLayers,
    controls: [
      {
        kind: 'text',
        key: 'name',
        label: () => s('cardName'),
        hint: () => s('cardNameHint'),
        placeholder: s('cardNamePlaceholder'),
        required: true,
      },
      {
        kind: 'select',
        key: 'window',
        label: () => s('cardWindow'),
        hint: () => s('cardWindowHint'),
        options: [
          { value: '5h', label: () => s('window5h') },
          { value: '1w', label: () => s('window1w') },
          { value: '1m', label: () => s('window1m') },
          { value: 'full', label: () => s('windowFull') },
        ],
      },
      {
        kind: 'datetime',
        key: 'expires_at',
        label: () => s('cardExpiry'),
        hint: () => s('cardExpiryHint'),
        presets: true,
        required: true,
      },
    ],
    defaults: {
      name: '',
      window: '5h',
    },
    button: {
      label: () => s('grantBtn'),
      confirm: (draft) => {
        const name = draft['name']?.trim() || s('defaultCardName');
        const win = resolveWindowLabel(draft['window'] || '5h');
        return s('confirmGrant', { name, window: win });
      },
      run: async (draft) => {
        const name = draft['name']?.trim() || '';
        const window = draft['window'] || '5h';
        const dateStr = draft['expires_at'];
        const expiresAt = dateStr ? new Date(dateStr).getTime() : 0;
        const res = await api.post<GrantResponse>('/api/admin/cardgrant/grant', {
          name,
          window,
          expires_at: expiresAt,
        });
        return s('grantSuccess', {
          count: res.granted,
          name: res.name || s('defaultCardName'),
          window: resolveWindowLabel(res.window),
        });
      },
    },
  }],
  lists: [{
    id: 'history',
    page: 'plugin:cardgrant',
    title: () => s('historyTitle'),
    hint: () => s('historyHint'),
    icon: IconLayers,
    empty: () => s('historyEmpty'),
    columns: [
      { key: 'name', header: () => s('colName') },
      { key: 'window', header: () => s('colWindow'), width: '160px' },
      { key: 'recipients', header: () => s('colRecipients'), width: '120px' },
      { key: 'expires_at', header: () => s('colExpiry'), width: '180px' },
      { key: 'created_at', header: () => s('colCreatedAt'), width: '180px' },
      { key: 'operator', header: () => s('colOperator'), width: '140px' },
    ],
    load: async (offset: number, limit: number) => {
      const res = await api.get<{ grants: GrantHistoryItem[]; total: number }>(
        `/api/admin/cardgrant/grants?offset=${offset}&limit=${limit}`,
      );
      return {
        rows: res.grants as unknown as Array<Record<string, unknown>>,
        total: res.total,
      };
    },
    cell: (key: string, row: Record<string, unknown>): ListCell => {
      switch (key) {
        case 'name':
          return { title: String(row['name'] || s('defaultCardName')) };
        case 'window': {
          const win = String(row['windows'] || '');
          const label = resolveWindowLabel(win);
          return {
            title: label,
            badge: { tone: win === 'full' || win === '' ? 'muted' : 'default' },
          };
        }
        case 'recipients':
          return { title: s('usersCount', { count: Number(row['recipient_count'] || 0) }) };
        case 'expires_at':
          return { title: absoluteTime(Number(row['expires_at'])) };
        case 'created_at':
          return { title: absoluteTime(Number(row['created_at'])) };
        case 'operator':
          return {
            title: String(row['actor_username'] || row['actor_id'] || ''),
            mask: true,
          };
        default:
          return { title: '' };
      }
    },
  }],
};

export default plugin;
