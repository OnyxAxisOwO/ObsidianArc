import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ChatComposer from '../src/chat/ChatComposer.vue';
import AdminAssistant from '../src/views/admin/AdminAssistant.vue';
import ChatSection from '../src/views/settings/ChatSection.vue';
import { assistant } from '../src/admin/assistant';
import { submit } from '../src/chat/useChat';
import { api } from '../src/api/client';
import * as auth from '../src/api/auth';
import { adopt, currentPreferences, forget } from '../src/stores/session';
import { changeLanguage, t } from '../src/composables/useI18n';
import { sectionMatches } from '../src/views/settings/search';
import { adminShell, button, mountComponent, settle } from './helpers';
import { makeAccount } from './fixtures/account';

vi.mock('../src/chat/useChat', async (original) => ({
  ...await original<typeof import('../src/chat/useChat')>(), submit: vi.fn().mockResolvedValue(undefined),
}));

let unmount: (() => void) | undefined;

beforeEach(async () => {
  await changeLanguage('en');
  adopt(makeAccount({ role: 'super_admin' }));
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
  vi.spyOn(api, 'get').mockResolvedValue({ models: [{ id: 'm', display_name: 'Model' }] });
  vi.spyOn(auth, 'savePreferences').mockResolvedValue({ preferences: {} });
  vi.spyOn(assistant, 'submit').mockResolvedValue(undefined);
});

afterEach(() => {
  unmount?.();
  unmount = undefined;
  assistant.reset();
  forget();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe.each([
  { name: 'chat', component: ChatComposer, sent: () => vi.mocked(submit) },
  { name: 'Rune', component: AdminAssistant, sent: () => vi.mocked(assistant.submit) },
])('$name send shortcut', ({ component, sent }) => {
  it.each([
    { name: 'default', preferences: {}, shiftKey: true },
    { name: 'Shift+Enter preference', preferences: { send_on_enter: false }, shiftKey: true },
    { name: 'Enter preference', preferences: { send_on_enter: true }, shiftKey: false },
    { name: 'invalid preference', preferences: { send_on_enter: 'true' }, shiftKey: true },
  ])('sends only with the $name shortcut and leaves IME confirmation alone', async ({ preferences, shiftKey }) => {
    currentPreferences.value = preferences;
    unmount = await mountComponent(component, {}, { adminView: adminShell() });
    const input = document.querySelector<HTMLTextAreaElement>('.ai-chat-input')!;
    input.value = '你好';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();

    const ignored: KeyboardEventInit[] = [
      { shiftKey: !shiftKey },
      { shiftKey, isComposing: true },
      { shiftKey, keyCode: 229 },
      { shiftKey, repeat: true },
      { shiftKey, ctrlKey: true },
      { shiftKey, metaKey: true },
      { shiftKey, altKey: true },
    ];
    for (const init of ignored) {
      const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true, ...init });
      input.dispatchEvent(event);
      expect(event.defaultPrevented).toBe(false);
      expect(sent()).not.toHaveBeenCalled();
      expect(input.value).toBe('你好');
    }

    const send = new KeyboardEvent('keydown', { key: 'Enter', shiftKey, bubbles: true, cancelable: true });
    input.dispatchEvent(send);
    expect(send.defaultPrevented).toBe(true);
    expect(sent()).toHaveBeenCalledOnce();

    // An already-open composer must use a changed setting without remounting.
    currentPreferences.value = { send_on_enter: shiftKey };
    const changed = new KeyboardEvent('keydown', { key: 'Enter', shiftKey: !shiftKey, bubbles: true, cancelable: true });
    input.dispatchEvent(changed);
    expect(changed.defaultPrevented).toBe(true);
    expect(sent()).toHaveBeenCalledTimes(2);
  });
});

it('saves the shortcut to account preferences, restores it, and makes it searchable', async () => {
  unmount = await mountComponent(ChatSection);
  expect(document.body.textContent).toContain(t('sendWithShiftEnter'));
  expect(sectionMatches(t('sendShortcut'), 'chat')).toBe(true);

  button(document, t('sendWithShiftEnter')).click();
  await settle();
  const enter = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((option) => option.textContent?.trim() === t('sendWithEnter'))!;
  enter.click();
  await settle();
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ send_on_enter: true });
  expect(currentPreferences.value['send_on_enter']).toBe(true);

  unmount();
  adopt(makeAccount(), { send_on_enter: true });
  unmount = await mountComponent(ChatSection);
  expect(button(document, t('sendWithEnter'))).toBeDefined();
  button(document, t('sendWithEnter')).click();
  await settle();
  const shiftEnter = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((option) => option.textContent?.trim() === t('sendWithShiftEnter'))!;
  shiftEnter.click();
  await settle();
  expect(auth.savePreferences).toHaveBeenLastCalledWith({ send_on_enter: false });
  expect(currentPreferences.value['send_on_enter']).toBe(false);
});
