// The check-in section when the first read fails. There is no status to draw
// the buttons from, so only the reason is left to say something: it has to be
// on the page, in the alert role, and not hidden along with a section that
// never appeared.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h, nextTick, type App } from 'vue';
import { ApiError, api } from '@/api/client';
import OaCheckin from '@/components/OaCheckin.vue';
import { changeLanguage, t } from '@/composables/useI18n';

vi.mock('@/api/client', () => ({
  ApiError: class ApiError extends Error {
    readonly status: number;
    readonly code: string;
    constructor(status: number, code: string, message: string) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
      this.code = code;
    }
  },
  api: { get: vi.fn(), post: vi.fn() },
}));

const get = vi.mocked(api.get);

let app: App | null = null;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.clearAllMocks();
});

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

describe('OaCheckin when its first read fails', () => {
  it('says why, in the alert role, instead of drawing nothing', async () => {
    get.mockRejectedValue(new Error('network down'));
    app = createApp(defineComponent({ render: () => h(OaCheckin) }));
    app.mount(host);
    await settle();

    const alert = host.querySelector('[role="alert"]');
    expect(alert?.textContent).toBe(t('checkinLoadFailed'));
    expect(host.querySelector('.oa-collapsible')).not.toBeNull();
    expect(host.querySelector('.oa-checkin-day')).toBeNull();
  });

  it('says a server fault in Chinese with the screen\'s own line, never the server sentence', async () => {
    await changeLanguage('zh');
    get.mockRejectedValue(new ApiError(500, 'internal', 'Something went wrong on our side.'));
    app = createApp(defineComponent({ render: () => h(OaCheckin) }));
    app.mount(host);
    await settle();

    const alert = host.querySelector('[role="alert"]');
    expect(alert?.textContent).toBe(t('checkinLoadFailed'));
    expect(host.textContent).not.toContain('Something went wrong');
    await changeLanguage('en');
  });

  it('draws nothing when the read succeeds and check-in is switched off', async () => {
    get.mockResolvedValue({ enabled: false });
    app = createApp(defineComponent({ render: () => h(OaCheckin) }));
    app.mount(host);
    await settle();

    expect(host.querySelector('.oa-collapsible')).toBeNull();
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });
});
