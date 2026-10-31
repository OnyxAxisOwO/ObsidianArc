import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaBulkBar from '../src/components/OaBulkBar.vue';
import { t } from '../src/composables/useI18n';

// Give Vue real frame boundaries without making these tests wait for the
// wall clock; the component still owns insertion, removal and cancellation.
describe('bulk bar transitions', () => {
  let app: App | undefined;

  afterEach(() => {
    app?.unmount();
    vi.useRealTimers();
  });

  function mount() {
    vi.useFakeTimers();
    const count = ref(0);
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp({
      render: () => h(OaBulkBar, {
        count: count.value, total: 5,
        onClear: () => { count.value = 0; },
      }),
    });
    app.mount(host);
    return { count, host };
  }

  it('enters on the first selection, keeps the bar for count changes, and leaves before removal', async () => {
    const { count, host } = mount();
    expect(host.querySelector('[role="toolbar"]')).toBeNull();

    count.value = 1;
    await nextTick();
    const bar = host.querySelector<HTMLElement>('[role="toolbar"]')!;
    expect(bar.classList.contains('oa-bulk-enter-from')).toBe(true);
    await vi.runAllTimersAsync();
    expect(bar.className).toBe('oa-bulk-bar');

    count.value = 2;
    await nextTick();
    expect(host.querySelector('[role="toolbar"]')).toBe(bar);
    expect(bar.className).toBe('oa-bulk-bar');
    expect(bar.textContent).toContain(t('bulkSelected', { count: 2 }));

    bar.querySelector<HTMLButtonElement>('.oa-bulk-clear')!.click();
    await nextTick();
    expect(bar.isConnected).toBe(true);
    expect(bar.classList.contains('oa-bulk-leave-active')).toBe(true);
    await vi.runAllTimersAsync();
    expect(host.querySelector('[role="toolbar"]')).toBeNull();
  });

  it('can select again while the previous bar is leaving', async () => {
    const { count, host } = mount();
    count.value = 1;
    await nextTick();
    await vi.runAllTimersAsync();
    count.value = 0;
    await nextTick();
    count.value = 3;
    await nextTick();
    await vi.runAllTimersAsync();

    const bars = host.querySelectorAll('[role="toolbar"]');
    expect(bars).toHaveLength(1);
    expect(bars[0]!.className).toBe('oa-bulk-bar');
    expect(bars[0]!.textContent).toContain(t('bulkSelected', { count: 3 }));
  });
});
