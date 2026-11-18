// The default-model control when the model list cannot be loaded. Its only
// choice must say the list failed; offering "first available model" there
// would promise an account a model it may not be able to reach.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';
import { api } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { startNewConversation } from '../src/chat/useChat';
import { models, reasoning, selectedID, type AvailableModel } from '../src/chat/useModels';
import { adopt, currentPreferences, forget } from '../src/stores/session';
import { makeAccount } from './fixtures/account';
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

it('uses the selected model’s tiers and applies its preferences when a conversation starts', async () => {
  const catalogue = [
    { id: 'a', display_name: 'A' },
    { id: 'b', display_name: 'B', reasoning_tiers: [{ id: 'deep', name: 'Deep search' }, { id: 'quick', name: 'Quick' }] },
  ] as AvailableModel[];
  adopt(makeAccount());
  vi.spyOn(api, 'patch').mockResolvedValue({ preferences: {} });
  vi.spyOn(api, 'get').mockResolvedValue({ models: catalogue });
  models.value = catalogue;
  selectedID.value = 'a';
  const host = document.createElement('div');
  document.body.append(host);
  app = createApp(ChatSection);
  app.mount(host);
  await settle();
  const selects = host.querySelectorAll<HTMLButtonElement>('button.oa-select');
  selects[0]!.click();
  await settle();
  const option = (label: string) => [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((entry) => entry.textContent?.trim() === label)!;
  option('B').click();
  await settle();
  expect(currentPreferences.value.default_model_id).toBe('b');
  selects[1]!.click();
  await settle();
  option('Deep search').click();
  await settle();
  expect(currentPreferences.value.reasoning_effort).toBe('deep');
  startNewConversation();
  expect(selectedID.value).toBe('b');
  expect(reasoning.value.effort).toBe('deep');
});
