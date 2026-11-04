import { afterEach, describe, expect, it } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import OaMarquee from '../src/components/OaMarquee.vue';

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
});

describe('OaMarquee component', () => {
  it('renders static text when text fits or in non-overflow environment', () => {
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp(OaMarquee, { text: 'GPT-4o' });
    app.mount(host);

    const staticEl = host.querySelector('.oa-marquee-static');
    expect(staticEl).toBeTruthy();
    expect(staticEl?.textContent?.trim()).toBe('GPT-4o');

    const container = host.querySelector('.oa-marquee');
    expect(container?.getAttribute('title')).toBe('GPT-4o');
  });

  it('renders an invisible sizer with aria-hidden="true"', () => {
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp(OaMarquee, { text: 'Claude Sonnet 5 (75折倍率)' });
    app.mount(host);

    const sizer = host.querySelector('.oa-marquee-sizer');
    expect(sizer).toBeTruthy();
    expect(sizer?.getAttribute('aria-hidden')).toBe('true');
    expect(sizer?.textContent?.trim()).toBe('Claude Sonnet 5 (75折倍率)');
  });

  it('switches to marquee track with duplicate slice when overflowing', async () => {
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp(OaMarquee, {
      text: 'Claude Sonnet 5 (75折倍率)',
      speed: 25,
      gap: 30,
    });
    const vm = app.mount(host) as any;

    const container = host.querySelector('.oa-marquee') as HTMLElement;
    const sizer = host.querySelector('.oa-marquee-sizer') as HTMLElement;

    // Mock clientWidth on container and scrollWidth on sizer to simulate long overflow
    Object.defineProperty(container, 'clientWidth', { configurable: true, value: 100 });
    Object.defineProperty(sizer, 'scrollWidth', { configurable: true, value: 250 });

    // Trigger checkOverflow
    vm.checkOverflow();
    await nextTick();

    expect(container.classList.contains('is-overflowing')).toBe(true);
    expect(host.querySelector('.oa-marquee-track')).toBeTruthy();

    const slices = host.querySelectorAll('.oa-marquee-slice');
    expect(slices.length).toBe(2);
    // Duplicate slice must be aria-hidden so screen readers do not read twice
    expect(slices[1]?.getAttribute('aria-hidden')).toBe('true');

    // Duration calculation: totalDistance = 250 + 30 = 280.
    // movingSeconds = 280 / 25 = 11.2s.
    // totalSeconds = 11.2 / 0.76 ≈ 14.7s.
    const style = container.getAttribute('style') || '';
    expect(style).toContain('--marquee-duration: 14.7s');
  });

  it('switches back to static text when container expands or text shortens', async () => {
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp(OaMarquee, { text: 'Claude Opus 5 (75折倍率)' });
    const vm = app.mount(host) as any;

    const container = host.querySelector('.oa-marquee') as HTMLElement;
    const sizer = host.querySelector('.oa-marquee-sizer') as HTMLElement;

    // First make it overflow
    Object.defineProperty(container, 'clientWidth', { configurable: true, value: 80 });
    Object.defineProperty(sizer, 'scrollWidth', { configurable: true, value: 200 });
    vm.checkOverflow();
    await nextTick();
    expect(container.classList.contains('is-overflowing')).toBe(true);

    // Now expand container so text fits
    Object.defineProperty(container, 'clientWidth', { configurable: true, value: 300 });
    vm.checkOverflow();
    await nextTick();
    expect(container.classList.contains('is-overflowing')).toBe(false);
    expect(host.querySelector('.oa-marquee-static')).toBeTruthy();
  });
});
