// A ranked board hides its scrollbar, so the rows past the visible ones can be
// reached with the wheel only — unless something in the scroller can take
// focus. Keyed rows are buttons and take focus themselves; plain rows do not,
// and then the scroller has to.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import type { UsageBreakdown } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import UsageBoard from '../src/views/admin/usage/UsageBoard.vue';

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
});

function row(key: string, requests: number): UsageBreakdown {
  return {
    key, label: key, last_at: 0, requests, input_tokens: 1, output_tokens: 1, reasoning_tokens: 0,
    total_tokens: 2, credits: 0, errors: 0, users: 1, models: 1, duration_ms: 0,
  };
}

const rows = [row('alpha', 50), row('beta', 40), row('gamma', 30), row('delta', 20), row('epsilon', 10)];

// Not mountComponent: the tests use the screen the moment it is mounted, without awaiting, and read its first render.
function mount(over: { selectable: boolean; rows?: UsageBreakdown[]; limit?: number }): HTMLElement {
  app = createApp({
    render: () => h(UsageBoard, {
      rows: over.rows ?? rows,
      kind: 'model',
      metric: 'requests',
      limit: over.limit ?? 2,
      selectable: over.selectable,
      emptyText: 'none',
    }),
  });
  app.mount(host);
  return host.querySelector<HTMLElement>('.oa-board-scroll')!;
}

describe('the scroller of a ranked board', () => {
  it('takes focus when its rows are plain, so the rows past the limit are reachable', async () => {
    const scroller = mount({ selectable: false });
    await nextTick();

    expect(scroller.getAttribute('tabindex')).toBe('0');
    expect(scroller.getAttribute('role')).toBe('region');
    expect(scroller.getAttribute('aria-label')).toBe(t('boardCount', { count: rows.length }));
    expect(host.querySelectorAll('button.oa-board-row')).toHaveLength(0);
  });

  it('leaves focus to the keyed rows when every row is a button', async () => {
    const scroller = mount({ selectable: true });
    await nextTick();

    expect(scroller.hasAttribute('tabindex')).toBe(false);
    expect(host.querySelectorAll('button.oa-board-row')).toHaveLength(rows.length);
  });

  it('does not take focus when nothing is hidden behind it', async () => {
    const scroller = mount({ selectable: false, limit: rows.length });
    await nextTick();

    expect(scroller.hasAttribute('tabindex')).toBe(false);
  });
});
