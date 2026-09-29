import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, emptyPolicy, type AdminModel, type Provider } from '../src/admin/api';
import type { Account } from '../src/api/auth';
import { provideAdminView } from '../src/views/admin/adminView';
import { providePanelHost } from '../src/composables/usePanelHost';
import { t } from '../src/composables/useI18n';
import { compactNumber } from '../src/lib/format';
import AdminAvailability from '../src/views/admin/AdminAvailability.vue';
import AdminDashboard from '../src/views/admin/AdminDashboard.vue';
import AdminDashboardTrend from '../src/views/admin/AdminDashboardTrend.vue';
import AdminModels from '../src/views/admin/AdminModels.vue';
import AdminProviders from '../src/views/admin/AdminProviders.vue';
import AdminResources from '../src/views/admin/AdminResources.vue';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import OaUsageWindow from '../src/components/OaUsageWindow.vue';
import UptimePanel from '../src/views/UptimePanel.vue';
import UsageBoard from '../src/views/admin/usage/UsageBoard.vue';
import UsageMatrix from '../src/views/admin/usage/UsageMatrix.vue';
import { api } from '../src/api/client';
import { dashboardFixture, emptyTotals } from './fixtures/dashboard';
import {
  activeMaskCount,
  isMasked,
  mask,
  maskBilling,
  maskCredential,
  maskLog,
  maskProvider,
  maskUser,
  SAFE_CATEGORIES,
  safeModeCategories,
  safeModeEnabled,
  setAllCategories,
  STORAGE_KEY,
  toggleCategory,
  toggleSafeMode,
  MASK_PLACEHOLDER,
} from '../src/admin/safeMode';
import AdminSafeMode from '../src/views/admin/AdminSafeMode.vue';
import { installPlugins } from '../src/plugins/registry';
import qqgroup from '../src/plugins/qqgroup/qqgroup.plugin';
import { site, siteInfo } from '../src/stores/session';

describe('Admin Safe Mode store & utilities', () => {
  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    for (const cat of SAFE_CATEGORIES) {
      safeModeCategories.value[cat.key] = true;
    }
  });

  afterEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
  });

  it('masks nothing when safe mode is disabled', () => {
    safeModeEnabled.value = false;
    expect(isMasked('users')).toBe(false);
    expect(isMasked('providers')).toBe(false);
    expect(isMasked('credentials')).toBe(false);
    expect(isMasked('billing')).toBe(false);
    expect(isMasked('logs')).toBe(false);

    expect(maskUser('alice')).toBe('alice');
    expect(maskProvider('OpenAI')).toBe('OpenAI');
    expect(maskCredential('sk-test1234')).toBe('sk-test1234');
    expect(maskBilling('100,000')).toBe('100,000');
    expect(maskLog('192.168.1.1')).toBe('192.168.1.1');
  });

  it('masks values into ****** when safe mode is enabled', () => {
    toggleSafeMode(true);
    expect(safeModeEnabled.value).toBe(true);

    expect(isMasked('users')).toBe(true);
    expect(isMasked('providers')).toBe(true);
    expect(isMasked('credentials')).toBe(true);
    expect(isMasked('billing')).toBe(true);
    expect(isMasked('logs')).toBe(true);

    expect(maskUser('alice')).toBe(MASK_PLACEHOLDER);
    expect(maskProvider('Anthropic')).toBe(MASK_PLACEHOLDER);
    expect(maskCredential('sk-proj-xxxx')).toBe(MASK_PLACEHOLDER);
    expect(maskBilling('5,000')).toBe(MASK_PLACEHOLDER);
    expect(maskLog('10.0.0.1')).toBe(MASK_PLACEHOLDER);
  });

  it('allows selective category masking', () => {
    toggleSafeMode(true);
    toggleCategory('providers', false);
    toggleCategory('billing', false);

    expect(isMasked('users')).toBe(true);
    expect(isMasked('providers')).toBe(false);
    expect(isMasked('billing')).toBe(false);
    expect(isMasked('credentials')).toBe(true);

    expect(maskUser('alice')).toBe(MASK_PLACEHOLDER);
    expect(maskProvider('OpenAI')).toBe('OpenAI');
    expect(maskBilling('1,000')).toBe('1,000');
    expect(maskCredential('sk-xxxx')).toBe(MASK_PLACEHOLDER);
  });

  it('handles empty, null, and undefined values cleanly', () => {
    toggleSafeMode(true);
    expect(mask(null, 'users')).toBe('');
    expect(mask(undefined, 'users')).toBe('');
    expect(mask('', 'users')).toBe('');
    expect(maskUser(null)).toBe('');
    expect(maskUser(undefined)).toBe('');
  });

  it('supports setAllCategories and computes activeMaskCount', () => {
    toggleSafeMode(true);
    expect(activeMaskCount.value).toBe(5);

    setAllCategories(false);
    expect(activeMaskCount.value).toBe(0);
    expect(isMasked('users')).toBe(false);

    setAllCategories(true);
    expect(activeMaskCount.value).toBe(5);
    expect(isMasked('users')).toBe(true);
  });

  it('persists preferences to localStorage', async () => {
    toggleSafeMode(true);
    toggleCategory('users', false);
    await nextTick();

    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY)!);
    expect(stored.enabled).toBe(true);
    expect(stored.categories.users).toBe(false);
    expect(stored.categories.providers).toBe(true);
  });
});

describe('AdminSafeMode component', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
  });

  it('renders eye toggle button and toggles menu', async () => {
    app = createApp(AdminSafeMode);
    app.mount(host);

    const triggerBtn = host.querySelector<HTMLButtonElement>('.oa-safe-mode-btn')!;
    expect(triggerBtn).not.toBeNull();
    expect(triggerBtn.classList.contains('is-active')).toBe(false);

    // Open dropdown menu
    triggerBtn.click();
    await nextTick();

    // Menu content is present
    const menu = host.querySelector('.oa-menu-safe-mode')!;
    expect(menu).not.toBeNull();
    expect(menu.textContent).toContain('******');

    // Toggle master switch
    const masterSwitch = menu.querySelector<HTMLInputElement>('.oa-safe-switch input')!;
    expect(masterSwitch.checked).toBe(false);
    masterSwitch.click();
    await nextTick();

    expect(safeModeEnabled.value).toBe(true);
    expect(triggerBtn.classList.contains('is-active')).toBe(true);

    // Category rows can be toggled
    const categoryRows = menu.querySelectorAll<HTMLButtonElement>('.oa-safe-mode-row');
    expect(categoryRows.length).toBe(SAFE_CATEGORIES.length);

    // Click the first category row (users)
    categoryRows[0]!.click();
    await nextTick();
    expect(safeModeCategories.value.users).toBe(false);

    // Click select all
    const selectAllBtn = menu.querySelector<HTMLButtonElement>('.oa-safe-mode-action-btn')!;
    selectAllBtn.click();
    await nextTick();
    expect(safeModeCategories.value.users).toBe(true);
  });

  it('toggles safe mode when clicking header title area', async () => {
    app = createApp(AdminSafeMode);
    app.mount(host);

    const triggerBtn = host.querySelector<HTMLButtonElement>('.oa-safe-mode-btn')!;
    triggerBtn.click();
    await nextTick();

    const head = host.querySelector<HTMLElement>('.oa-safe-mode-head')!;
    expect(safeModeEnabled.value).toBe(false);

    head.click();
    await nextTick();
    expect(safeModeEnabled.value).toBe(true);

    head.click();
    await nextTick();
    expect(safeModeEnabled.value).toBe(false);
  });

  it('enables safe mode when clicking a category while safe mode is disabled', async () => {
    app = createApp(AdminSafeMode);
    app.mount(host);

    const triggerBtn = host.querySelector<HTMLButtonElement>('.oa-safe-mode-btn')!;
    triggerBtn.click();
    await nextTick();

    expect(safeModeEnabled.value).toBe(false);

    const categoryRows = host.querySelectorAll<HTMLButtonElement>('.oa-safe-mode-row');
    categoryRows[0]!.click();
    await nextTick();

    expect(safeModeEnabled.value).toBe(true);
    expect(safeModeCategories.value.users).toBe(true);
  });
});

describe('Admin Safe Mode live component integration', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let actions: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    actions = document.createElement('div');
    document.body.append(host, actions);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    actions.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
    vi.restoreAllMocks();
  });

  it('dynamically masks and unmasks sensitive data in AdminDashboard when safe mode toggles', async () => {
    vi.spyOn(adminApi, 'dashboard').mockResolvedValue(dashboardFixture());
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    app = createApp({
      setup() {
        provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
        return () => h(AdminDashboard);
      },
    });
    app.use(router);
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 20));
    await nextTick();

    // In ranking table, switch to users
    const rankingSection = host.querySelector('#secBusiestModels')!;
    const usersBtn = [...rankingSection.querySelectorAll<HTMLButtonElement>('button')].find((btn) => btn.textContent?.includes('Users') || btn.textContent?.includes('用户'));
    expect(usersBtn).toBeDefined();
    usersBtn?.click();
    await nextTick();

    // When safe mode is off, user name 'onyx' should be visible
    expect(rankingSection.textContent).toContain('onyx');

    // KPI cards have unmasked tokens and credits
    const metricsSection = host.querySelector('.oa-dashboard-metrics')!;
    expect(metricsSection.textContent).toContain('168.6M');
    expect(metricsSection.textContent).toContain('2M');

    // Recent requests section
    const recentSection = host.querySelector('#secRecentRequests')!;
    expect(recentSection.textContent).toContain('onyx');
    expect(recentSection.textContent).toContain('Anthropic');

    // Turn ON Safe Mode
    toggleSafeMode(true);
    await nextTick();

    // User name 'onyx' should be masked to '******'
    expect(rankingSection.querySelector('.oa-dashboard-rank-list')?.textContent).not.toContain('onyx');
    expect(rankingSection.querySelector('.oa-dashboard-rank-list')?.textContent).toContain('******');

    // KPI cards have masked tokens and credits
    expect(metricsSection.textContent).not.toContain('168.6M');
    expect(metricsSection.textContent).not.toContain('2M');

    // Provider name Anthropic should be masked to '******' in recent requests
    expect(recentSection.textContent).not.toContain('Anthropic');
    expect(recentSection.textContent).toContain('******');

    // Turn OFF Safe Mode
    toggleSafeMode(false);
    await nextTick();

    // 'onyx' and 'Anthropic' should be back
    expect(rankingSection.querySelector('.oa-dashboard-rank-list')?.textContent).toContain('onyx');
    expect(recentSection.textContent).toContain('Anthropic');
    expect(metricsSection.textContent).toContain('168.6M');
    expect(metricsSection.textContent).toContain('2M');
  });
});

describe('UsageBoard safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
  });

  it('masks user names and @detail notes in UsageBoard', async () => {
    const dummyRows = [
      { key: 'user-1', label: 'Alice Wonder', detail: 'alicew', requests: 10, total_tokens: 5000, credits: 2.5 },
    ];
    app = createApp({
      render() {
        return h(UsageBoard as any, { rows: dummyRows, kind: 'user', metric: 'tokens', emptyText: 'none' });
      },
    });
    app.mount(host);
    await nextTick();

    expect(host.textContent).toContain('Alice Wonder');
    expect(host.textContent).toContain('@alicew');

    toggleSafeMode(true);
    await nextTick();

    expect(host.textContent).not.toContain('Alice Wonder');
    expect(host.textContent).not.toContain('@alicew');
    expect(host.textContent).toContain('******');
    expect(host.textContent).toContain('@******');
  });
});

describe('UsageMatrix safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
  });

  // The user × model grid on the usage page: reported as showing everything
  // unmasked with Safe Mode fully on. The name, the model's provider and every
  // figure — including the ones only visible in a cell's hover title — have to
  // go together.
  it('masks the user name, the model provider detail and every figure in the who-uses-what grid', async () => {
    const matrix = {
      rows: ['user-1'],
      cols: ['model-1'],
      cells: [{ ...emptyTotals, row: 'user-1', col: 'model-1', total_tokens: 5000 }],
    };
    const users = [{ ...emptyTotals, key: 'user-1', label: 'Alice Wonder', total_tokens: 5000, last_at: 0 }];
    const models = [{ ...emptyTotals, key: 'model-1', label: 'GPT-5', detail: 'OpenAI', total_tokens: 5000, last_at: 0 }];
    app = createApp({
      render() {
        return h(UsageMatrix as any, {
          matrix, users, models, metric: 'total_tokens',
          format: (value: number) => String(value),
        });
      },
    });
    app.mount(host);
    await nextTick();

    expect(host.textContent).toContain('Alice Wonder');
    expect(host.textContent).toContain('OpenAI');
    expect(host.textContent).toContain('5000');
    expect(host.querySelector('td[title]')?.getAttribute('title')).toContain('Alice Wonder');

    toggleSafeMode(true);
    await nextTick();

    expect(host.textContent).not.toContain('Alice Wonder');
    expect(host.textContent).not.toContain('OpenAI');
    expect(host.textContent).not.toContain('5000');
    expect(host.textContent).toContain('******');
    const maskedTitle = host.querySelector('td[title]')?.getAttribute('title');
    expect(maskedTitle).not.toContain('Alice Wonder');
    expect(maskedTitle).toContain('******');
  });
});

describe('OaUsageWindow safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
  });

  // The account panel's own allowance bars, read by an administrator about
  // someone else's account rather than their own: masking the figure only
  // half hides it if the bar beside it still shows the same percentage as a
  // shape.
  it('replaces the figure and hides the meter bar when the caller marks it masked', async () => {
    const window_ = {
      kind: '5h', enforced: true,
      used_requests: 40, used_tokens: 0, used_credits: 0,
      limit_requests: 100, limit_tokens: null, limit_credits: null,
      resets_at: Date.now() + 3_600_000,
    };
    app = createApp({ render() { return h(OaUsageWindow as any, { window: window_, masked: false }); } });
    app.mount(host);
    await nextTick();
    expect(host.textContent).toContain('40 / 100');
    expect(host.querySelector('.oa-meter')).not.toBeNull();
    app.unmount();

    app = createApp({ render() { return h(OaUsageWindow as any, { window: window_, masked: true }); } });
    app.mount(host);
    await nextTick();
    expect(host.textContent).not.toContain('40 / 100');
    expect(host.textContent).toContain('******');
    expect(host.querySelector('.oa-meter')).toBeNull();
  });
});

describe('AdminDashboardTrend safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
  });

  it('masks the token trend once switched to tokens, but never the request count', async () => {
    const at = Date.now() - 3_600_000;
    const series = [{ ...emptyTotals, at, requests: 500, total_tokens: 987654 }];
    const totals = { ...emptyTotals, requests: 500, total_tokens: 987654 };
    app = createApp({
      render() { return h(AdminDashboardTrend as any, { series, totals, bucketMs: 21_600_000 }); },
    });
    app.mount(host);
    await nextTick();

    // Requests are not spending, so the default view is never masked.
    expect(host.textContent).toContain(compactNumber(500));

    const tokensBtn = [...host.querySelectorAll('button')].find((btn) => btn.textContent === t('statTokens'))!;
    tokensBtn.click();
    await nextTick();
    expect(host.textContent).toContain(compactNumber(987654));

    toggleSafeMode(true);
    await nextTick();
    expect(host.textContent).not.toContain(compactNumber(987654));
    expect(host.textContent).toContain('******');

    // Switching back to requests must not still be masked.
    const requestsBtn = [...host.querySelectorAll('button')].find((btn) => btn.textContent === t('statRequests'))!;
    requestsBtn.click();
    await nextTick();
    expect(host.textContent).toContain(compactNumber(500));
  });
});

describe('AdminModels safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    panels.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
    vi.restoreAllMocks();
  });

  // The "read-only fact" beside the editable fields — existing!.provider_name,
  // printed straight from the row rather than through maskProvider like every
  // other provider name on this screen.
  it("masks the provider name in an existing model's detail panel", async () => {
    const model: AdminModel = {
      id: 'model-1', provider_id: 'prov-1', provider_name: 'Zephyr Cloud', provider_kind: 'anthropic',
      model_id: 'claude-x', api_name: 'claude-x', system_prompt: '', auto_disabled: false,
      display_name: 'Claude X', description: '', avatar: '', enabled: true, hidden: false, sort_order: 0,
      route_to_id: '', reasoning_style: '', reasoning_tiers: [],
      supports_reasoning: false, supports_images: false, supports_vision: false, supports_streaming: true,
      supports_system_prompt: true, supports_tools: false, supports_image_gen: false, supports_chat_image_gen: false,
      emulate_tools: false, context_window: 0, max_output_tokens: 0,
      request_weight: 0, input_token_weight: 1, output_token_weight: 1, reasoning_token_weight: 1,
    };
    vi.spyOn(adminApi, 'models').mockResolvedValue({ models: [model] });
    vi.spyOn(adminApi, 'providerOptions').mockResolvedValue({ providers: [{ id: 'prov-1', name: 'Zephyr Cloud', kind: 'anthropic', enabled: true }] });
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'meta').mockResolvedValue({ provider_kinds: ['openai', 'anthropic'], reasoning_styles: ['auto'] });
    vi.spyOn(adminApi, 'health').mockResolvedValue({ hours: 24, models: [], policy: { probe: true, window_mins: 30, disable_after: 0 } });

    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }] });
    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminModels);
      },
    });
    app.use(router);
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await nextTick();

    expect(panels.textContent).toContain('Zephyr Cloud');

    toggleSafeMode(true);
    await nextTick();
    expect(panels.textContent).not.toContain('Zephyr Cloud');
    expect(panels.textContent).toContain('******');
  });
});

describe('AdminResources safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
    vi.restoreAllMocks();
  });

  // The by-account storage table's own cell slot bypassed the column's masked
  // `text`, printing the raw name and an unmasked initial straight from it.
  it('masks the account name and its avatar initial in the storage-by-account table', async () => {
    vi.spyOn(adminApi, 'resources').mockResolvedValue({
      storage: { held_bytes: 1000, held_count: 1, discarded_count: 0, by_user: [{ user_id: 'user-1', name: 'onyx', count: 3, bytes: 1000 }] },
      memory: { heap_bytes: 0, heap_sys_bytes: 0, sys_bytes: 0, gc_count: 0, gc_pause_ms: 0, goroutines: 1 },
      cpu: { cores: 1, gomaxprocs: 1 },
      sampled_at: Date.now(),
    });
    app = createApp({
      setup() {
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminResources);
      },
    });
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    expect(host.textContent).toContain('onyx');
    expect(host.querySelector('.oa-resource-avatar')?.textContent).toBe('O');

    toggleSafeMode(true);
    await nextTick();
    expect(host.textContent).not.toContain('onyx');
    expect(host.textContent).toContain('******');
    expect(host.querySelector('.oa-resource-avatar')?.textContent).toBe('*');
  });
});

describe('editable fields blur under safe mode instead of being replaced', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;

  const member: Account = {
    id: 'member', username: 'member', nickname: 'Member', email: 'member@example.com', fields: { qq: '10001' }, bio: '', avatar: '',
    role: 'user', status: 'active', group_id: '', group_expires_at: 0, admin_permissions: [],
    created_at: Date.now(), updated_at: Date.now(), last_login_at: 0, group_name: '',
    email_verified: true, allow_stats: true, allow_delete_conversations: true,
    api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
  };

  const provider: Provider = {
    id: 'prov-1', name: 'OpenRouter', kind: 'openai', base_url: 'https://openrouter.ai/api/v1',
    allow_insecure: false, api_key_hint: '****abcd', headers: {}, anthropic_version: '',
    reasoning_style: 'auto', timeout_seconds: 120, enabled: true, sort_order: 0, model_count: 0,
    created_at: Date.now(), updated_at: Date.now(),
  };

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    // An instance running the QQ-group plugin, so the panel has a plugin
    // account field to blur along with the core's.
    installPlugins([qqgroup]);
    site.value = { ...siteInfo.value, fields: { qq: 'optional' } };
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    panels.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
    installPlugins([]);
    site.value = null;
    vi.restoreAllMocks();
  });

  function fieldFor(label: string): HTMLElement {
    const field = [...panels.querySelectorAll<HTMLLabelElement>('.oa-field')].find((node) =>
      node.querySelector('.oa-field-label')?.textContent === label);
    if (!field) throw new Error(`Field not found: ${label}`);
    return field;
  }

  it("blurs the user detail panel's nickname, email, plugin field and avatar — never the value itself", async () => {
    const policy = emptyPolicy('user', member.id);
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
    vi.spyOn(adminApi, 'users').mockResolvedValue({ users: [member], total: 1 });
    vi.spyOn(adminApi, 'user').mockResolvedValue({
      user: member, policy, usage: { windows: [], unlimited: true },
      lifetime: { requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0, credits: 0, errors: 0, users: 0, models: 0, duration_ms: 0 },
      cards: { total: 0, available: 0, used: 0, expired: 0, cards: [] },
    });
    vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });

    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminUsers);
      },
    });
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await nextTick();

    // The field still carries the real value — only editing is at stake here,
    // masking would replace it with ****** and there would be nothing to edit.
    expect(fieldFor(t('nickname')).querySelector<HTMLInputElement>('input')!.value).toBe('Member');
    expect(fieldFor(t('nickname')).classList.contains('oa-safe-blur')).toBe(false);

    toggleSafeMode(true);
    await nextTick();
    expect(fieldFor(t('nickname')).classList.contains('oa-safe-blur')).toBe(true);
    expect(fieldFor(t('email')).classList.contains('oa-safe-blur')).toBe(true);
    // A plugin's field, drawn by the core's component, blurs the same way.
    const qqLabel = qqgroup.fields!['qq']!.optionalLabel();
    expect(fieldFor(qqLabel).classList.contains('oa-safe-blur')).toBe(true);
    expect(fieldFor(qqLabel).querySelector<HTMLInputElement>('input')!.value).toBe('10001');
    expect(fieldFor(t('avatar')).classList.contains('oa-safe-blur')).toBe(true);
    // Bio is prose, not one of the listed user fields, so it is left alone.
    expect(fieldFor(t('bio')).classList.contains('oa-safe-blur')).toBe(false);
    // Still the real value underneath the blur.
    expect(fieldFor(t('nickname')).querySelector<HTMLInputElement>('input')!.value).toBe('Member');

    toggleCategory('users', false);
    await nextTick();
    expect(fieldFor(t('nickname')).classList.contains('oa-safe-blur')).toBe(false);
  });

  it("blurs the provider panel's name and base URL fields while providers are masked", async () => {
    vi.spyOn(adminApi, 'providers').mockResolvedValue({ providers: [provider] });
    vi.spyOn(adminApi, 'meta').mockResolvedValue({ provider_kinds: ['openai', 'anthropic'], reasoning_styles: ['auto'] });

    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminProviders);
      },
    });
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
    await nextTick();

    expect(fieldFor(t('name')).classList.contains('oa-safe-blur')).toBe(false);
    expect(fieldFor(t('baseURL')).classList.contains('oa-safe-blur')).toBe(false);

    toggleSafeMode(true);
    await nextTick();
    expect(fieldFor(t('name')).classList.contains('oa-safe-blur')).toBe(true);
    expect(fieldFor(t('baseURL')).classList.contains('oa-safe-blur')).toBe(true);
    // Still editable — the base URL field still carries the real value.
    expect(fieldFor(t('baseURL')).querySelector<HTMLInputElement>('input')!.value).toBe(provider.base_url);

    toggleCategory('providers', false);
    await nextTick();
    expect(fieldFor(t('name')).classList.contains('oa-safe-blur')).toBe(false);
  });
});

describe('UptimePanel safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;
  let panels: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    panels = document.createElement('div');
    document.body.append(host, panels);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    panels.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
    vi.restoreAllMocks();
  });

  it('masks the provider name in uptime model rows when safe mode is enabled', async () => {
    vi.spyOn(api, 'get').mockImplementation(async (path: string) => {
      if (path === '/api/uptime') {
        return {
          uptime_sec: 7200,
          models: [
            {
              id: 'model-claude',
              display_name: 'Claude Sonnet 3.7',
              provider_name: 'Anthropic Direct',
              enabled: true,
              state: 'up',
              uptime: 0.99,
              uptime_hour: 0.99,
              history: [],
            },
          ],
        } as any;
      }
      return null as any;
    });

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
    });

    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        return () => h(UptimePanel);
      },
    });
    app.use(router);
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    // Provider name is initially visible while safe mode is off
    expect(panels.textContent).toContain('Anthropic Direct');

    // Enabling safe mode masks the provider name with ******
    toggleSafeMode(true);
    await nextTick();
    expect(panels.textContent).not.toContain('Anthropic Direct');
    expect(panels.textContent).toContain('******');

    // Disabling providers mask reveals it again
    toggleCategory('providers', false);
    await nextTick();
    expect(panels.textContent).toContain('Anthropic Direct');
  });
});

describe('AdminAvailability safe mode masking', () => {
  let app: App | undefined;
  let host: HTMLElement;

  beforeEach(() => {
    localStorage.clear();
    safeModeEnabled.value = false;
    setAllCategories(true);
    host = document.createElement('div');
    document.body.appendChild(host);
  });

  afterEach(() => {
    app?.unmount();
    app = undefined;
    host.remove();
    localStorage.clear();
    safeModeEnabled.value = false;
    vi.restoreAllMocks();
  });

  it('masks the provider name in health model cards when safe mode is enabled', async () => {
    vi.spyOn(adminApi, 'settings').mockResolvedValue({ settings: {} });
    vi.spyOn(adminApi, 'health').mockResolvedValue({
      hours: 24,
      models: [
        {
          model_id: 'm1',
          name: 'GPT-5 Mini',
          provider: 'OpenAI Upstream',
          enabled: true,
          auto_disabled: false,
          status: {
            state: 'up',
            uptime: 1,
            samples: 10,
            user_samples: 10,
            system_samples: 0,
            failures_in_a_row: 0,
            last_ok_at: Date.now(),
            last_error_at: 0,
            last_code: '',
            last_message: '',
            errors: [],
          },
        },
      ],
      policy: { probe: true, window_mins: 30, disable_after: 0 },
    });

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
    });

    app = createApp({
      setup() {
        provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
        return () => h(AdminAvailability);
      },
    });
    app.use(router);
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    expect(host.textContent).toContain('OpenAI Upstream');

    toggleSafeMode(true);
    await nextTick();
    expect(host.textContent).not.toContain('OpenAI Upstream');
    expect(host.textContent).toContain('******');

    toggleCategory('providers', false);
    await nextTick();
    expect(host.textContent).toContain('OpenAI Upstream');
  });
});


