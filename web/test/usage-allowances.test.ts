import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import UsageAllowances from '../src/views/admin/usage/UsageAllowances.vue';
import { adminApi, type AllowanceList, type AllowanceRow } from '../src/admin/api';

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
});
afterEach(() => {
  app?.unmount();
  app = null;
  vi.restoreAllMocks();
});

function row(id: string, pressure: number | null): AllowanceRow {
  const used = pressure === null ? 0 : Math.round(pressure * 100);
  return {
    id, username: id, nickname: '', group_id: 'g1', last_active_at: 0, pressure, unlimited: pressure === null,
    windows: (['5h', '1w', '1m'] as const).map((kind) => ({
      kind, enforced: pressure !== null && kind !== '1m',
      used_requests: 0, used_tokens: used, used_credits: 0,
      limit_requests: null, limit_tokens: 100, limit_credits: null, resets_at: Date.now() + 3600_000,
    })),
  };
}

const answer = (rows: AllowanceRow[]): AllowanceList => ({ rows, total: rows.length, display: 'remaining', low: 1, exhausted: 1 });

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i++) { await Promise.resolve(); await nextTick(); }
}

describe('UsageAllowances', () => {
  it('draws one row per account, a window with no limit as such, and asks again when the state changes', async () => {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [{ id: 'g1', name: 'Pro' }] } as never);
    const ask = vi.spyOn(adminApi, 'usageAllowances')
      .mockResolvedValueOnce(answer([row('gone', 1), row('free', null)]))
      .mockResolvedValueOnce(answer([row('gone', 1)]));

    app = createApp({ render: () => h(UsageAllowances as never, { groupId: '' }) });
    app.mount(host);
    await settle();

    expect(host.querySelectorAll('.oa-allow-row').length).toBe(2);
    expect(host.querySelector('.oa-allow-row')?.textContent).toContain('@gone · Pro');
    // The account with nothing enforced reads "no limit" three times over,
    // and gets no bar at all.
    const free = host.querySelectorAll('.oa-allow-row')[1]!;
    expect(free.querySelectorAll('.oa-allow-none').length).toBe(3);
    expect(free.querySelector('.oa-meter')).toBeNull();
    expect(ask.mock.calls[0]![0]).toBe('limit=50&offset=0');

    const chips = [...host.querySelectorAll<HTMLButtonElement>('.oa-segment button')];
    expect(chips.length).toBe(3);
    chips[2]!.click();
    await settle();
    expect(ask.mock.calls[1]![0]).toContain('state=exhausted');
    expect(host.querySelectorAll('.oa-allow-row').length).toBe(1);
  });
});
