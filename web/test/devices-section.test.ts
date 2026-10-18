import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App, type Component } from 'vue';
import * as sessionsApi from '../src/api/sessions';
import type { DeviceSession } from '../src/api/sessions';
import * as twoFactorApi from '../src/api/twofactor';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import SecuritySection from '../src/views/settings/SecuritySection.vue';
import { settle, button } from './helpers';

// The "Signed-in devices" card: listing, signing a single device out, "sign
// out other devices", and the "this device" badge.

const OFF = {
  available: true, enabled: false, enabled_at: 0, recovery_remaining: 0,
  mandatory: false, policy: 'optional' as const, remember_days: 0,
};

const THIS_DEVICE: DeviceSession = {
  id: 'aaaaaaaaaaaaaaaa', created_at: 1, last_seen_at: Date.now(),
  ip: '203.0.113.9', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/119.0.0.0 Safari/537.36',
  current: true,
};

const OTHER_DEVICE: DeviceSession = {
  id: 'bbbbbbbbbbbbbbbb', created_at: 1, last_seen_at: Date.now() - 60_000,
  ip: '198.51.100.4', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Version/17.0 Mobile/15E148 Safari/604.1',
  current: false,
};

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.append(host);
  vi.spyOn(twoFactorApi, 'fetchTwoFactor').mockResolvedValue(OFF);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
});

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

describe('the signed-in devices card', () => {
  it('lists every device and marks the current one', async () => {
    vi.spyOn(sessionsApi, 'fetchSessions').mockResolvedValue({ sessions: [THIS_DEVICE, OTHER_DEVICE] });
    await mount(SecuritySection);

    expect(host.textContent).toContain(t('secDevices'));
    expect(host.textContent).toContain(t('deviceThisDevice'));
    expect(host.textContent).toContain('Chrome · macOS');
    expect(host.textContent).toContain('Safari · iOS');
  });

  it('signs out one other device, without a button for the current one', async () => {
    vi.spyOn(sessionsApi, 'fetchSessions').mockResolvedValue({ sessions: [THIS_DEVICE, OTHER_DEVICE] });
    const revoke = vi.spyOn(sessionsApi, 'revokeSession').mockResolvedValue();
    await mount(SecuritySection);

    // Two "Sign out" buttons: one per row would sign the current device out
    // too, which the server itself refuses — so there is only one here.
    const signOutButtons = [...host.querySelectorAll('button')]
      .filter((node) => node.textContent?.trim() === t('deviceSignOut'));
    expect(signOutButtons).toHaveLength(1);

    signOutButtons[0]!.click();
    await settle();
    button(host, t('confirmWord')).click();
    await settle();
    expect(revoke).toHaveBeenCalledWith(OTHER_DEVICE.id);
    expect(host.textContent).not.toContain('Safari · iOS');
  });

  it('signs out every other device in one call, and keeps this one', async () => {
    vi.spyOn(sessionsApi, 'fetchSessions').mockResolvedValue({ sessions: [THIS_DEVICE, OTHER_DEVICE] });
    const revokeOthers = vi.spyOn(sessionsApi, 'revokeOtherSessions').mockResolvedValue();
    await mount(SecuritySection);

    button(host, t('deviceSignOutOthers')).click();
    await settle();
    button(host, t('confirmWord')).click();
    await settle();
    expect(revokeOthers).toHaveBeenCalled();
    expect(host.textContent).toContain(t('deviceSignOutOthersDone'));
    expect(host.textContent).toContain('Chrome · macOS');
    expect(host.textContent).not.toContain('Safari · iOS');
  });

  it('offers no "sign out other devices" row with only one device', async () => {
    vi.spyOn(sessionsApi, 'fetchSessions').mockResolvedValue({ sessions: [THIS_DEVICE] });
    await mount(SecuritySection);
    expect(() => button(host, t('deviceSignOutOthers'))).toThrow();
  });
});
