// The browser's half of plugins/riskcontrol: the service's SDK in front of
// sign-up and sign-in, the settings that point the server at it, policy controls,
// and the verification log audit table.

import { api } from '@/api/client';
import { IconKey, IconShield } from '@/icons';
import { absoluteTime } from '@/lib/format';
import { pluginStrings } from '../registry';
import type { ArcPlugin, GuardAction, ListCell, PluginConfig } from '../types';
import { beginRiskControl, type RiskControlAPI } from './sdk';

const s = pluginStrings({
  captchaMode: 'Super risk control only',
  title: 'Super risk control',
  hint: 'Self-hosted anti-abuse risk control: browser runs SDK, server trades token for verdict before account creation or sign-in.',
  pageTitle: 'Super risk control',
  pageHint: 'Manage anti-abuse policies, puzzle challenges, and monitor real-time verification logs.',
  baseURL: 'Service address',
  baseURLHint: 'Where the browser loads boot.js from and where tokens are verified. Enter /rc for local reverse proxy.',
  site: 'Site key',
  siteHint: 'The siteKey configured on the risk control server (e.g. ai).',
  secret: 'Site secret',
  secretHint: 'Communication secret for /api/siteverify. Leave empty to keep existing.',
  adminToken: 'Admin token (RC_ADMIN_TOKEN)',
  adminTokenHint: 'Management token used to fetch logs and update policies. Leave empty to keep existing.',
  onLogin: 'Risk check on sign-in',
  onLoginHint: 'Verify a token before the password is checked. A "block" verdict refuses the sign-in.',
  policyTitle: 'Risk policies & challenges',
  policyHint: 'Configure strict IP checks, automation blocking, PoW difficulty and puzzle mode.',
  strictIp: 'Strict IP consistency (RC_STRICT_IP)',
  strictIpHint: 'Hard-reject if solving IP differs from submit IP',
  strictIpOff: 'Off (downgrade score only)',
  strictIpOn: 'On (hard reject)',
  rejectAuto: 'Reject automation signals (RC_REJECT_AUTOMATION)',
  rejectAutoHint: 'Reject immediately when automation or devtools signals match',
  rejectAutoOff: 'Off',
  rejectAutoOn: 'On (immediate reject)',
  powBits: 'Proof of Work difficulty (RC_POW_BITS: 0~32)',
  powBitsHint: 'CPU burn required before challenges (default 20)',
  puzzle: 'Puzzle challenge type',
  puzzleHint: 'Challenge presented when secondary verification is required',
  puzzleRandom: 'Random',
  puzzleShadow: '3D object orientation',
  puzzleGrid: '9-grid image recognition',
  puzzleOff: 'Off (no challenge)',
  puzzleMin: 'Trigger threshold (0.0~1.0)',
  puzzleMinHint: 'Scores below this threshold trigger a puzzle challenge (default 0.4)',
  savePolicy: 'Save policies',
  policySaved: 'Risk policies saved successfully!',
  logTitle: 'Verification & interception logs',
  logHint: 'Real-time record of all /api/siteverify evaluations, newest first.',
  logEmpty: 'No verification records yet.',
  colTime: 'Time',
  colAction: 'Action',
  colDecision: 'Decision',
  colScore: 'Score',
  colIP: 'Client IP',
  colHost: 'Domain',
  actRegister: 'Register',
  actLogin: 'Login',
  decAllow: 'Allow',
  decBlock: 'Block',
  decChallenge: 'Challenge',
  checking: 'Running risk check...',
  failed: 'The risk check did not pass. Please try again with a normal browser.',
  blocked: 'This request was rejected by risk control.',
  devtoolsLocked: 'Developer tools detected. Please close developer tools and refresh.',
  event: 'Super risk control',
  reasonFailed: 'Risk verification failed',
  reasonBlocked: 'Rejected by risk verdict',
}, {
  captchaMode: '仅超级风控',
  title: '超级风控',
  hint: '自建反滥用风控服务：浏览器运行 SDK 并取得令牌，本服务在开户或登录前换取判定结果。',
  pageTitle: '超级风控',
  pageHint: '管理反滥用风控策略、人机挑战题型，并监控实时验签与拦截日志。',
  baseURL: '服务地址',
  baseURLHint: '浏览器加载 boot.js 与令牌校验的地址。本机反代请填写 /rc。',
  site: '站点标识 (siteKey)',
  siteHint: '风控后台创建站点时填写的 siteKey（如 ai）。',
  secret: '站点密钥',
  secretHint: '用于 /api/siteverify 校验的通信密钥。留空表示保留已保存的值。',
  adminToken: '风控管理令牌 (RC_ADMIN_TOKEN)',
  adminTokenHint: '访问风控服务端 /admin 接口拉取日志与更新策略的口令。留空表示保留已保存的值。',
  onLogin: '登录时启用风控',
  onLoginHint: '校验密码之前先验证令牌。判定为"拒绝"时登录被拒。',
  policyTitle: '风控策略与人机挑战',
  policyHint: '配置严格 IP 校验、自动化拦截、工作量证明与题型。',
  strictIp: '严格 IP 一致性 (RC_STRICT_IP)',
  strictIpHint: '解题 IP 与提交 IP 不一致时直接拒绝',
  strictIpOff: '关闭 (仅降分)',
  strictIpOn: '开启 (直接拒绝)',
  rejectAuto: '直接拒绝自动化特征 (RC_REJECT_AUTOMATION)',
  rejectAutoHint: '命中明确自动化或反调试特征时直接拒绝',
  rejectAutoOff: '关闭',
  rejectAutoOn: '开启 (直接拒绝)',
  powBits: '工作量证明难度 PoW Bits (0~32)',
  powBitsHint: '浏览器解题前需消耗的算力位数（默认 20）',
  puzzle: '人机验证题型',
  puzzleHint: '触发二次验证时向用户呈现的题型',
  puzzleRandom: '随机',
  puzzleShadow: '3D 旋转指物',
  puzzleGrid: '九宫格识图',
  puzzleOff: '关闭 (不弹题)',
  puzzleMin: '触发验证阈值 (0.0~1.0)',
  puzzleMinHint: '评分低于此分数时弹题（默认 0.4）',
  savePolicy: '保存风控策略',
  policySaved: '风控策略已成功保存！',
  logTitle: '验签与拦截日志',
  logHint: '近期每一次调用 siteverify 的评分明细与拦截结果，按时间倒序。',
  logEmpty: '暂无验签调用记录。',
  colTime: '时间',
  colAction: '场景',
  colDecision: '结果',
  colScore: '评分',
  colIP: '客户端 IP',
  colHost: '站点/域名',
  actRegister: '注册',
  actLogin: '登录',
  decAllow: '放行',
  decBlock: '拦截拒绝',
  decChallenge: '二次验证',
  checking: '正在进行风控检测…',
  failed: '风控检测未通过，请使用正常浏览器重试。',
  blocked: '本次请求被风控拒绝。',
  devtoolsLocked: '检测到开发者工具已启用，请关闭后刷新页面重试。',
  event: '超级风控',
  reasonFailed: '风控验证未通过',
  reasonBlocked: '风控判定拒绝',
});

const str = (value: unknown): string => (typeof value === 'string' ? value : '');

function guarded(action: GuardAction, config: PluginConfig): boolean {
  return action === 'register' ? config['on_signup'] === true : config['on_login'] === true;
}

let ready: Promise<RiskControlAPI | null> | null = null;

function start(config: PluginConfig): Promise<RiskControlAPI | null> {
  ready = beginRiskControl(str(config['base_url']), str(config['site']));
  return ready;
}

function begin(config: PluginConfig): Promise<RiskControlAPI | null> {
  return ready ?? start(config);
}

const plugin: ArcPlugin = {
  name: 'riskcontrol',
  icon: IconShield,
  adminPages: [{
    slug: 'riskcontrol',
    title: () => s('pageTitle'),
    hint: () => s('pageHint'),
    icon: IconShield,
    permission: 'security',
    keywords: ['超级风控', '风控', '人机验证', 'risk control', 'anti-bot', 'siteverify', 'boot.js'],
  }],
  guards: [{
    name: 'riskcontrol',
    active: (action, config) => guarded(action, config) && !!str(config['base_url']) && !!str(config['site']),
    prepare: (_action, config) => { void start(config); },
    async token(action, config, form) {
      const api = await begin(config);
      if (!api) {
        ready = null;
        throw new Error('the risk control service could not be loaded');
      }
      return api.execute(action, form);
    },
    checking: () => s('checking'),
    failed: (failure) => (failure instanceof Error && failure.message === 'devtools-locked'
      ? s('devtoolsLocked') : s('failed')),
  }],
  captchaModes: [{ value: 'risk', label: () => s('captchaMode') }],
  refusals: {
    risk_blocked: () => s('blocked'),
  },
  securityEvents: { risk_challenge: () => s('event') },
  securityReasons: {
    '风控验证未通过': () => s('reasonFailed'),
    '风控判定拒绝': () => s('reasonBlocked'),
  },
  settings: [
    {
      id: 'secRisk',
      page: 'security',
      category: 'verification',
      column: 0,
      title: () => s('title'),
      hint: () => s('hint'),
      icon: IconKey,
      keywords: ['超级风控', '自建风控', '风控服务', '风险控制', 'siteverify', 'super risk control', 'risk control', 'anti-bot', 'boot.js'],
      defaults: { 'risk.on_login': 'false' },
      controls: [
        { kind: 'text', key: 'risk.base_url', label: () => s('baseURL'), hint: () => s('baseURLHint'), placeholder: 'https://ai.onyxaxis.org/rc' },
        { kind: 'text', key: 'risk.site', label: () => s('site'), hint: () => s('siteHint') },
        { kind: 'secret', key: 'risk.secret_key', label: () => s('secret'), hint: () => s('secretHint'), placeholder: '••••••••••••••••' },
        { kind: 'switch', key: 'risk.on_login', label: () => s('onLogin'), hint: () => s('onLoginHint') },
      ],
    },
    {
      id: 'secRiskConnect',
      page: 'plugin:riskcontrol',
      category: 'verification',
      column: 0,
      title: () => s('title'),
      hint: () => s('hint'),
      icon: IconKey,
      keywords: ['超级风控', '自建风控', '风控服务', '风险控制', 'siteverify', 'super risk control', 'risk control', 'anti-bot', 'boot.js'],
      defaults: { 'risk.on_login': 'false' },
      controls: [
        { kind: 'text', key: 'risk.base_url', label: () => s('baseURL'), hint: () => s('baseURLHint'), placeholder: 'https://ai.onyxaxis.org/rc' },
        { kind: 'text', key: 'risk.site', label: () => s('site'), hint: () => s('siteHint'), placeholder: 'ai' },
        { kind: 'secret', key: 'risk.secret_key', label: () => s('secret'), hint: () => s('secretHint'), placeholder: '••••••••••••••••' },
        { kind: 'secret', key: 'risk.admin_token', label: () => s('adminToken'), hint: () => s('adminTokenHint'), placeholder: 'RC_ADMIN_TOKEN' },
        { kind: 'switch', key: 'risk.on_login', label: () => s('onLogin'), hint: () => s('onLoginHint') },
      ],
    },
  ],
  actionCards: [{
    id: 'risk-policy',
    page: 'plugin:riskcontrol',
    title: () => s('policyTitle'),
    hint: () => s('policyHint'),
    icon: IconShield,
    controls: [
      {
        kind: 'select',
        key: 'strict_ip',
        label: () => s('strictIp'),
        hint: () => s('strictIpHint'),
        options: [
          { value: 'false', label: () => s('strictIpOff') },
          { value: 'true', label: () => s('strictIpOn') },
        ],
      },
      {
        kind: 'select',
        key: 'reject_automation',
        label: () => s('rejectAuto'),
        hint: () => s('rejectAutoHint'),
        options: [
          { value: 'false', label: () => s('rejectAutoOff') },
          { value: 'true', label: () => s('rejectAutoOn') },
        ],
      },
      {
        kind: 'text',
        key: 'pow_bits',
        label: () => s('powBits'),
        hint: () => s('powBitsHint'),
        placeholder: '20',
      },
      {
        kind: 'select',
        key: 'puzzle',
        label: () => s('puzzle'),
        hint: () => s('puzzleHint'),
        options: [
          { value: 'random', label: () => s('puzzleRandom') },
          { value: 'shadow', label: () => s('puzzleShadow') },
          { value: 'grid', label: () => s('puzzleGrid') },
          { value: 'off', label: () => s('puzzleOff') },
        ],
      },
      {
        kind: 'text',
        key: 'puzzle_min',
        label: () => s('puzzleMin'),
        hint: () => s('puzzleMinHint'),
        placeholder: '0.4',
      },
    ],
    defaults: {
      strict_ip: 'false',
      reject_automation: 'false',
      pow_bits: '20',
      puzzle: 'random',
      puzzle_min: '0.4',
    },
    button: {
      label: () => s('savePolicy'),
      run: async (draft) => {
        await api.put('/api/admin/riskcontrol/config', {
          strictIp: draft['strict_ip'] === 'true',
          rejectAutomation: draft['reject_automation'] === 'true',
          powBits: Number(draft['pow_bits'] || 20),
        });
        await api.put('/api/admin/riskcontrol/sites/ai', {
          puzzle: draft['puzzle'] || 'random',
          puzzleMin: Number(draft['puzzle_min'] || 0.4),
        });
        return s('policySaved');
      },
    },
  }],
  lists: [{
    id: 'verifications',
    page: 'plugin:riskcontrol',
    title: () => s('logTitle'),
    hint: () => s('logHint'),
    icon: IconShield,
    empty: () => s('logEmpty'),
    columns: [
      { key: 'time', header: () => s('colTime'), width: '180px' },
      { key: 'action', header: () => s('colAction'), width: '100px' },
      { key: 'decision', header: () => s('colDecision'), width: '120px' },
      { key: 'score', header: () => s('colScore'), width: '100px' },
      { key: 'ip', header: () => s('colIP'), width: '160px' },
      { key: 'details', header: () => s('colHost') },
    ],
    load: async (offset: number, limit: number) => {
      const res = await api.get<{ rows: Array<Record<string, unknown>>; total: number }>(
        `/api/admin/riskcontrol/verifications?offset=${offset}&limit=${limit}`,
      );
      return res;
    },
    cell: (key: string, row: Record<string, unknown>): ListCell => {
      switch (key) {
        case 'time':
          return { title: absoluteTime(Number(row['ts'] || 0)) };
        case 'action':
          return { title: row['action'] === 'register' ? s('actRegister') : s('actLogin') };
        case 'decision': {
          const dec = String(row['decision'] || '');
          if (dec === 'allow') return { title: s('decAllow'), badge: { tone: 'default' } };
          if (dec === 'block') return { title: s('decBlock'), badge: { tone: 'danger' } };
          return { title: s('decChallenge'), badge: { tone: 'muted' } };
        }
        case 'score': {
          const score = typeof row['score'] === 'number' ? row['score'].toFixed(2) : '—';
          return { title: String(score) };
        }
        case 'ip':
          return { title: String(row['ip'] || '—'), mask: true };
        case 'details': {
          const host = String(row['hostname'] || row['site'] || '—');
          return { title: host };
        }
        default:
          return { title: '' };
      }
    },
  }],
};

export default plugin;
