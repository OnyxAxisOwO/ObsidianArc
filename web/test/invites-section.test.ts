import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App, type Component } from 'vue';
import * as invitesApi from '../src/api/invites';
import type { ProfileInvites } from '../src/api/invites';
import { ApiError } from '../src/api/client';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t, tn } from '../src/composables/useI18n';
import { absoluteTime } from '../src/lib/format';
import * as markdown from '../src/chat/markdown';
import InvitesSection from '../src/views/settings/InvitesSection.vue';

// The "Invites" settings card: claiming somebody else's code (always
// present), and — when personal invites are on — an account's own code, its
// every-N reward progress, and who it has brought in.

const route = { path: '/settings', query: {} as Record<string, string> };
const router = { replace: vi.fn() };
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => router,
}));

const OFF: ProfileInvites = {
  enabled: false, code: '', limit: 0, used: 0, counted: 0,
  reward_every: 1, reward_cards: 0, reward_card_days: 0, next_reward_in: 0,
  invitees: [],
};

const ON: ProfileInvites = {
  enabled: true, code: 'ABCD2345', limit: 10, used: 1, counted: 1,
  reward_every: 5, reward_cards: 2, reward_card_days: 30, next_reward_in: 4,
  invitees: [
    {
      nickname: 'Ada', username: 'ada', created_at: Date.now(),
      counted: true, reward_cards: 0, reward_skipped: '',
    },
  ],
};

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  route.query = {};
  router.replace.mockClear();
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.restoreAllMocks();
  vi.useRealTimers();
});

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i++) {
    if (vi.isFakeTimers()) {
      await vi.advanceTimersByTimeAsync(0);
    } else {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    await nextTick();
  }
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  const panels = document.createElement('div');
  document.body.append(panels);
  app = createApp({
    setup() {
      providePanelHost(shallowRef(panels));
      return () => h(component, props);
    },
  });
  app.mount(host);
  await settle();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Button not found: ${label}`);
  return found;
}

function claimInput(): HTMLInputElement {
  const found = host.querySelector<HTMLInputElement>('input');
  if (!found) throw new Error('Claim input not found');
  return found;
}

async function type(input: HTMLInputElement, value: string): Promise<void> {
  input.value = value;
  input.dispatchEvent(new Event('input'));
  await settle();
}

describe('the invites card', () => {
  it('keeps the claim box even when the operator has personal codes off', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(OFF);
    await mount(InvitesSection);

    expect(host.textContent).toContain(t('inviteClaimTitle'));
    expect(() => button(t('inviteClaimSubmit'))).not.toThrow();
    expect(host.textContent).not.toContain(t('inviteYourCode'));
  });

  it('shows the personal code grouped as XXXX-XXXX once enabled', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    await mount(InvitesSection);

    expect(host.textContent).toContain('ABCD-2345');
  });

  it('shows the personal code and invite link, each with its own copy button and feedback', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    const copySpy = vi.spyOn(markdown, 'copyToClipboard').mockResolvedValue(true);
    await mount(InvitesSection);

    expect(host.textContent).toContain('ABCD-2345');
    const expectedLink = invitesApi.inviteLink('ABCD2345');
    expect(host.textContent).toContain(expectedLink);

    const copyButtons = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .filter((node) => node.textContent?.trim() === t('copy'));
    expect(copyButtons.length).toBe(2);

    // Copy code button
    copyButtons[0]!.click();
    await settle();
    expect(copySpy).toHaveBeenCalledWith('ABCD-2345');
    expect(copyButtons[0]!.textContent?.trim()).toBe(t('copied'));
    expect(copyButtons[0]!.classList.contains('copied')).toBe(true);
    expect(copyButtons[0]!.getAttribute('aria-label')).toBe(t('copied'));

    // Copy link button
    copyButtons[1]!.click();
    await settle();
    expect(copySpy).toHaveBeenCalledWith(expectedLink);
    expect(copyButtons[1]!.textContent?.trim()).toBe(t('copied'));
    expect(copyButtons[1]!.classList.contains('copied')).toBe(true);
    expect(copyButtons[1]!.getAttribute('aria-label')).toBe(t('copied'));
  });

  it('handles clipboard failure and clears error message on subsequent success', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    const copySpy = vi.spyOn(markdown, 'copyToClipboard').mockResolvedValue(false);
    await mount(InvitesSection);

    const copyButtons = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .filter((node) => node.textContent?.trim() === t('copy'));
    expect(copyButtons.length).toBe(2);

    // Click when clipboard fails
    copyButtons[0]!.click();
    await settle();
    expect(copySpy).toHaveBeenCalledWith('ABCD-2345');
    expect(host.textContent).toContain(t('copyFailed'));

    // Subsequent copy succeeds
    copySpy.mockResolvedValueOnce(true);
    copyButtons[0]!.click();
    await settle();
    expect(host.textContent).not.toContain(t('copyFailed'));
    expect(copyButtons[0]!.textContent?.trim()).toBe(t('copied'));
  });

  it('resets copied state back to copy label after timeout, and handles rapid clicks cleanly', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    vi.spyOn(markdown, 'copyToClipboard').mockResolvedValue(true);
    await mount(InvitesSection);

    const copyButtons = [...host.querySelectorAll<HTMLButtonElement>('button')]
      .filter((node) => node.textContent?.trim() === t('copy'));
    expect(copyButtons.length).toBe(2);

    vi.useFakeTimers();
    try {
      // First click
      copyButtons[0]!.click();
      await vi.advanceTimersByTimeAsync(10);
      expect(copyButtons[0]!.textContent?.trim()).toBe(t('copied'));

      // Rapid second click after 500ms
      await vi.advanceTimersByTimeAsync(500);
      copyButtons[0]!.click();
      await vi.advanceTimersByTimeAsync(10);
      expect(copyButtons[0]!.textContent?.trim()).toBe(t('copied'));

      // Advance by 1100ms (1600ms from first click, but only 1100ms from second click)
      await vi.advanceTimersByTimeAsync(1100);
      expect(copyButtons[0]!.textContent?.trim()).toBe(t('copied'));

      // Advance remaining time to pass 1500ms from second click
      await vi.advanceTimersByTimeAsync(500);
      expect(copyButtons[0]!.textContent?.trim()).toBe(t('copy'));
    } finally {
      vi.useRealTimers();
    }
  });

  it('lists usage against the limit, and as unlimited when there is none', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    await mount(InvitesSection);
    expect(host.textContent).toContain(t('inviteUsageLimited', { used: 1, limit: 10 }));

    app?.unmount();
    host.textContent = '';
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue({ ...ON, limit: 0 });
    await mount(InvitesSection);
    expect(host.textContent).toContain(t('inviteUsageUnlimited', { used: 1 }));
  });

  it('pre-fills the claim box from ?claim=, without submitting it', async () => {
    route.query = { claim: 'PARTNER1' };
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(OFF);
    const claim = vi.spyOn(invitesApi, 'claimInviteCode');
    await mount(InvitesSection);

    expect(claimInput().value).toBe('PARTNER1');
    expect(claim).not.toHaveBeenCalled();
  });

  it('claims a code and names the group and the new expiry', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(OFF);
    const expiresAt = Date.now() + 30 * 24 * 60 * 60 * 1000;
    const claim = vi.spyOn(invitesApi, 'claimInviteCode')
      .mockResolvedValue({ group_id: 'g2', group_name: 'VIP', days: 30, expires_at: expiresAt });
    await mount(InvitesSection);

    await type(claimInput(), 'PARTNER1');
    button(t('inviteClaimSubmit')).click();
    await settle();

    expect(claim).toHaveBeenCalledWith('PARTNER1');
    expect(host.textContent).toContain(t('inviteClaimSuccess', { group: 'VIP', date: absoluteTime(expiresAt) }));
  });

  it('maps each claim refusal to its own words', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(OFF);
    const cases: Array<[string, Record<string, unknown>, string]> = [
      ['invite_invalid', {}, t('inviteClaimInvalid')],
      ['invite_claimed', {}, t('inviteClaimAlready')],
      ['invite_group_conflict', {}, t('inviteClaimConflict')],
      ['too_many_attempts', { retry_after_seconds: 42 }, t('tooManyAttempts', { count: 42 })],
    ];

    for (const [code, details, expected] of cases) {
      vi.spyOn(invitesApi, 'claimInviteCode')
        .mockRejectedValueOnce(new ApiError(400, code, 'refused', details));
      await mount(InvitesSection);

      await type(claimInput(), 'CODE1');
      button(t('inviteClaimSubmit')).click();
      await settle();

      expect(host.textContent).toContain(expected);
      app?.unmount();
      host.textContent = '';
    }
  });

  it('shows the every-N progress line and bar, and hides both when rewards are off', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    await mount(InvitesSection);

    expect(host.textContent).toContain(
      tn(2, 'inviteProgressOne', 'inviteProgressOther', { counted: 1, remaining: 4, cards: 2 }),
    );
    expect(host.querySelector('.oa-meter')).not.toBeNull();

    app?.unmount();
    host.textContent = '';
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue({ ...ON, reward_cards: 0, next_reward_in: 0 });
    await mount(InvitesSection);
    expect(host.querySelector('.oa-meter')).toBeNull();
  });

  it('names who joined, whether they were counted, and any cards that row earned', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue({
      ...ON,
      invitees: [
        { nickname: 'Ada', username: 'ada', created_at: Date.now(), counted: true, reward_cards: 2, reward_skipped: '' },
        { nickname: '', username: 'bob', created_at: Date.now(), counted: false, reward_skipped: 'same_ip', reward_cards: 0 },
        { nickname: 'Cy', username: 'cy', created_at: Date.now(), counted: false, reward_skipped: 'inviter_gone', reward_cards: 0 },
        { nickname: 'Di', username: 'di', created_at: Date.now(), counted: false, reward_skipped: '', reward_cards: 0 },
      ],
    });
    await mount(InvitesSection);

    expect(host.textContent).toContain('Ada');
    expect(host.textContent).toContain(t('inviteeCounted'));
    expect(host.textContent).toContain(tn(2, 'inviteeCardsOne', 'inviteeCardsOther', { cards: 2 }));
    // No nickname falls back to the username.
    expect(host.textContent).toContain('bob');
    expect(host.textContent).toContain(t('inviteeSkipSameIp'));
    expect(host.textContent).toContain(t('inviteeSkipInviterGone'));
    expect(host.textContent).toContain(t('inviteePending'));
  });

  it('says nobody has joined yet, rather than an empty list', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue({ ...ON, invitees: [] });
    await mount(InvitesSection);
    expect(host.textContent).toContain(t('inviteesEmpty'));
  });

  it('regenerates the code behind a confirm, and shows the new one', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    const regenerate = vi.spyOn(invitesApi, 'regenerateProfileInvite')
      .mockResolvedValue({ ...ON, code: 'ZZZZ9999', used: 0, invitees: [] });
    await mount(InvitesSection);

    button(t('inviteRegenerate')).click();
    await settle();
    button(t('confirmWord')).click();
    await settle();

    expect(regenerate).toHaveBeenCalled();
    expect(host.textContent).toContain('ZZZZ-9999');
    expect(host.textContent).not.toContain('ABCD-2345');
  });

  it('keeps claim box, displays error and allows retry when loading fails', async () => {
    const fetchSpy = vi.spyOn(invitesApi, 'fetchProfileInvites')
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'Network connection failed'))
      .mockResolvedValueOnce(ON);

    await mount(InvitesSection);

    // Claim box is still rendered
    expect(host.textContent).toContain(t('inviteClaimTitle'));
    expect(() => button(t('inviteClaimSubmit'))).not.toThrow();

    // Error and retry button are shown
    expect(host.textContent).toContain('Network connection failed');
    const retryBtn = button(t('retry'));
    expect(retryBtn).not.toBeNull();

    // Clicking retry reloads the data
    retryBtn.click();
    await settle();

    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain('ABCD-2345');
    expect(host.textContent).not.toContain('Network connection failed');
  });

  it('claims a permanent group code and displays permanent success message', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(OFF);
    const claim = vi.spyOn(invitesApi, 'claimInviteCode')
      .mockResolvedValue({ group_id: 'g-perm', group_name: 'Lifetime VIP', days: 0, expires_at: 0 });
    await mount(InvitesSection);

    await type(claimInput(), 'LIFETIME');
    button(t('inviteClaimSubmit')).click();
    await settle();

    expect(claim).toHaveBeenCalledWith('LIFETIME');
    expect(host.textContent).toContain(t('inviteClaimSuccessPermanent', { group: 'Lifetime VIP' }));
  });

  it('removes ?claim= from query params after claiming successfully', async () => {
    route.query = { claim: 'CLEANME' };
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(OFF);
    vi.spyOn(invitesApi, 'claimInviteCode')
      .mockResolvedValue({ group_id: 'g1', group_name: 'Basic', days: 7, expires_at: Date.now() + 7 * 86400000 });
    await mount(InvitesSection);

    button(t('inviteClaimSubmit')).click();
    await settle();

    expect(router.replace).toHaveBeenCalledWith({ path: '/settings', query: {} });
  });

  it('displays rate limit error when regenerate is throttled', async () => {
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockResolvedValue(ON);
    vi.spyOn(invitesApi, 'regenerateProfileInvite')
      .mockRejectedValue(new ApiError(429, 'too_many_attempts', 'rate limited', { retry_after_seconds: 45 }));
    await mount(InvitesSection);

    button(t('inviteRegenerate')).click();
    await settle();
    button(t('confirmWord')).click();
    await settle();

    expect(host.textContent).toContain(t('tooManyAttempts', { count: 45 }));
  });

  it('renders loading state while profile invites are being fetched', async () => {
    let resolveInvites!: (value: ProfileInvites) => void;
    vi.spyOn(invitesApi, 'fetchProfileInvites').mockReturnValue(new Promise((resolve) => {
      resolveInvites = resolve;
    }));

    const panels = document.createElement('div');
    document.body.append(panels);
    app = createApp({
      setup() {
        providePanelHost(shallowRef(panels));
        return () => h(InvitesSection, {});
      },
    });
    app.mount(host);
    await nextTick();

    expect(host.textContent).toContain(t('inviteClaimTitle'));
    expect(host.textContent).toContain(t('loading'));

    resolveInvites(ON);
    await settle();

    expect(host.textContent).not.toContain(t('loading'));
    expect(host.textContent).toContain('ABCD-2345');
  });
});

