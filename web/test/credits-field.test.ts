// A model that reserves nothing per turn has no number of turns to count. The
// readout used to divide by its zero cost and print "Infinity turns".

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, type App } from 'vue';
import { adminApi, type AdminModel } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import CreditsField from '../src/views/admin/CreditsField.vue';
import { settle } from './helpers';

;

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  vi.restoreAllMocks();
});

describe('the credits readout', () => {
  it('reads a model that costs nothing as free, not as infinitely many turns', async () => {
    const free = {
      id: 'free', provider_id: 'p1', provider_name: 'P1', model_id: 'free', display_name: 'Free model',
      enabled: true, max_output_tokens: 0, request_weight: 0, output_token_weight: 0,
    } as unknown as AdminModel;
    vi.spyOn(adminApi, 'models').mockResolvedValue({ models: [free] } as never);

    app = createApp({
      render: () => h(CreditsField as never, { modelValue: 10, 'onUpdate:modelValue': () => {} }),
    });
    app.mount(host);
    await settle();

    expect(host.textContent).toContain(t('creditsRowFree', { name: 'Free model' }));
    expect(host.textContent).not.toContain('Infinity');
  });
});
