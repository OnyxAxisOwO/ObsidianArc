import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, nextTick, type App as VueApp } from 'vue';
import App from '../src/App.vue';
import { router } from '../src/router';
import { adopt, forget, site, siteInfo } from '../src/stores/session';
import type { Account } from '../src/api/auth';
import { conversations } from '../src/chat/useChat';
import { changeLanguage, t } from '../src/composables/useI18n';

// The whole application, mounted.
//
// Every other test here checks one unit; this one checks that they are wired
// together — that the router resolves, that the layouts provide what the
// panels inject, and that the screens a reader actually lands on render
// without throwing. A component graph this size fails by exploding at mount,
// which no amount of unit testing catches.

const ACCOUNT: Account = {
  id: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
  username: 'ada',
  email: 'ada@example.com',
  qq: '',
  nickname: 'Ada',
  avatar: '',
  bio: '',
  role: 'user',
  group_id: 'g1',
  group_expires_at: 0,
  group_name: 'Default',
  status: 'active',
  created_at: Date.now(),
  updated_at: Date.now(),
  last_login_at: Date.now(),
  email_verified: true,
      allow_stats: true,
      allow_delete_conversations: true,
      api_restricted: false,
      api_restricted_until: 0,
      api_restriction_source: '',
      signup_user_agent: 'Mozilla/5.0 ObsidianArcTest/1.0',
};

/**
 * Every endpoint the screens under test reach for, answered with an empty but
 * well-shaped payload.
 *
 * The shapes matter as much as the emptiness: a screen handed `{}` where it
 * expected `{ providers: [] }` fails in a way no real server would produce,
 * and the test would then be measuring the stub.
 */
const EMPTY_BODIES: Array<[RegExp, unknown]> = [
  [/\/api\/admin\/references/, { groups: [], models: [], providers: [] }],
  [/\/api\/admin\/security\/events/, { events: [], total: 0 }],
  [/\/api\/admin\/security\/two-factor/, {
    policy: 'optional', accounts: 0, enabled: 0, admins: 0, admins_enabled: 0,
    admins_without: [], remember_days: 0, issuer_fallback: 'Obsidian Arc', available: true,
  }],
  [/\/api\/profile\/two-factor$/, {
    available: true, enabled: false, enabled_at: 0, recovery_remaining: 0,
    mandatory: false, policy: 'optional', remember_days: 0,
  }],
  [/\/api\/health/, { status: 'ok', version: 'vtest', uptime_sec: 1 }],
  [/\/api\/announcements/, { announcements: [], unread: 0, popup: null }],
  [/\/api\/notifications\/poll/, { notifications: [], unread: 0 }],
  [/\/api\/notifications/, { notifications: [], unread: 0, seen_at: 0 }],
  [/\/api\/conversations/, { conversations: [] }],
  [/\/api\/keys/, { keys: [], enabled: false, max: 0 }],
  [/\/api\/admin\/providers/, { providers: [] }],
  [/\/api\/admin\/models/, { models: [] }],
  [/\/api\/admin\/groups/, { groups: [], policies: [] }],
  [/\/api\/admin\/meta/, { provider_kinds: ['openai', 'anthropic'], reasoning_styles: ['auto'] }],
  [/\/api\/admin\/health/, { hours: 24, models: [], policy: { probe: true, window_mins: 30, disable_after: 0 } }],
  [/\/api\/admin\/usage\/records/, { records: [], total: 0 }],
  [/\/api\/admin\/usage/, {
    totals: {
      requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0,
      total_tokens: 0, credits: 0, errors: 0,
    },
    by_model: [], by_provider: [], by_user: [], series: [], bucket_ms: 3600000,
  }],
  [/\/api\/admin\/quota\/policies/, { policies: [] }],
  [/\/api\/admin\/codes\/[^/]+\/redemptions/, { redemptions: [] }],
  [/\/api\/admin\/codes/, { codes: [] }],
  [/\/api\/admin\/announcements/, { announcements: [] }],
  [/\/api\/admin\/logs\/facets/, {
    users: [], models: [], error_codes: [], statuses: [], total: 0, dropped: 0, oldest: 0,
  }],
  [/\/api\/admin\/logs/, { entries: [], total: 0, limit: 50, offset: 0 }],
  [/\/api\/admin\/users/, { users: [], total: 0 }],
  [/\/api\/admin\/settings/, {
    settings: {}, groups: [], mail_configured: false, attachments: { held: 0, bytes: 0 },
  }],
  [/\/api\/admin\/resources/, {
    storage: { held_bytes: 0, held_count: 0, discarded_count: 0, by_user: [] },
    memory: { heap_bytes: 0, heap_sys_bytes: 0, sys_bytes: 0, gc_count: 0, gc_pause_ms: 0, goroutines: 0 },
    cpu: { cores: 1, gomaxprocs: 1 },
    sampled_at: 0,
  }],
  [/\/api\/admin\/dashboard/, {
    counts: {
      users: 0, active_users: 0, providers: 0, enabled_providers: 0, models: 0, enabled_models: 0,
    },
    newest_users: [],
    last_24h: {
      requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0,
      total_tokens: 0, credits: 0, errors: 0,
    },
    last_7d: {
      requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0,
      total_tokens: 0, credits: 0, errors: 0,
    },
    top_models: [], top_users: [], series: [], bucket_ms: 3600000, recent: [],
  }],
  [/\/api\/usage\/me\/history/, {
    totals: {
      requests: 0, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0,
      total_tokens: 0, credits: 0, errors: 0,
    },
    turns: [],
  }],
  [/\/api\/usage\/cards/, { cards: [] }],
  [/\/api\/usage\/me$/, { unlimited: true, windows: [] }],
  [/\/api\/models/, { models: [] }],
  [/\/api\/uptime/, {
    uptime_sec: 10,
    models: [
      {
        id: 'model-1',
        display_name: 'Model One',
        provider_name: 'Provider A',
        enabled: true,
        uptime: 0.99,
        uptime_hour: 0.5,
        state: 'up',
        total: 100,
        history: [{ at: 1000, uptime: 0.99, total: 10 }],
      },
      {
        id: 'model-2',
        display_name: 'Model Two',
        provider_name: 'Provider B',
        enabled: true,
        uptime: 0.85,
        state: 'degraded',
        total: 50,
        history: [{ at: 1000, uptime: 0.85, total: 5 }],
      },
    ],
  }],
];

function stubServer(): void {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/admin/health/probe')) {
      const encoder = new TextEncoder();
      return new Response(new ReadableStream({
        start(controller) {
          controller.enqueue(encoder.encode(
            'event: progress\ndata: {"completed":1,"total":2,"succeeded":1,"failed":0}\n\n',
          ));
          window.setTimeout(() => {
            controller.enqueue(encoder.encode(
              'event: done\ndata: {"completed":2,"total":2,"succeeded":1,"failed":1}\n\n',
            ));
            controller.close();
          }, 50);
        },
      }), {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      });
    }
    const match = EMPTY_BODIES.find(([pattern]) => pattern.test(url));
    return new Response(JSON.stringify(match?.[1] ?? {}), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }));
}

let app: VueApp | null = null;
let host: HTMLElement;

async function mountAt(path: string): Promise<void> {
  await router.replace(path);
  await router.isReady();
  app = createApp(App);
  app.use(router);
  app.mount(host);
  // Long enough for the route component to mount and for whatever it asked
  // the server for to come back, so nothing is torn down mid-flight.
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => requestAnimationFrame(resolve));
}

beforeEach(() => {
  stubServer();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  site.value = null;
  delete window.turnstile;
  app?.unmount();
  app = null;
  host.remove();
  document.body.textContent = '';
  forget();
  vi.unstubAllGlobals();
});

describe('the application, mounted', () => {
  it('shows a permission message and does not fetch an ungranted administrator page', async () => {
    adopt({ ...ACCOUNT, role: 'admin', admin_permissions: ['users'] });
    await mountAt('/admin/providers');
    expect(host.querySelector('.oa-permission-empty')?.textContent).toContain(t('permissionDeniedTitle'));
    expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes('/api/admin/providers'))).toBe(false);
    await router.push('/admin/users');
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => requestAnimationFrame(resolve));
    expect(host.querySelector('.oa-permission-empty')).toBeNull();
    expect(vi.mocked(fetch).mock.calls.some(([url]) => String(url).includes('/api/admin/users'))).toBe(true);
  });
  it('shows the sign-in card to a visitor', async () => {
    await mountAt('/login');
    expect(host.querySelector('.oa-auth-card')).not.toBeNull();
    expect(host.querySelector('input[type="password"]')).not.toBeNull();
  });

  it('shows the chat to a signed-in account', async () => {
    adopt(ACCOUNT);
    await mountAt('/');

    expect(host.querySelector('.oa-workspace')).not.toBeNull();
    expect(host.querySelector('.ai-chat-sidebar')).not.toBeNull();
    expect(host.querySelector('.ai-chat-composer')).not.toBeNull();
    // No model is configured in this stub, so the surface says so rather than
    // offering a composer that would refuse.
    expect(host.querySelector('.ai-chat-setup')).not.toBeNull();
  });

  it('opens a panel beside the chat rather than replacing it', async () => {
    adopt(ACCOUNT);
    await mountAt('/settings');

    // The chat is still mounted behind the column.
    expect(host.querySelector('.ai-chat-sidebar')).not.toBeNull();
    // The panel teleports into the row, which is inside the mount point.
    const panel = host.querySelector('.oa-panel');
    expect(panel).not.toBeNull();
    expect(panel?.parentElement?.classList.contains('oa-chat-root')).toBe(true);
    expect(host.querySelector('.oa-settings-tabs')).not.toBeNull();
    const registrationAgent = Array.from(host.querySelectorAll('.oa-fact')).find(
      (fact) => fact.querySelector('.oa-fact-label')?.textContent === t('registrationUserAgent'),
    );
    expect(registrationAgent?.querySelector('.oa-fact-value')?.textContent)
      .toBe('Mozilla/5.0 ObsidianArcTest/1.0');
  });

  it('renders the operator-written About introduction as Markdown', async () => {
    site.value = {
      ...siteInfo.value,
      about: { title: 'Lantern', body: '**Private** knowledge for [the team](https://example.com).' },
    };
    adopt(ACCOUNT);
    await mountAt('/about');

    expect(host.querySelector('.oa-about-name')?.textContent).toBe('Lantern');
    expect(host.querySelector('.oa-about-lede strong')?.textContent).toBe('Private');
    expect(host.querySelector<HTMLAnchorElement>('.oa-about-lede a')?.href).toBe('https://example.com/');
  });

  it('shows the current group and renders its description as Markdown on Usage', async () => {
    adopt({
      ...ACCOUNT,
      group_name: 'Research',
      group_description: '**Early** access for [members](https://example.com/group).',
    });
    await mountAt('/usage');

    expect(host.querySelector('.oa-usage-group-name')?.textContent).toBe('Research');
    expect(host.querySelector('.oa-usage-group-description strong')?.textContent).toBe('Early');
    expect(host.querySelector<HTMLAnchorElement>('.oa-usage-group-description a')?.href)
      .toBe('https://example.com/group');
  });

  it('offers every image size as a tile whose silhouette fits its box', async () => {
    adopt(ACCOUNT);
    await mountAt('/image-lab');

    expect(host.querySelectorAll('.oa-ratio-tile').length).toBe(8);
    // The picture a prompt can work from, and nothing staged until one is
    // chosen — the thumbnail is what says something is.
    expect(host.querySelector('.oa-reference-pick')).not.toBeNull();
    expect(host.querySelector('.oa-reference')).toBeNull();
    // One rule draws all of them from a ratio, which is what lets a preset be
    // added to the table without drawing another `<svg>` — and what this
    // guards: a new ratio cannot quietly overflow the 32-unit icon box.
    for (const rect of Array.from(host.querySelectorAll('.oa-ratio-rect'))) {
      const x = Number(rect.getAttribute('x'));
      const y = Number(rect.getAttribute('y'));
      const width = Number(rect.getAttribute('width'));
      const height = Number(rect.getAttribute('height'));
      expect(width).toBeGreaterThan(0);
      expect(height).toBeGreaterThan(0);
      expect(x).toBeGreaterThanOrEqual(0);
      expect(y).toBeGreaterThanOrEqual(0);
      expect(x + width).toBeLessThanOrEqual(32);
      expect(y + height).toBeLessThanOrEqual(32);
    }
  });

  it('sends a visitor asking for a panel to the sign-in card', async () => {
    await mountAt('/keys');
    expect(router.currentRoute.value.path).toBe('/login');
  });

  it('refuses the backoffice to an ordinary account, over the chat', async () => {
    adopt(ACCOUNT);
    await mountAt('/admin');

    expect(host.querySelector('.ai-chat-sidebar')).not.toBeNull();
    expect(document.querySelector('.oa-modal-overlay')).not.toBeNull();
    // The rail of an administration screen it may not see is not drawn at all.
    expect(host.querySelector('.oa-admin-rail')).toBeNull();
  });

  it('draws the backoffice for an administrator', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/providers');

    expect(host.querySelector('.oa-admin-rail')).not.toBeNull();
    expect(document.querySelector('.oa-modal-overlay')).toBeNull();
  });

  it('names an address that resolves to nothing', async () => {
    await mountAt('/nowhere');
    expect(host.querySelector('.oa-notice-title')).not.toBeNull();
  });
});

describe('what moves, and what does not', () => {
  async function search(selector: string, value: string): Promise<HTMLInputElement> {
    const input = host.querySelector<HTMLInputElement>(selector)!;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    return input;
  }

  function shown(selector: string): Element[] {
    return Array.from(host.querySelectorAll<HTMLElement>(selector)).filter((node) => {
      for (let parent: HTMLElement | null = node; parent; parent = parent.parentElement) {
        if (parent.style.display === 'none') return false;
      }
      return true;
    });
  }

  it('filters history titles without changing the underlying list and clears with Escape', async () => {
    adopt(ACCOUNT);
    await mountAt('/');
    conversations.value = ['Vue Search', '周末计划', 'Go notes'].map((title, index) => ({
      id: `search-${index}`, title, model_id: '', pinned: false, message_count: 0, created_at: 0, updated_at: 0,
    }));
    await nextTick();
    await search('.oa-history-search input', '  ＶＵＥ search  ');
    expect(host.querySelectorAll('.ai-chat-list-item')).toHaveLength(1);
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('Vue Search');
    expect(conversations.value).toHaveLength(3);
    await search('.oa-history-search input', '周末');
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('周末计划');
    const input = await search('.oa-history-search input', 'absent');
    expect(host.querySelector('.ai-chat-list-empty')?.textContent).toBe(t('noSearchResults'));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await nextTick();
    expect(host.querySelectorAll('.ai-chat-list-item')).toHaveLength(3);
    expect(document.activeElement).toBe(input);
    input.dispatchEvent(new CompositionEvent('compositionstart'));
    await search('.oa-history-search input', 'zhou');
    expect(host.querySelectorAll('.ai-chat-list-item')).toHaveLength(3);
    input.value = '周末';
    input.dispatchEvent(new CompositionEvent('compositionend'));
    await nextTick();
    expect(host.querySelectorAll('.ai-chat-list-item')).toHaveLength(1);
    expect(host.querySelector('.ai-chat-list-title')?.textContent).toBe('周末计划');
    conversations.value = [];
  });

  it('searches settings across categories and preserves a hidden profile draft', async () => {
    await changeLanguage('en');
    adopt(ACCOUNT);
    await mountAt('/settings');
    await search('.oa-settings-search input', 'nickname');
    expect(shown('.oa-settings-panel')).toHaveLength(1);
    const nickname = shown('.oa-settings-panel')[0]!.querySelector<HTMLInputElement>('input')!;
    nickname.value = 'Unsaved nickname';
    nickname.dispatchEvent(new Event('input', { bubbles: true }));
    await search('.oa-settings-search input', 'wallpaper');
    expect(shown('.oa-wallpaper-upload')).toHaveLength(1);
    expect(shown('.oa-range-field')).toHaveLength(0);
    await search('.oa-settings-search input', 'nothing-matches-this');
    expect(shown('.oa-settings-panel')).toHaveLength(0);
    expect(host.querySelector('.oa-settings .oa-search-empty')?.textContent).toBe(t('noSearchResults'));
    await search('.oa-settings-search input', 'nickname');
    expect(shown('.oa-settings-panel')[0]!.querySelector<HTMLInputElement>('input')).toBe(nickname);
    expect(nickname.value).toBe('Unsaved nickname');
    host.querySelector<HTMLButtonElement>('.oa-settings-search button')!.click();
    await nextTick();
    expect(shown('.oa-settings-panel')).toHaveLength(1);
    expect(shown('.oa-wallpaper-upload')).toHaveLength(1);
    expect(shown('.oa-range-field')).toHaveLength(0);
    expect(host.querySelector('.oa-panel')).not.toBeNull();
  });

  it('updates settings matches when switching language with a query still entered', async () => {
    await changeLanguage('en');
    adopt(ACCOUNT);
    await mountAt('/settings');
    await search('.oa-settings-search input', '壁纸');
    expect(shown('.oa-settings-panel')).toHaveLength(0);
    await changeLanguage('zh');
    await nextTick();
    expect(shown('.oa-wallpaper-upload')).toHaveLength(1);
    expect(shown('.oa-range-field')).toHaveLength(0);
    expect(host.querySelector<HTMLInputElement>('.oa-settings-search input')?.placeholder).toBe('搜索设置');
    await changeLanguage('en');
  });

  it('filters admin navigation and instance settings while retaining unsaved values', async () => {
    await changeLanguage('en');
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/settings');
    await search('.oa-admin-search input', 'providers');
    expect(host.querySelectorAll('.oa-admin-nav')).toHaveLength(1);
    expect(host.querySelector('.oa-admin-nav')?.getAttribute('href')).toBe('/admin/providers');
    expect(router.currentRoute.value.path).toBe('/admin/settings');
    await search('.oa-admin-body .oa-search input', t('siteName'));
    const siteName = shown('.oa-admin-body section')[0]!.querySelector<HTMLInputElement>('input')!;
    siteName.value = 'Unsaved site';
    siteName.dispatchEvent(new Event('input', { bubbles: true }));
    await search('.oa-admin-body .oa-search input', t('attachmentPurgeDaily'));
    expect(shown('.oa-admin-body section')).toHaveLength(1);
    expect(shown('.oa-admin-body section')[0]!.textContent).toContain(t('attachmentPurgeDaily'));
    await search('.oa-admin-body .oa-search input', t('siteName'));
    expect(shown('.oa-admin-body section')[0]!.querySelector<HTMLInputElement>('input')).toBe(siteName);
    expect(siteName.value).toBe('Unsaved site');
    await search('.oa-admin-body .oa-search input', 'nothing-matches-this');
    expect(shown('.oa-admin-body section')).toHaveLength(0);
    expect(host.querySelector('.oa-admin-body .oa-search-empty')?.textContent).toBe(t('noSearchResults'));
    await search('.oa-admin-body .oa-search input', '');
    // The site category's cards: identity, login background, the PWA card,
    // landing, about, the home notice and the feedback signature.
    expect(shown('.oa-admin-body section')).toHaveLength(7);
  });

  it('saves drafts across categories and keeps edits made during an in-flight save', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/settings');
    await search('#secIdentity input', 'First draft');
    const category = Array.from(host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab'))
      .find((button) => button.textContent?.includes(t('controlFiles')))!;
    category.click();
    await nextTick();
    expect(shown('.oa-control-card').map((card) => card.id)).toEqual(['secAttachments', 'secCleanup']);
    await search('#secAttachments input[type="number"]', '12');
    const server = fetch;
    let finish!: (response: Response) => void;
    let submitted: Record<string, string> = {};
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/api/admin/settings') && init?.method === 'PUT') {
        submitted = JSON.parse(String(init.body));
        return new Promise<Response>((resolve) => { finish = resolve; });
      }
      return server(input, init);
    }));
    host.querySelector<HTMLButtonElement>('.oa-admin-actions .primary')!.click();
    await nextTick();
    expect(submitted['site.name']).toBe('First draft');
    expect(submitted['attachments.max_mb']).toBe('12');
    expect(submitted).not.toHaveProperty('registration.enabled');
    await search('#secAttachments input[type="number"]', '18');
    finish(new Response(JSON.stringify({ settings: submitted }), { status: 200 }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(host.querySelector('.oa-control-save-state')?.textContent).toContain(t('controlUnsaved'));
    expect(host.querySelector<HTMLInputElement>('#secAttachments input')?.value).toBe('18');
    await search('#secAttachments input[type="number"]', '12');
    expect(host.querySelector('.oa-control-save-state')?.textContent).toContain(t('controlSaved'));
  });

  // The operator's policy holds an account at the door until it enrols. The
  // server refuses the rest regardless; the router only saves drawing a
  // screen made of refusals, and must still let the enrolment screen mount.
  // The operator's code at the backoffice door. The page must not mount — and
  // so must not ask the server for anything — until the code is in, and a
  // visit that lapses mid-page must hand the screen back to the door.
  it('asks for the backoffice code before drawing a page, and draws it after', async () => {
    const locked: Account = {
      ...ACCOUNT, role: 'super_admin', two_factor_at: 1,
      two_factor_backoffice_verify: 'visit', two_factor_backoffice_minutes: 15, two_factor_backoffice_locked: true,
    };
    adopt(locked);
    const server = fetch;
    const calls: string[] = [];
    let open = false;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url.endsWith('/api/auth/me')) {
        return Promise.resolve(new Response(JSON.stringify({
          user: { ...locked, two_factor_backoffice_locked: !open }, preferences: {},
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (url.endsWith('/api/profile/two-factor/backoffice')) {
        open = true;
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (url.endsWith('/api/profile/two-factor/backoffice/leave')) {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return server(input, init);
    }));

    await mountAt('/admin/users');
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    expect(host.querySelector('.oa-2fa-unlock')).not.toBeNull();
    expect(host.querySelector('.oa-2fa-unlock')!.textContent).toContain(t('backofficeUnlockVisit'));
    expect(host.querySelector('#usersList')).toBeNull();
    expect(calls.some((call) => call.includes('/api/admin/'))).toBe(false);

    const field = host.querySelector<HTMLInputElement>('.oa-2fa-unlock input')!;
    field.value = '123456';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    for (let i = 0; i < 4; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      await nextTick();
    }
    expect(calls).toContain('POST /api/profile/two-factor/backoffice');
    expect(host.querySelector('.oa-2fa-unlock')).toBeNull();
    expect(host.querySelector('#usersList')).not.toBeNull();

    // The server says the visit ran out: the page gives way to the door.
    open = false;
    window.dispatchEvent(new Event('oa-backoffice-locked'));
    for (let i = 0; i < 3; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      await nextTick();
    }
    expect(host.querySelector('.oa-2fa-unlock')).not.toBeNull();

    // Leaving the backoffice says so, which is what ends a visit in this mode.
    app?.unmount();
    app = null;
    expect(calls).toContain('POST /api/profile/two-factor/backoffice/leave');
  });

  // The door switched on after this page loaded: the shell's copy of the
  // account says nothing about it, so the first refusal has to be enough.
  it('shows the door when it was switched on after the page loaded', async () => {
    const before: Account = { ...ACCOUNT, role: 'super_admin', two_factor_at: 1 };
    adopt(before);
    const server = fetch;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/api/auth/me')) {
        return Promise.resolve(new Response(JSON.stringify({
          user: { ...before, two_factor_backoffice_verify: 'visit', two_factor_backoffice_locked: true }, preferences: {},
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return server(input, init);
    }));
    await mountAt('/admin/users');
    expect(host.querySelector('#usersList')).not.toBeNull();

    window.dispatchEvent(new Event('oa-backoffice-locked'));
    for (let i = 0; i < 3; i++) {
      await new Promise((resolve) => setTimeout(resolve, 0));
      await nextTick();
    }
    expect(host.querySelector('.oa-2fa-stage .oa-2fa-unlock')).not.toBeNull();
    expect(host.querySelector('#usersList')).toBeNull();
  });

  it('sends an account the policy is holding to enrol, and nowhere else', async () => {
    adopt({ ...ACCOUNT, two_factor_enrol: true });
    await mountAt('/settings');
    expect(router.currentRoute.value.path).toBe('/two-factor');
    expect(router.currentRoute.value.query['next']).toBe('/settings');
    expect(host.querySelector('.oa-auth-title')?.textContent).toBe(t('twoFactorRequiredTitle'));
    expect(host.querySelector('.oa-2fa-wizard')).not.toBeNull();
  });

  it('draws the setup in place of a backoffice page the policy has closed', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin', two_factor_backoffice: true });
    await mountAt('/admin/users');
    expect(host.querySelector('.oa-2fa-gate .oa-2fa-title')?.textContent).toBe(t('twoFactorGateTitle'));
    expect(host.querySelector('.oa-2fa-gate .oa-2fa-wizard')).not.toBeNull();
    // The page itself never mounted, so it asked the server for nothing.
    expect(host.querySelector('#usersList')).toBeNull();
    // The rail stays, so it is clear where this is.
    expect(host.querySelector('.oa-admin-rail')).not.toBeNull();
  });

  it('opens hidden categories for deep links and keeps local search from hiding the target', async () => {
    // jsdom has no layout or native scrolling; the browser verifies placement.
    HTMLElement.prototype.scrollIntoView ??= () => {};
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/security#secChatChallenge');
    expect(host.querySelector('.oa-workbench-tab[aria-pressed="true"]')?.textContent)
      .toContain(t('controlVerification'));
    expect(shown('.oa-control-card').map((card) => card.id)).toContain('secChatChallenge');
    await search('.oa-workbench .oa-search input', t('signupsPerIP'));
    expect(shown('.oa-control-card').map((card) => card.id)).toEqual(['secRegistrationLimits']);
    await router.push('/admin/security#secSecurityLog');
    await nextTick();
    expect(host.querySelector<HTMLInputElement>('.oa-workbench .oa-search input')?.value).toBe('');
    expect(shown('.oa-control-card').map((card) => card.id)).toEqual(['secSecurityLog']);
  });

  it('keeps resource data and table state after a failed refresh and does not invent a CPU sample', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/resources');
    expect(host.querySelector('.oa-resource-metrics')?.textContent).toContain(t('resMeasuring'));
    const table = host.querySelector('.oa-table-block');
    const server = fetch;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/api/admin/resources')) return Promise.reject(new Error('Refresh unavailable'));
      return server(input, init);
    }));
    host.querySelector<HTMLButtonElement>('.oa-admin-actions button')!.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(host.querySelector('.oa-table-block')).toBe(table);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('Refresh unavailable');
    expect(host.querySelector('.oa-resource-metrics')?.textContent).toContain(t('resMeasuring'));
  });

  it('expands the compact admin search across the navigation strip on narrow screens', async () => {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
      matches: query === '(max-width: 900px)',
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })));
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/settings');

    const searchBox = host.querySelector<HTMLElement>('.oa-admin-search')!;
    const trigger = searchBox.querySelector<HTMLButtonElement>('.oa-search-open')!;
    const input = searchBox.querySelector<HTMLInputElement>('input')!;
    expect(searchBox.classList.contains('oa-search-collapsible')).toBe(true);
    expect(searchBox.classList.contains('expanded')).toBe(false);

    trigger.click();
    await nextTick();
    expect(searchBox.classList.contains('expanded')).toBe(true);
    expect(document.activeElement).toBe(input);

    await search('.oa-admin-search input', 'providers');
    expect(host.querySelectorAll('.oa-admin-nav')).toHaveLength(1);
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await nextTick();
    expect(searchBox.classList.contains('expanded')).toBe(false);
    expect(host.querySelectorAll('.oa-admin-nav')).toHaveLength(1);
    expect(document.activeElement).toBe(trigger);

    trigger.click();
    await nextTick();
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await nextTick();
    expect(searchBox.classList.contains('expanded')).toBe(false);
    // Sixteen: the terminal left the backoffice for the account menu, and
    // invite codes and the leaderboard joined the rail.
    expect(host.querySelectorAll('.oa-admin-nav')).toHaveLength(16);
    expect(host.querySelector('a[href="/admin/administrators"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it('swaps one panel for another without letting the chat reflow wide', async () => {
    adopt(ACCOUNT);
    await mountAt('/settings');
    expect(host.querySelector('.oa-panel')?.classList.contains('open')).toBe(true);

    // Sibling routes, so the outgoing panel unmounts and the incoming one
    // mounts inside a single flush. The new one has to arrive already open:
    // entering from the right would show the conversation at full width for
    // the frame in between, then narrow it again.
    await router.push('/keys');
    await nextTick();

    const panel = host.querySelector('.oa-panel');
    expect(panel).not.toBeNull();
    expect(panel?.classList.contains('open')).toBe(true);
  });

  it('mounts the uptime panel when navigated to and toggles accordion cards', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/uptime');
    expect(host.querySelector('.oa-panel')).not.toBeNull();
    expect(host.querySelector('.oa-uptime-content')).not.toBeNull();

    const cards = host.querySelectorAll('.oa-uptime-card');
    expect(cards.length).toBe(2);

    const firstHeader = cards[0]?.querySelector<HTMLElement>('.oa-uptime-card-header');
    const firstAccordion = cards[0]?.querySelector('.oa-uptime-accordion');
    expect(firstAccordion?.classList.contains('open')).toBe(false);
    expect(firstHeader?.querySelector('.oa-uptime-percentage')?.textContent).toContain('50.0%');
    expect(firstHeader?.querySelector('.oa-uptime-percentage')?.textContent).not.toContain('99');
    expect(cards[1]?.querySelector('.oa-uptime-percentage')?.textContent).toContain('—');

    // Clicking header expands the card
    firstHeader?.click();
    await nextTick();
    expect(firstAccordion?.classList.contains('open')).toBe(true);
    expect(firstAccordion?.querySelector('.oa-uptime-card-stats')?.textContent).toContain('99.0%');

    // Clicking again collapses it
    firstHeader?.click();
    await nextTick();
    expect(firstAccordion?.classList.contains('open')).toBe(false);

    // Expand all button
    const actionBtns = host.querySelectorAll<HTMLButtonElement>('.oa-uptime-action-btn');
    const expandAllBtn = actionBtns[0];
    const collapseAllBtn = actionBtns[1];

    expandAllBtn?.click();
    await nextTick();
    const accordions = host.querySelectorAll('.oa-uptime-accordion.open');
    expect(accordions.length).toBe(2);

    // Collapse all button
    collapseAllBtn?.click();
    await nextTick();
    const closedAccordions = host.querySelectorAll('.oa-uptime-accordion.open');
    expect(closedAccordions.length).toBe(0);
  });

  it('opens a challenge before sending a redemption code when the operator requires it', async () => {
    window.turnstile = {
      render: vi.fn(() => 'redeem-widget'),
      reset: vi.fn(),
      remove: vi.fn(),
    };
    site.value = {
      ...siteInfo.value,
      turnstile_site_key: 'test-site-key',
      turnstile_on_redeem: true,
    };
    adopt(ACCOUNT);
    await mountAt('/usage');

    host.querySelector<HTMLButtonElement>('.oa-card-head button')!.click();
    await nextTick();
    const input = host.querySelector<HTMLInputElement>('.oa-redeem-row input')!;
    input.value = 'HUMAN';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    host.querySelector<HTMLButtonElement>('.oa-redeem-row button')!.click();
    await nextTick();
    await new Promise((resolve) => requestAnimationFrame(resolve));

    expect(document.querySelector('.oa-modal-overlay')).not.toBeNull();
    expect(document.querySelector('.oa-modal-card .oa-auth-title')?.textContent)
      .toBe(t('redeemChallengeTitle'));
    const sent = vi.mocked(fetch).mock.calls.some(([request, options]) =>
      String(request).includes('/api/usage/redeem') && options?.method === 'POST');
    expect(sent).toBe(false);
  });

  it('mounts the admin availability section when navigated to', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/availability');

    const body = host.querySelector('.oa-admin-body');
    expect(body).not.toBeNull();
    await search('#secDegradationPolicy input', '77');

    const probeButton = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find(
      (button) => button.textContent?.trim() === t('probeAllModels'),
    );
    expect(probeButton).not.toBeUndefined();
    probeButton?.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(probeButton?.textContent?.trim()).toBe(
      t('probeAllModelsProgress', { completed: 1, total: 2 }),
    );

    await new Promise((resolve) => setTimeout(resolve, 60));
    await new Promise((resolve) => setTimeout(resolve, 0));

    const calls = vi.mocked(globalThis.fetch).mock.calls;
    expect(calls.some(([input]) => String(input).includes('/api/admin/health/probe'))).toBe(true);
    expect(body?.textContent).toContain(t('probeAllModelsDone', { total: 2, succeeded: 1, failed: 1 }));
    expect(host.querySelector<HTMLInputElement>('#secDegradationPolicy input')?.value).toBe('77');
    expect(host.querySelector('.oa-control-save-state')?.textContent).toContain(t('controlUnsaved'));
    expect(calls.filter(([input, init]) => String(input).endsWith('/api/admin/settings') && init?.method === 'GET')).toHaveLength(1);
  });

  it('moves each backoffice section with its heading and keeps navigation in place', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin/providers');

    const first = host.querySelector('.oa-admin-main');
    const rail = host.querySelector('.oa-admin-rail');
    expect(first).not.toBeNull();

    // Two steps in the same direction. A CSS animation runs when its class
    // arrives, so `enter-forward` landing on a node that already carries it
    // is not an arrival — the element itself has to be new.
    await router.push('/admin/models');
    await new Promise((resolve) => setTimeout(resolve, 0));
    const second = host.querySelector('.oa-admin-main');

    await router.push('/admin/usage');
    await new Promise((resolve) => setTimeout(resolve, 0));
    const third = host.querySelector('.oa-admin-main');

    expect(second).not.toBe(first);
    expect(third).not.toBe(second);
    expect(third?.classList.contains('enter-forward')).toBe(true);
    expect(third?.contains(host.querySelector('.oa-admin-head'))).toBe(true);
    expect(host.querySelector('.oa-admin-rail')).toBe(rail);

    await router.push('/admin/providers');
    await new Promise((resolve) => setTimeout(resolve, 0));
    const back = host.querySelector('.oa-admin-main');
    expect(back?.classList.contains('enter-back')).toBe(true);
    expect(back?.querySelector('.oa-admin-actions button')).not.toBeNull();
    expect(host.querySelectorAll('.oa-admin-actions')).toHaveLength(1);

    await router.push('/admin/providers#provider-list');
    await nextTick();
    expect(host.querySelector('.oa-admin-main')).toBe(back);
  });

  it('leaves out the trial transcript when there is no trial', async () => {
    // `.oa-landing-thread` is `flex: 1`, so an empty one absorbs the column
    // and pushes the notice and the way in to the bottom of the window.
    site.value = {
      ...siteInfo.value,
      registration_enabled: true,
      landing: { mode: 'chat', intro: '', trial: false, trial_turns: 0 },
    };
    await mountAt('/');

    expect(host.querySelector('.oa-landing-chat')).not.toBeNull();
    expect(host.querySelector('.oa-landing-thread')).toBeNull();
    expect(host.querySelector('.oa-landing-entry')).not.toBeNull();
  });

  it('collapses the conversation rail from the header, and remembers nothing else', async () => {
    adopt(ACCOUNT);
    await mountAt('/');

    const row = host.querySelector('.ai-chat')!;
    const toggle = host.querySelector<HTMLButtonElement>('.oa-header-leading .oa-icon-btn')!;
    const before = row.classList.contains('rail-collapsed');

    toggle.click();
    await nextTick();
    expect(row.classList.contains('rail-collapsed')).toBe(!before);

    toggle.click();
    await nextTick();
    expect(row.classList.contains('rail-collapsed')).toBe(before);
  });

  it('gives the backoffice the same pair of controls, beside the title', async () => {
    adopt({ ...ACCOUNT, role: 'super_admin' });
    await mountAt('/admin');

    // Both live in the header now. The way out used to sit inside the rail,
    // which is the one place it could not be reached from once the rail could
    // be slid away.
    const leading = host.querySelector('.oa-header-leading')!;
    expect(leading.querySelector('.oa-admin-rail-toggle')).not.toBeNull();
    expect(leading.querySelector('.oa-admin-back')).not.toBeNull();
    expect(host.querySelector('.oa-admin-rail-head .oa-admin-back')).toBeNull();

    const row = host.querySelector('.oa-admin')!;
    const toggle = host.querySelector<HTMLButtonElement>('.oa-admin-rail-toggle')!;
    const before = row.classList.contains('rail-collapsed');

    toggle.click();
    await nextTick();
    expect(row.classList.contains('rail-collapsed')).toBe(!before);

    toggle.click();
    await nextTick();
    expect(row.classList.contains('rail-collapsed')).toBe(before);
  });

  it('still draws the transcript when the trial is live', async () => {
    site.value = {
      ...siteInfo.value,
      landing: { mode: 'chat', intro: '', trial: true, trial_turns: 3 },
    };
    await mountAt('/');

    expect(host.querySelector('.oa-landing-thread')).not.toBeNull();
    expect(host.querySelector('.oa-landing-composer')).not.toBeNull();
  });
});
