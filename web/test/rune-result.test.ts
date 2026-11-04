import { expect, it } from 'vitest';
import { nextTick } from 'vue';
import RuneResult from '../src/views/admin/RuneResult.vue';
import { mountComponent } from './helpers';

it('keeps a result collapsed and accessible state follows both directions of its animated disclosure', async () => {
  const unmount = await mountComponent(RuneResult, { id: 'call-1', text: '<script>not markup</script>' });
  try {
    const trigger = document.querySelector<HTMLButtonElement>('.oa-assistant-result-toggle')!;
    const body = document.getElementById(trigger.getAttribute('aria-controls')!)!;
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    expect(body.getAttribute('aria-hidden')).toBe('true');
    expect(body.querySelector('script')).toBeNull();
    expect(document.querySelector('.oa-assistant-result-chevron')).not.toBeNull();
    trigger.click();
    await nextTick();
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(body.getAttribute('aria-hidden')).toBe('false');
    expect(document.querySelector('.oa-assistant-result.open')).not.toBeNull();
    trigger.click();
    await nextTick();
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    expect(document.querySelector('.oa-assistant-result.open')).toBeNull();
  } finally { unmount(); }
});
