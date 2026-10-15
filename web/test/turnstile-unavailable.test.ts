// When the challenge script cannot be had, the form says the challenge is
// unavailable, in the alert role, instead of drawing an empty box that a
// refusal then fails against with nothing to solve.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import OaTurnstile from '../src/components/OaTurnstile.vue';
import { changeLanguage, t } from '../src/composables/useI18n';

vi.mock('@/composables/useTurnstile', () => ({
  loadTurnstile: vi.fn(async () => null),
}));

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
});

describe('OaTurnstile without its script', () => {
  it('says the challenge is unavailable', async () => {
    await changeLanguage('en');
    const host = document.createElement('div');
    document.body.append(host);
    app = createApp({ render: () => h(OaTurnstile, { siteKey: 'site-key' }) });
    app.mount(host);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    const alert = host.querySelector('[role="alert"]');
    expect(alert?.textContent).toBe(t('challengeUnavailable'));
  });
});
