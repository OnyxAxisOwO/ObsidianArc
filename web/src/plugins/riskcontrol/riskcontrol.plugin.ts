// The browser's half of plugins/riskcontrol: the service's SDK in front of
// sign-up and sign-in, and the settings that point the server at it.
//
// The server's /api/site block says whether each door is guarded and hands
// out the address and site key only while one is — see plugin.go there — so
// an instance that has not configured the service never fetches boot.js.

import { IconKey, IconShield } from '@/icons';
import { pluginStrings } from '../registry';
import type { ArcPlugin, GuardAction, PluginConfig } from '../types';
import { beginRiskControl, type RiskControlAPI } from './sdk';

const s = pluginStrings({
  captchaMode: 'Super risk control only',
  title: 'Super risk control',
  hint: 'Connect the Super risk control service (self-hosted, reCAPTCHA shape): the browser runs its SDK and hands this server a token, which is traded for a verdict before an account is opened or a sign-in completes.',
  baseURL: 'Service address',
  baseURLHint: 'Where the browser loads boot.js from and where tokens are verified. A path (e.g. /rc) means the service is reverse-proxied under this site; a full URL is used as it stands.',
  site: 'Site key',
  siteHint: 'The siteKey from the service admin console. Public: the browser init() call carries it.',
  secret: 'Site secret',
  secretHint: 'Never sent to a browser and never read back out of this form. Leave it empty to keep the one already saved.',
  onLogin: 'Risk check on sign-in',
  onLoginHint: 'Verify a token before the password is checked. A "block" verdict refuses the sign-in; a middle score passes, because an account has no second door to be sent to.',
  checking: 'Running risk check...',
  failed: 'The risk check did not pass. Please try again with a normal browser.',
  blocked: 'This request was rejected by risk control.',
  event: 'Super risk control',
  reasonFailed: 'Risk verification failed',
  reasonBlocked: 'Rejected by risk verdict',
}, {
  captchaMode: '仅超级风控',
  title: '超级风控',
  hint: '接入超级风控服务：浏览器运行其 SDK 并取得令牌，本服务在开户或登录前用令牌换取判定结果。',
  baseURL: '服务地址',
  baseURLHint: '浏览器加载 boot.js 的地址，也是令牌校验的地址。填写路径（如 /rc）表示服务反代在本站域名下；填写完整地址则按原样使用。',
  site: '站点标识 (siteKey)',
  siteHint: '风控后台创建站点时得到的 siteKey。公开信息：浏览器 init() 时会携带它。',
  secret: '站点密钥',
  secretHint: '绝不会发送到浏览器，也绝不会在此表单中回显。留空表示保留已保存的密钥。',
  onLogin: '登录时启用风控',
  onLoginHint: '校验密码之前先验证令牌。判定为"拒绝"时登录被拒；中间分数放行——账户没有第二道门可去。',
  checking: '正在进行风控检测…',
  failed: '风控检测未通过，请使用正常浏览器重试。',
  blocked: '本次请求被风控拒绝。',
  event: '超级风控',
  reasonFailed: '风控验证未通过',
  reasonBlocked: '风控判定拒绝',
});

const str = (value: unknown): string => (typeof value === 'string' ? value : '');

function guarded(action: GuardAction, config: PluginConfig): boolean {
  return action === 'register' ? config['on_signup'] === true : config['on_login'] === true;
}

// Started when a card opens (prepare) and reused by the submission that
// follows, so the telemetry the SDK gathered while the reader typed is the
// telemetry the token speaks for.
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
  guards: [{
    name: 'riskcontrol',
    // Guarded, and handed what the SDK needs: the server withholds the
    // address while the service is only half configured, and a door with no
    // address to load from is not one this guard can stand at.
    active: (action, config) => guarded(action, config) && !!str(config['base_url']) && !!str(config['site']),
    prepare: (_action, config) => { void start(config); },
    async token(action, config, form) {
      const api = await begin(config);
      // A service that could not be loaded is a check that did not pass: the
      // design is fail-closed, and the server would refuse a missing token
      // anyway.
      // Not remembered either: the next submission asks for the script again.
      if (!api) {
        ready = null;
        throw new Error('the risk control service could not be loaded');
      }
      return api.execute(action, form);
    },
    checking: () => s('checking'),
    failed: () => s('failed'),
  }],
  captchaModes: [{ value: 'risk', label: () => s('captchaMode') }],
  refusals: {
    // Distinct from a failed challenge, which can be retried: this one says
    // nothing about trying again, because trying again answers to the
    // service, not to the form.
    risk_blocked: () => s('blocked'),
  },
  securityEvents: { risk_challenge: () => s('event') },
  securityReasons: {
    // The server records these in Chinese, as it always has; they are keys
    // here, not text.
    '风控验证未通过': () => s('reasonFailed'),
    '风控判定拒绝': () => s('reasonBlocked'),
  },
  settings: [{
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
      { kind: 'text', key: 'risk.base_url', label: () => s('baseURL'), hint: () => s('baseURLHint'), placeholder: 'https://risk.example.com' },
      { kind: 'text', key: 'risk.site', label: () => s('site'), hint: () => s('siteHint') },
      { kind: 'secret', key: 'risk.secret_key', label: () => s('secret'), hint: () => s('secretHint'), placeholder: 'rk_live_…' },
      { kind: 'switch', key: 'risk.on_login', label: () => s('onLogin'), hint: () => s('onLoginHint') },
    ],
  }],
};

export default plugin;
