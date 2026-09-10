import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaRangeField from '../src/components/OaRangeField.vue';

let app: App | undefined;
afterEach(() => {
  app?.unmount();
  document.body.textContent = '';
});

describe('range field', () => {
  it('labels the native control, previews changes and commits only on change', async () => {
    const host = document.createElement('div');
    document.body.append(host);
    const value = ref(30);
    const commit = vi.fn();
    app = createApp({ render: () => h(OaRangeField, {
      modelValue: value.value, min: 10, max: 50, step: 2, label: 'Blur', hint: 'Preview blur',
      format: (n: number) => `${n}px`,
      'onUpdate:modelValue': (n: number) => { value.value = n; }, onCommit: commit,
    }) });
    app.mount(host);
    const input = host.querySelector('input')!;
    expect(host.querySelector('label')?.htmlFor).toBe(input.id);
    expect(document.getElementById(input.getAttribute('aria-describedby')!)?.textContent).toBe('Preview blur');
    expect(input.style.getPropertyValue('--oa-range-progress')).toBe('50%');
    input.value = '42';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    expect(value.value).toBe(42);
    expect(input.getAttribute('aria-valuetext')).toBe('42px');
    expect(input.style.getPropertyValue('--oa-range-progress')).toBe('80%');
    expect(commit).not.toHaveBeenCalled();
    input.dispatchEvent(new Event('change', { bubbles: true }));
    expect(commit).toHaveBeenCalledWith(42);
    value.value = 10;
    await nextTick();
    expect(input.style.getPropertyValue('--oa-range-progress')).toBe('0%');
    value.value = 50;
    await nextTick();
    expect(input.style.getPropertyValue('--oa-range-progress')).toBe('100%');
  });
});
