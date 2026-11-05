// The check-in on the usage panel. The server decides everything — whether it
// is today's first check-in, whether a milestone is reached — so what these
// tests hold is that the buttons are drawn from what it says and that a
// press goes to the right endpoint and tells the host to read again.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/api/client';
import type { CheckinStatus } from '@/api/checkin';
import OaCheckin from '@/components/OaCheckin.vue';
import { changeLanguage, t } from '@/composables/useI18n';
import { mountComponent, settle, button } from './helpers';

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
const post = vi.mocked(api.post);

let unmount: (() => void) | undefined;
let host: HTMLElement;
let changed = 0;

beforeEach(async () => {
  await changeLanguage('en');
  localStorage.clear();
  changed = 0;
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount?.();
  unmount = undefined;
  vi.clearAllMocks();
});

function status(over: Partial<CheckinStatus> = {}): CheckinStatus {
  return {
    enabled: true, today: '2030-02-10', checked_in_today: false, streak: 3,
    month_days: [8, 9], month_count: 2, daily: { kind: '' }, rules: [],
    ...over,
  };
}


async function mount(): Promise<void> {
  // No panel host: the card opens no panel, so one starting to would fail here.
  unmount = await mountComponent(OaCheckin, { onChanged: () => { changed += 1; } }, { host, panels: null });
}

function serve(...answers: CheckinStatus[]): void {
  let next = 0;
  get.mockImplementation(async (url: string) => {
    if (url !== '/api/checkin') throw new Error(`unexpected ${url}`);
    return answers[Math.min(next++, answers.length - 1)]!;
  });
}

describe('OaCheckin', () => {
  it('draws nothing at all while the administrator has it switched off', async () => {
    serve(status({ enabled: false }));
    await mount();
    expect(host.querySelector('.oa-checkin')).toBeNull();
    expect(host.textContent).toBe('');
  });

  it('draws the month, marking the days checked in and today', async () => {
    serve(status());
    await mount();
    const days = [...host.querySelectorAll('.oa-checkin-day')];
    // February 2030 has 28 days.
    expect(days).toHaveLength(28);
    expect(days.filter((day) => day.classList.contains('on')).map((day) => day.textContent)).toEqual(['8', '9']);
    expect(days.find((day) => day.classList.contains('today'))?.textContent).toBe('10');
    expect(host.textContent).toContain(t('checkinStreak', { count: 3 }));
  });

  it('checks in from the heading while the section is folded, and opens it for a refusal without changing what was kept', async () => {
    localStorage.setItem('obsidian-arc-fold-usage-checkin', '1');
    const { ApiError } = await import('@/api/client');
    serve(status(), status({ checked_in_today: true }));
    post.mockRejectedValue(new ApiError(409, 'already_checked_in', 'Already checked in today.'));
    await mount();
    const toggle = host.querySelector<HTMLButtonElement>('.oa-collapsible-toggle')!;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');

    button(host, t('checkinNow')).click();
    await settle();

    expect(post).toHaveBeenCalledWith('/api/checkin', {});
    // The reason is inside the section, so the section opens to show it — for now.
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(host.textContent).toContain('Already checked in today.');
    expect(localStorage.getItem('obsidian-arc-fold-usage-checkin')).toBe('1');
  });

  it('turns the button off once today is done', async () => {
    serve(status({ checked_in_today: true }));
    await mount();
    const done = button(host, t('checkinDone'));
    expect(done.disabled).toBe(true);
    expect(host.textContent).not.toContain(t('checkinNow'));
  });

  it('checks in, says what it earned, and tells the host to read again', async () => {
    serve(
      status({ daily: { kind: 'bonus', bar_id: 'b1', amount: 0.5, valid_days: 7 } }),
      status({ checked_in_today: true, streak: 4, month_days: [8, 9, 10], month_count: 3 }),
    );
    post.mockResolvedValue({
      day: '2030-02-10', streak: 4, reward_failed: false,
      reward: { kind: 'bonus', bar_id: 'b1', amount: 0.5, valid_days: 7 },
    });
    await mount();
    expect(host.textContent).toContain(t('checkinDaily', { reward: t('checkinRewardBonus', { amount: '0.5', days: 7 }) }));

    button(host, t('checkinNow')).click();
    await settle();

    expect(post).toHaveBeenCalledWith('/api/checkin', {});
    expect(host.querySelector('[role="status"]')?.textContent)
      .toContain(t('checkinGot', { streak: 4, reward: t('checkinRewardBonus', { amount: '0.5', days: 7 }) }));
    expect(button(host, t('checkinDone')).disabled).toBe(true);
    expect(changed).toBe(1);
  });

  it('says so, rather than claiming a reward, when the check-in counted but the reward did not arrive', async () => {
    serve(status(), status({ checked_in_today: true }));
    post.mockResolvedValue({ day: '2030-02-10', streak: 4, reward_failed: true, reward: { kind: 'bonus' } });
    await mount();

    button(host, t('checkinNow')).click();
    await settle();

    expect(host.querySelector('[role="status"]')?.textContent).toBe(t('checkinRewardFailed'));
  });

  it('reads again after a refusal, because the answer is usually that somebody else already checked in', async () => {
    const { ApiError } = await import('@/api/client');
    serve(status(), status({ checked_in_today: true }));
    post.mockRejectedValue(new ApiError(409, 'already_checked_in', 'Already checked in today.'));
    await mount();

    button(host, t('checkinNow')).click();
    await settle();

    expect(host.textContent).toContain('Already checked in today.');
    expect(get).toHaveBeenCalledTimes(2);
    expect(button(host, t('checkinDone')).disabled).toBe(true);
    expect(changed).toBe(0);
  });

  it('offers the claim only on a milestone that can be claimed, and claims the one pressed', async () => {
    serve(
      status({
        rules: [
          { id: 'week', title: 'A week', basis: 'streak', days: 7, progress: 7, claimable: true, claimed: false,
            reward: { kind: 'card', name: 'Reset', windows: [], cards: 2, valid_days: 30 } },
          { id: 'month', title: '', basis: 'month', days: 20, progress: 2, claimable: false, claimed: false,
            reward: { kind: 'bonus', bar_id: 'b1', amount: 5 } },
          { id: 'done', title: 'Done', basis: 'streak', days: 3, progress: 3, claimable: false, claimed: true,
            reward: { kind: 'bonus', bar_id: 'b1', amount: 1, valid_days: 3 } },
        ],
      }),
      status(),
    );
    post.mockResolvedValue({ reward: { kind: 'card', cards: 2, valid_days: 30 } });
    await mount();

    const rows = [...host.querySelectorAll('.oa-checkin-milestone')];
    expect(rows).toHaveLength(3);
    expect(rows[0]!.querySelector('button')!.disabled).toBe(false);
    expect(rows[1]!.querySelector('button')!.disabled).toBe(true);
    expect(rows[2]!.querySelector('button')!.textContent).toBe(t('checkinClaimed'));
    // An untitled milestone is described by what it counts.
    expect(rows[1]!.textContent).toContain(t('checkinRuleMonth', { days: 20 }));

    rows[0]!.querySelector('button')!.click();
    await settle();

    expect(post).toHaveBeenCalledWith('/api/checkin/claims/week', {});
    expect(host.querySelector('[role="status"]')?.textContent)
      .toContain(t('checkinClaimedNotice', { reward: t('checkinRewardCard', { count: 2, days: 30 }) }));
    expect(changed).toBe(1);
  });

  it('says so when the status cannot be read', async () => {
    get.mockRejectedValue(new Error('offline'));
    await mount();
    // Nothing to draw a button from: an error line would sit under no heading,
    // so the component stays quiet and the panel around it is unaffected.
    expect(host.querySelector('.oa-checkin')).toBeNull();
  });
});
