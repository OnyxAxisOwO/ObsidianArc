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
  group_name: 'Default',
  status: 'active',
  created_at: Date.now(),
  updated_at: Date.now(),
  last_login_at: Date.now(),
  email_verified: true,
  allow_stats: true,
  allow_delete_conversations: true,
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
  [/\/api\/health/, { status: 'ok', version: 'vtest', uptime_sec: 1 }],
  [/\/api\/announcements/, { announcements: [], unread: 0, popup: null }],
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
  app?.unmount();
  app = null;
  host.remove();
  document.body.textContent = '';
  forget();
  vi.unstubAllGlobals();
});

describe('the application, mounted', () => {
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
    adopt({ ...ACCOUNT, role: 'admin' });
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
    expect(shown('.oa-range-field')).toHaveLength(4);
    await search('.oa-settings-search input', 'nothing-matches-this');
    expect(shown('.oa-settings-panel')).toHaveLength(0);
    expect(host.querySelector('.oa-settings .oa-search-empty')?.textContent).toBe(t('noSearchResults'));
    await search('.oa-settings-search input', 'nickname');
    expect(shown('.oa-settings-panel')[0]!.querySelector<HTMLInputElement>('input')).toBe(nickname);
    expect(nickname.value).toBe('Unsaved nickname');
    host.querySelector<HTMLButtonElement>('.oa-settings-search button')!.click();
    await nextTick();
    expect(shown('.oa-settings-panel')).toHaveLength(2);
    expect(shown('.oa-range-field')).toHaveLength(4);
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
    expect(shown('.oa-range-field')).toHaveLength(4);
    expect(host.querySelector<HTMLInputElement>('.oa-settings-search input')?.placeholder).toBe('搜索设置');
    await changeLanguage('en');
  });

  it('filters admin navigation and instance settings while retaining unsaved values', async () => {
    await changeLanguage('en');
    adopt({ ...ACCOUNT, role: 'admin' });
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
    expect(shown('.oa-admin-body section')).toHaveLength(7);
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
    adopt({ ...ACCOUNT, role: 'admin' });
    await mountAt('/uptime');
    expect(host.querySelector('.oa-panel')).not.toBeNull();
    expect(host.querySelector('.oa-uptime-content')).not.toBeNull();

    const cards = host.querySelectorAll('.oa-uptime-card');
    expect(cards.length).toBe(2);

    const firstHeader = cards[0]?.querySelector<HTMLElement>('.oa-uptime-card-header');
    const firstAccordion = cards[0]?.querySelector('.oa-uptime-accordion');
    expect(firstAccordion?.classList.contains('open')).toBe(false);

    // Clicking header expands the card
    firstHeader?.click();
    await nextTick();
    expect(firstAccordion?.classList.contains('open')).toBe(true);

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

  it('mounts the admin availability section when navigated to', async () => {
    adopt({ ...ACCOUNT, role: 'admin' });
    await mountAt('/admin/availability');

    const body = host.querySelector('.oa-admin-body');
    expect(body).not.toBeNull();
  });

  it('gives each backoffice section a fresh body, so its entry actually plays', async () => {
    adopt({ ...ACCOUNT, role: 'admin' });
    await mountAt('/admin/providers');

    const first = host.querySelector('.oa-admin-body');
    expect(first).not.toBeNull();

    // Two steps in the same direction. A CSS animation runs when its class
    // arrives, so `enter-forward` landing on a node that already carries it
    // is not an arrival — the element itself has to be new.
    await router.push('/admin/models');
    await new Promise((resolve) => setTimeout(resolve, 0));
    const second = host.querySelector('.oa-admin-body');

    await router.push('/admin/usage');
    await new Promise((resolve) => setTimeout(resolve, 0));
    const third = host.querySelector('.oa-admin-body');

    expect(second).not.toBe(first);
    expect(third).not.toBe(second);
    expect(third?.classList.contains('enter-forward')).toBe(true);
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
    adopt({ ...ACCOUNT, role: 'admin' });
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
