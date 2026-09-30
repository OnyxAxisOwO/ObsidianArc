import { describe, expect, it } from 'vitest';
import type { Notification } from '../src/api/notifications';
import { describeNotification } from '../src/lib/notification-text';

// The server sends a kind and params, never a sentence. These are the kinds
// whose params decide the wording, so a param renamed on one side and not the
// other shows up here rather than as a generic line in somebody's bell.

function note(kind: string, params: Record<string, unknown>): Notification {
  return { id: 'n1', kind, params, link: '', created_at: 1 } as Notification;
}

describe('describeNotification', () => {
  it('says which two-step change happened', () => {
    expect(describeNotification(note('two_factor_changed', { kind: 'disabled' })).body).toMatch(/turned off/);
    expect(describeNotification(note('two_factor_changed', { kind: 'reset' })).body).toMatch(/administrator/i);
    expect(describeNotification(note('two_factor_changed', { kind: 'recovery_used' })).body).toMatch(/recovery code/);
    // A kind this build does not know still reads as a two-step change.
    expect(describeNotification(note('two_factor_changed', { kind: 'later' })).body).toMatch(/Two-step/);
  });

  it('names the device and address of a new sign-in', () => {
    const body = describeNotification(note('new_device_login', {
      ua: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15',
      ip: '203.0.113.9',
    })).body;
    expect(body).toContain('Safari · macOS');
    expect(body).toContain('203.0.113.9');
    expect(describeNotification(note('new_device_login', {})).body).not.toContain('{device}');
  });

  it('lists what an administrator changed, dropping keys it cannot word', () => {
    const body = describeNotification(note('account_changed', { what: ['profile', 'group', 'mystery'] })).body;
    expect(body).toBe('An administrator changed your profile, group.');
    expect(describeNotification(note('account_changed', { what: 'role' })).body)
      .toBe('An administrator changed something about your account.');
  });

  it('names the bar and the amount of a bonus that arrived, to two places at most', () => {
    const received = describeNotification(note('bonus_granted', { name: 'Launch gift', amount: 2.5 }));
    expect(received.body).toBe('Launch gift: 2.5 credits');
    expect(describeNotification(note('bonus_granted', { name: 'Gift', amount: 0.1 + 0.2 })).body).toBe('Gift: 0.3 credits');
    // A param that never arrived reads as an empty name and no credit, never as a placeholder.
    expect(describeNotification(note('bonus_granted', {})).body).not.toContain('{');
  });

  it('says what is about to lapse, in which bar, and when', () => {
    const body = describeNotification(note('bonus_expiring', {
      name: 'Launch gift', amount: 7, expires_at: Date.UTC(2031, 0, 15, 12),
    })).body;
    expect(body).toContain('7 credits in Launch gift expire');
    expect(body).not.toContain('{');
    expect(body).toMatch(/2031/);
  });

  it('puts the held-back username in the title', () => {
    expect(describeNotification(note('signup_flagged', { username: 'mallory' })).title).toContain('mallory');
  });
});
