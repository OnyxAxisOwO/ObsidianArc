// The announcements table shows the start of each body on its second line. The
// cut is by character, so one that falls inside an emoji does not leave half of
// it behind: a lone surrogate renders as a replacement glyph.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { adminApi } from '../src/admin/api';
import type { Announcement } from '../src/api/announcements';
import { changeLanguage } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import AdminAnnouncements from '../src/views/admin/AdminAnnouncements.vue';
import { provideAdminView } from '../src/views/admin/adminView';

const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  document.body.append(host, panels);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  panels.remove();
  vi.restoreAllMocks();
});

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

function announcement(body: string): Announcement {
  return {
    id: 'a1', title: 'Maintenance', body, display_mode: 'always', dismiss_after_seconds: 0,
    published: true, pinned: false, created_at: 0, updated_at: 0, read: false,
  };
}

async function mountPage(): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/');
  await router.isReady();
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
      return () => h(AdminAnnouncements);
    },
  });
  app.use(router);
  app.mount(host);
  await settle();
}

describe('the second line of an announcement row', () => {
  it('does not leave half an emoji at the end of the cut', async () => {
    // 59 letters and an emoji: counted in UTF-16 units the cut at 60 lands
    // inside the emoji.
    const body = `${'a'.repeat(59)}😀b`;
    vi.spyOn(adminApi, 'announcements').mockResolvedValue({ announcements: [announcement(body)] });
    await mountPage();

    expect(host.textContent).toContain(`${'a'.repeat(59)}😀…`);
    expect(host.textContent ?? '').not.toMatch(LONE_SURROGATE);
  });
});
