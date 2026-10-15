// The hold button behind the usage reset. Holding a key down sends the
// browser's auto-repeat as a stream of keydowns with `repeat` set, and those
// are the same physical press: the action has to run once per press, not once
// per hold-length for as long as the key stays down.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import OaHoldButton from '../src/components/OaHoldButton.vue';

const HOLD_MS = 1200;

let app: App | undefined;
let fired = 0;

beforeEach(() => {
  vi.useFakeTimers();
  fired = 0;
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.useRealTimers();
});

function mount(): HTMLButtonElement {
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp({
    render: () => h(OaHoldButton, {
      label: 'Reset',
      holdingLabel: 'Hold',
      holdMs: HOLD_MS,
      onFire: () => { fired += 1; },
    }),
  });
  app.mount(host);
  return host.querySelector<HTMLButtonElement>('.oa-bigbutton-cap')!;
}

function keyEvent(button: HTMLButtonElement, type: 'keydown' | 'keyup', repeat = false): void {
  button.dispatchEvent(new KeyboardEvent(type, { key: 'Enter', repeat, bubbles: true, cancelable: true }));
}

describe('OaHoldButton with the keyboard', () => {
  it('fires once for a held key and does not re-arm on auto-repeat', () => {
    const button = mount();

    keyEvent(button, 'keydown');
    vi.advanceTimersByTime(HOLD_MS);
    expect(fired).toBe(1);

    // Four seconds of auto-repeat after the action has already run.
    for (let i = 0; i < 120; i += 1) {
      keyEvent(button, 'keydown', true);
      vi.advanceTimersByTime(33);
    }
    expect(fired).toBe(1);
  });

  it('fires again for a new press once the key has come up', () => {
    const button = mount();

    keyEvent(button, 'keydown');
    vi.advanceTimersByTime(HOLD_MS);
    keyEvent(button, 'keyup');
    expect(fired).toBe(1);

    keyEvent(button, 'keydown');
    vi.advanceTimersByTime(HOLD_MS);
    expect(fired).toBe(2);
  });

  it('lets go early without firing', () => {
    const button = mount();

    keyEvent(button, 'keydown');
    vi.advanceTimersByTime(HOLD_MS - 100);
    keyEvent(button, 'keyup');
    vi.advanceTimersByTime(HOLD_MS);
    expect(fired).toBe(0);
  });
});

describe('OaHoldButton with the pointer', () => {
  it('fires once for a press held past the threshold', () => {
    const button = mount();

    button.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true }));
    vi.advanceTimersByTime(HOLD_MS);
    button.dispatchEvent(new MouseEvent('pointerup', { bubbles: true }));
    vi.advanceTimersByTime(HOLD_MS);

    expect(fired).toBe(1);
  });
});
