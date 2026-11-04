// The admin search index opens a section by its id and finds it with
// document.getElementById. An entry the index names that the page does not
// draw with that id scrolls nowhere, so each one must have an element carrying
// its id once the page is mounted.
//
// The page is mounted rather than its source searched: an id in a comment, or
// in a v-if branch that never renders, would pass a text search, and an id
// bound through :id= would fail one while it renders correctly.
//
// A page draws into three places — its body, the panel column and the actions
// strip — and the search looks in the whole document, so the check does too.
// Some sections are drawn only inside the side panel the page opens, so the
// check opens that panel first, with the data its v-if branches need.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi, emptyPolicy, type Account } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import AdminGroups from '../src/views/admin/AdminGroups.vue';
import AdminProviders from '../src/views/admin/AdminProviders.vue';
import AdminUsers from '../src/views/admin/AdminUsers.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import { ADMIN_FEATURES } from '../src/views/admin/features';
import { settle } from './helpers';

type Detail = Awaited<ReturnType<typeof adminApi.user>>;

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

;

beforeEach(() => {
  host = document.createElement('div');
  panels = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panels, actions);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  panels.remove();
  actions.remove();
  vi.restoreAllMocks();
});

function mountPage(page: Component): void {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({ actionsHost: actions, setTitle() {}, reload() {}, params: [] });
      return () => h(page);
    },
  });
  app.use(router);
  app.mount(host);
}

const alpha = {
  id: 'a', username: 'user-a', email: 'a@example.test', nickname: 'Nick a', avatar: '', bio: '',
  role: 'user', group_id: '', group_expires_at: 0, group_name: '',
  status: 'active', ban_reason: '', created_at: 1, updated_at: 1, last_login_at: 0,
  email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0,
} as Account;

// The held-cards section is drawn only when the detail carries cards, so the
// detail has to supply them for that section to exist.
const detail = {
  user: alpha,
  usage: { unlimited: true, windows: [] },
  lifetime: { requests: 0, total_tokens: 0, credits: 0 },
  models: [],
  policy: emptyPolicy('user', alpha.id),
  cards: { available: 0, used: 0, expired: 0, total: 0, cards: [] },
} as unknown as Detail;

// The account panel is drawn for a row once its detail has arrived.
async function openAccountPanel(): Promise<void> {
  vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] });
  vi.spyOn(adminApi, 'users').mockResolvedValue({ users: [alpha], total: 1 });
  vi.spyOn(adminApi, 'user').mockResolvedValue(detail);
  vi.spyOn(adminApi, 'userKeys').mockResolvedValue({ keys: [] });
  vi.spyOn(adminApi, 'userSessions').mockResolvedValue({ sessions: [] });
  mountPage(AdminUsers);
  await settle();
  host.querySelector<HTMLTableRowElement>('tbody tr')!.click();
  await settle();
}

// The sections of the create-group form exist only while the panel is open.
async function openNewGroupPanel(): Promise<void> {
  vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [], policies: [] });
  vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
  mountPage(AdminGroups);
  await settle();
  actions.querySelector<HTMLButtonElement>('#addGroup')!.click();
  await settle();
}

// The provider form's sections exist only while its panel is open, and the panel
// waits for the server's list of provider kinds and reasoning styles.
async function openNewProviderPanel(): Promise<void> {
  vi.spyOn(adminApi, 'providers').mockResolvedValue({ providers: [] } as unknown as Awaited<ReturnType<typeof adminApi.providers>>);
  vi.spyOn(adminApi, 'meta').mockResolvedValue({
    provider_kinds: ['openai', 'anthropic'],
    reasoning_styles: ['auto', 'none'],
  } as unknown as Awaited<ReturnType<typeof adminApi.meta>>);
  mountPage(AdminProviders);
  await settle();
  actions.querySelector<HTMLButtonElement>('#addProvider')!.click();
  await settle();
}

function undrawn(slug: string): string[] {
  const ids = ADMIN_FEATURES.filter((item) => item.pageSlug === slug).map((item) => item.id);
  expect(ids.length).toBeGreaterThan(0);
  return ids.filter((id) => document.getElementById(id) === null);
}

describe('admin search anchors', () => {
  it('draws every entry the index names on the users page with its id', async () => {
    await openAccountPanel();
    expect(undrawn('users')).toEqual([]);
  });

  it('draws every entry the index names on the groups page with its id', async () => {
    await openNewGroupPanel();
    expect(undrawn('groups')).toEqual([]);
  });

  it('draws every entry the index names on the providers page with its id', async () => {
    await openNewProviderPanel();
    expect(undrawn('providers')).toEqual([]);
  });
});
