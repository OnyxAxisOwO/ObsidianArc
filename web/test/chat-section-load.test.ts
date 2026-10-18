// The default-model control when the model list cannot be loaded. Its only
// choice must say the list failed; offering "first available model" there
// would promise an account a model it may not be able to reach.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';
import { api } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { forget } from '../src/stores/session';
import ChatSection from '../src/views/settings/ChatSection.vue';
import { settle } from './helpers';

let app: App | undefined;

beforeEach(async () => {
  await changeLanguage('en');
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
  forget();
});


it('names the load failure as the only model choice when the model list request is rejected', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('network down'));
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp(ChatSection);
  app.mount(host);
  await settle();

  expect(host.textContent).toContain(t('couldNotLoadModels'));
  expect(host.textContent).not.toContain(t('firstAvailableModel'));
});
