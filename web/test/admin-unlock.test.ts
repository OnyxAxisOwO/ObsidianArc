import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import * as twoFactorApi from '../src/api/twofactor';
import { changeLanguage } from '../src/composables/useI18n';
import AdminUnlock from '../src/views/admin/AdminUnlock.vue';
import { catchAllRouter, mountComponent, settle } from './helpers';

let unmount: (() => void) | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount?.();
  unmount = undefined;
  host.remove();
  vi.restoreAllMocks();
});

async function mount(listeners: Record<string, unknown> = {}): Promise<void> {
  // No panel host and no administration view: the unlock screen uses neither,
  // so one starting to would fail here.
  unmount = await mountComponent(AdminUnlock, listeners, { host, panels: null, router: await catchAllRouter('/admin') });
}

function codeField(): HTMLInputElement {
  return host.querySelector<HTMLInputElement>('input.oa-2fa-code')!;
}

function type(value: string): void {
  const field = codeField();
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('AdminUnlock', () => {
  it('sends a six-digit code as soon as its last digit is typed, and opens the door', async () => {
    const enter = vi.spyOn(twoFactorApi, 'enterBackoffice').mockResolvedValue(undefined);
    const unlocked = vi.fn();
    await mount({ onUnlocked: unlocked });

    type('12345');
    await settle();
    expect(enter).not.toHaveBeenCalled();

    type('123456');
    await settle();

    expect(enter).toHaveBeenCalledTimes(1);
    expect(enter).toHaveBeenCalledWith('123456');
    expect(unlocked).toHaveBeenCalledTimes(1);
  });

  it('waits for the button when the code has letters in it, as a recovery code does', async () => {
    const enter = vi.spyOn(twoFactorApi, 'enterBackoffice').mockResolvedValue(undefined);
    await mount();

    type('ab12-cd34');
    await settle();
    expect(enter).not.toHaveBeenCalled();

    host.querySelector<HTMLButtonElement>('button[type="submit"]')!.click();
    await settle();
    expect(enter).toHaveBeenCalledWith('ab12-cd34');
  });

  it('shows a refused code in the alert, clears the field and keeps the door shut', async () => {
    vi.spyOn(twoFactorApi, 'enterBackoffice')
      .mockRejectedValue(new ApiError(401, 'two_factor_code', 'That code is not valid.'));
    const unlocked = vi.fn();
    await mount({ onUnlocked: unlocked });

    type('000000');
    await settle();

    const alert = host.querySelector<HTMLParagraphElement>('p[role="alert"]')!;
    expect(alert.hasAttribute('hidden')).toBe(false);
    expect(alert.textContent!.trim()).not.toBe('');
    expect(codeField().value).toBe('');
    expect(unlocked).not.toHaveBeenCalled();
  });
});
