// An account's own two-step verification: what the security settings and the
// setup wizard call. Signing in with a code is in auth.ts, beside login.

import { api } from './client';
import type { Account } from './auth';

export interface TwoFactorStatus {
  available: boolean;
  enabled: boolean;
  enabled_at: number;
  recovery_remaining: number;
  /** Whether the operator's policy covers this account, which is also
   *  whether it may be turned off. */
  mandatory: boolean;
  policy: 'optional' | 'backoffice' | 'admins' | 'everyone';
  remember_days: number;
}

export interface TwoFactorSetup {
  secret: string;
  uri: string;
  issuer: string;
  account: string;
  /** The otpauth link as a QR code, already drawn: the dark modules as one
   *  SVG path in module units, and the side length without the quiet zone. */
  qr: { size: number; path: string };
  /** Whether confirming asks for the account's password, so the wizard draws
   *  the field up front. False for an account that only signs in through a
   *  provider. */
  password_required?: boolean;
}

export function fetchTwoFactor(): Promise<TwoFactorStatus> {
  return api.get<TwoFactorStatus>('/api/profile/two-factor');
}

export function beginTwoFactor(): Promise<TwoFactorSetup> {
  return api.post<TwoFactorSetup>('/api/profile/two-factor/setup');
}

/** Enabling signs every other device out, so the server wants the password
 *  as well as the code — a session alone is what a stolen cookie is. */
export function enableTwoFactor(
  code: string,
  currentPassword?: string,
): Promise<{ recovery_codes: string[]; user: Account }> {
  return api.post<{ recovery_codes: string[]; user: Account }>('/api/profile/two-factor/enable', {
    code,
    ...(currentPassword ? { current_password: currentPassword } : {}),
  });
}

export function disableTwoFactor(code: string): Promise<{ user: Account }> {
  return api.post<{ user: Account }>('/api/profile/two-factor/disable', { code });
}

export function regenerateRecovery(code: string): Promise<{ recovery_codes: string[] }> {
  return api.post<{ recovery_codes: string[] }>('/api/profile/two-factor/recovery', { code });
}

/** Opens this browser's visit to the backoffice, where the server asks for a
 *  code at its door. A recovery code works here too. */
export function enterBackoffice(code: string): Promise<void> {
  return api.post<void>('/api/profile/two-factor/backoffice', { code });
}

/**
 * Says this browser has left the backoffice, which ends the visit where the
 * operator asks for a code on every one.
 *
 * A beacon, because the moment worth sending it is the page going away, and
 * an ordinary request is cancelled with the page. Nothing reads the answer:
 * a visit that failed to close still closes itself after the idle minutes.
 */
export function leaveBackoffice(): void {
  const path = '/api/profile/two-factor/backoffice/leave';
  if (typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function' && navigator.sendBeacon(path)) {
    return;
  }
  void fetch(path, { method: 'POST', credentials: 'same-origin', keepalive: true }).catch(() => {
    // Offline or already signed out: nothing is left open either way.
  });
}
