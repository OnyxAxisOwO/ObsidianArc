// The figure beside an allowance window. A thousands figure that rounds up to
// 1000k used to print as such, where the unit above it is the one it reads as.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import type { UsageWindow } from '../src/api/usage';
import OaUsageWindow from '../src/components/OaUsageWindow.vue';

let app: App | undefined;
let host: HTMLElement;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
});

// No limit on any dimension, so the figure is the raw count and nothing else.
async function figureFor(used: number): Promise<string | null> {
  app?.unmount();
  host.replaceChildren();
  const allowance: UsageWindow = {
    kind: '1m', enforced: true,
    used_requests: used, used_tokens: 0, used_credits: 0,
    limit_requests: null, limit_tokens: null, limit_credits: null,
    resets_at: Date.now() + 3_600_000,
  };
  app = createApp({ render: () => h(OaUsageWindow, { window: allowance }) });
  app.mount(host);
  await nextTick();
  return host.querySelector('.oa-usage-value')?.textContent ?? null;
}

describe('OaUsageWindow figure at the unit boundaries', () => {
  it('moves a thousands figure that rounds up to 1000k into millions', async () => {
    expect(await figureFor(999_960)).toBe('1M');
    expect(await figureFor(999_950)).toBe('1M');
  });

  it('moves a millions figure that rounds up to 1000M into billions', async () => {
    expect(await figureFor(999_950_000)).toBe('1B');
    expect(await figureFor(999_949_999)).toBe('999.9M');
    expect(await figureFor(1_050_000_000)).toBe('1.1B');
  });

  it('keeps the thousands figure that does not round over', async () => {
    expect(await figureFor(999_949)).toBe('999.9k');
    expect(await figureFor(1_234)).toBe('1.2k');
  });

  it('moves a sub-thousand figure that rounds up to 1000 into thousands', async () => {
    expect(await figureFor(999.96)).toBe('1k');
  });

  it('prints a small count as it always did', async () => {
    expect(await figureFor(42)).toBe('42');
    expect(await figureFor(12.34)).toBe('12.3');
  });
});
