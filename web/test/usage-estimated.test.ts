// Token figures the provider never reported are shown as the estimates they
// are. Without the mark, a reader compares a guess from one provider against
// a count from another and concludes one of them is broken — which is how a
// gateway that reports no usage came to look like "a free model has no
// tokens".

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { api } from '@/api/client';
import { providePanelHost } from '@/composables/usePanelHost';
import { tokenFigure } from '@/lib/format';
import UsagePanel from '@/views/UsagePanel.vue';

vi.mock('@/api/client', () => ({
  ApiError: class ApiError extends Error {},
  api: { get: vi.fn(), post: vi.fn() },
}));
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }));

let app: App | null = null;

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.clearAllMocks();
  vi.useRealTimers();
});

function turn(id: string, tokens: number, estimated: boolean) {
  return {
    id, model_name: `model-${id}`, input_tokens: tokens, output_tokens: 0, reasoning_tokens: 0,
    total_tokens: tokens, credits: 0, status: 'ok', started_at: Date.now(), finished_at: Date.now(),
    ...(estimated ? { estimated: true } : {}),
  };
}

it('marks an estimated figure and leaves a counted one alone', () => {
  expect(tokenFigure(1234, true)).toBe('≈1.2k');
  expect(tokenFigure(1234, false)).toBe('1.2k');
  expect(tokenFigure(1234)).toBe('1.2k');
});

it('shows the mark on the turns the provider never counted', async () => {
  vi.mocked(api.get).mockImplementation(async (url: string) => {
    if (url === '/api/usage/me') return { unlimited: true, windows: [] };
    if (url === '/api/usage/cards') return { cards: [] };
    if (url === '/api/bonus') return { bars: [] };
    if (url === '/api/checkin') return { enabled: false };
    return {
      totals: { requests: 2, input_tokens: 0, output_tokens: 0, reasoning_tokens: 0, total_tokens: 0, credits: 0, errors: 0 },
      turns: [turn('counted', 1500, false), turn('guessed', 2500, true)],
    };
  });

  const panelHost = document.createElement('div');
  const host = document.createElement('div');
  document.body.append(panelHost, host);
  app = createApp({ setup() { providePanelHost(ref(panelHost)); }, render: () => h(UsagePanel) });
  app.mount(host);
  await vi.advanceTimersByTimeAsync(0);
  await nextTick();

  const text = document.body.textContent ?? '';
  expect(text).toContain('≈2.5k');
  expect(text).toContain('1.5k');
  expect(text).not.toContain('≈1.5k');
});
