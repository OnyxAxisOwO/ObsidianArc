import { afterEach, expect, it, vi } from 'vitest';
import { createApp } from 'vue';
import RuneField from '../src/views/admin/RuneField.vue';
import { runeIntensity } from '../src/admin/rune-field';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('moves signal pulses over time and keeps every cell within its intensity range', () => {
  let changed = 0;
  for (let y = 0; y < 40; y += 1) {
    for (let x = 0; x < 80; x += 1) {
      const first = runeIntensity(x, y, 80, 40, 0);
      const later = runeIntensity(x, y, 80, 40, 2);
      expect(first).toBeGreaterThanOrEqual(0);
      expect(first).toBeLessThanOrEqual(1);
      if (Math.abs(first - later) > 0.05) changed++;
    }
  }
  expect(changed).toBeGreaterThan(100);
});

it.each([false, true])('cleans up its animation and keeps reduced-motion=%s static', (reduced) => {
  const context = { clearRect: vi.fn(), fillText: vi.fn(), setTransform: vi.fn() };
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(context as unknown as CanvasRenderingContext2D);
  vi.spyOn(HTMLCanvasElement.prototype, 'getBoundingClientRect').mockReturnValue({ width: 720, height: 560 } as DOMRect);
  vi.stubGlobal('matchMedia', () => ({ matches: reduced }));
  const schedule = vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(42);
  const cancel = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
  const host = document.createElement('div');
  document.body.append(host);
  const app = createApp(RuneField);
  app.mount(host);
  expect(host.querySelector('canvas[aria-hidden="true"]')).not.toBeNull();
  expect(context.fillText).toHaveBeenCalled();
  expect(schedule).toHaveBeenCalledTimes(reduced ? 0 : 1);
  app.unmount();
  expect(cancel).toHaveBeenCalledWith(reduced ? 0 : 42);
});
