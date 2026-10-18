import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import UsageAllowances from '../src/views/admin/usage/UsageAllowances.vue';
import { adminApi, type AllowanceList, type AllowanceRow } from '../src/admin/api';
import { t } from '../src/composables/useI18n';
import { settle } from './helpers';
import { queriesSent } from './admin-query';

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

const answer = (rows: AllowanceRow[]): AllowanceList => ({
  rows, total: rows.length, listed: rows.length, loaded: rows.length, capped: false, display: 'remaining', low: 1, exhausted: 1,
});


describe('UsageAllowances', () => {
  it('draws one row per account, a window with no limit as such, and asks again when the state changes', async () => {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [{ id: 'g1', name: 'Pro' }] });
    const ask = vi.spyOn(adminApi, 'usageAllowances')
      .mockResolvedValueOnce(answer([row('gone', 1), row('free', null)]))
      .mockResolvedValueOnce(answer([row('gone', 1)]));

    app = createApp({ render: () => h(UsageAllowances, { groupId: '' }) });
    app.mount(host);
    await settle();

    expect(host.querySelectorAll('.oa-allow-row').length).toBe(2);
    expect(host.querySelector('.oa-allow-row')?.textContent).toContain('@gone · Pro');
    // The account with nothing enforced reads "no limit" three times over,
    // and gets no bar at all.
    const free = host.querySelectorAll('.oa-allow-row')[1]!;
    expect(free.querySelectorAll('.oa-allow-none').length).toBe(3);
    expect(free.querySelector('.oa-meter')).toBeNull();
    expect(queriesSent(ask)[0]).toBe('limit=50&offset=0');

    const chips = [...host.querySelectorAll<HTMLButtonElement>('.oa-segment button')];
    expect(chips.length).toBe(3);
    chips[2]!.click();
    await settle();
    expect(queriesSent(ask)[1]).toContain('state=exhausted');
    expect(host.querySelectorAll('.oa-allow-row').length).toBe(1);
  });

  it('says when the list is only the newest accounts, and gives the real total', async () => {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] } as never);
    vi.spyOn(adminApi, 'usageAllowances').mockResolvedValueOnce({
      ...answer([row('a', 1), row('b', 0.5)]), total: 7000, listed: 2, loaded: 2, capped: true,
    });

    app = createApp({ render: () => h(UsageAllowances, { groupId: '' }) });
    app.mount(host);
    await settle();

    // The count on the board is the real number of accounts, and the note says
    // the ranking covers only the ones that were loaded.
    const note = host.querySelector('.oa-board-count')?.textContent ?? '';
    expect(note).toBe(t('allowanceCapped', { count: 2, total: 7000 }));
  });

  // A state filter past the cap is counted over the loaded accounts only, so the
  // note must not present that count as the population, and must say how many
  // accounts it was drawn from.
  it('words a filtered count past the cap as matches among the loaded accounts', async () => {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] } as never);
    const ask = vi.spyOn(adminApi, 'usageAllowances')
      .mockResolvedValueOnce({ ...answer([row('a', 1)]), total: 7000, listed: 1, loaded: 5000, capped: true })
      .mockResolvedValueOnce({ ...answer([row('a', 1)]), total: 1, listed: 1, loaded: 5000, capped: true });

    app = createApp({ render: () => h(UsageAllowances, { groupId: '' }) });
    app.mount(host);
    await settle();

    const chips = [...host.querySelectorAll<HTMLButtonElement>('.oa-segment button')];
    chips[2]!.click();
    await settle();
    expect(queriesSent(ask)[1]).toContain('state=exhausted');
    const note = host.querySelector('.oa-board-count')?.textContent ?? '';
    expect(note).toBe(t('allowanceCappedMatching', { count: 1, loaded: 5000 }));
    expect(note).not.toContain(t('allowanceCapped', { count: 1, total: 1 }));
  });

  // A next page that failed leaves the rows up. Every scroll event near the end
  // used to ask for the same page again, so the reader gets a retry instead,
  // and scrolling waits for it.
  it('stops asking for a next page that failed until the reader retries it', async () => {
    vi.spyOn(adminApi, 'groupOptions').mockResolvedValue({ groups: [] } as never);
    const page = (prefix: string) => Array.from({ length: 50 }, (_, index) => row(`${prefix}${index}`, 0.5));
    const ask = vi.spyOn(adminApi, 'usageAllowances')
      .mockResolvedValueOnce({ ...answer(page('a')), total: 120, listed: 120 })
      .mockRejectedValueOnce(new Error('upstream down'))
      .mockResolvedValueOnce({ ...answer(page('b')), total: 120, listed: 120 });

    app = createApp({ render: () => h(UsageAllowances, { groupId: '' }) });
    app.mount(host);
    await settle();
    // The scroller is the element the rows sit in; a scroll event on it is what
    // the reader's wheel or drag produces.
    const scroller = () => host.querySelector<HTMLElement>('.oa-board')!.parentElement!;

    scroller().dispatchEvent(new Event('scroll'));
    await settle();
    expect(ask).toHaveBeenCalledTimes(2);
    expect(queriesSent(ask)[1]).toBe('limit=50&offset=50');
    expect(host.querySelectorAll('.oa-allow-row').length).toBe(50);
    expect(host.textContent).toContain(t('couldNotLoad'));

    for (let i = 0; i < 5; i++) scroller().dispatchEvent(new Event('scroll'));
    await settle();
    expect(ask).toHaveBeenCalledTimes(2);

    const retry = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === t('tryAgain'))!;
    retry.click();
    await settle();
    expect(ask).toHaveBeenCalledTimes(3);
    expect(host.querySelectorAll('.oa-allow-row').length).toBe(100);
    expect(host.textContent).not.toContain(t('couldNotLoad'));
  });
});
