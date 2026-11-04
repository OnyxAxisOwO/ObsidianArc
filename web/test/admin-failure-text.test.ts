// The line an administration screen shows for a failed request. A server
// sentence is shown as the server wrote it, except that a code with a reader's
// sentence is translated; whatever the server did not send is not shown at all,
// because a raw error (`TypeError: …`) is for the console.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { failureText } from '../src/views/admin/shared';

beforeEach(async () => {
  await changeLanguage('en');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('failureText', () => {
  it('translates a code the interface has a reader sentence for', () => {
    expect(failureText(new ApiError(504, 'plugin_timeout', 'The plugin did not answer in time.')))
      .toBe(t('errPluginTimeout'));
  });

  it('translates a backoffice refusal by its code, quoting the figures its details carry', async () => {
    const refusal = new ApiError(400, 'setting_out_of_range', 'Setting "x" must be between 1 and 9.', {
      setting: 'x', min: 1, max: 9,
    });
    expect(failureText(refusal)).toBe('Setting "x" must be between 1 and 9.');
    await changeLanguage('zh');
    expect(failureText(refusal)).toBe('设置“x”必须在 1 到 9 之间。');
  });

  it('translates the refusals an administrator gets for acting on their own account', async () => {
    await changeLanguage('zh');
    expect(failureText(new ApiError(400, 'cannot_ban_self', 'You cannot ban your own account.')))
      .toBe(t('cannotBanSelf'));
    expect(failureText(new ApiError(400, 'cannot_delete_self', 'You cannot delete your own account.')))
      .toBe('不能删除自己的账户。');
  });

  it('keeps the server sentence for any other refusal, including a 5xx a plugin chose', () => {
    expect(failureText(new ApiError(503, 'upstream_down', 'The partner service is down.')))
      .toBe('The partner service is down.');
  });

  it('shows an Error as its message, the way the code that threw it wrote it', () => {
    expect(failureText(new Error('upstream down'))).toBe('upstream down');
  });

  it('shows the generic line, not the raw value, for a thrown value that is not an Error', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const text = failureText({ detail: 'a raw object' });
    expect(text).toBe(t('failed'));
    expect(text).not.toContain('object');
    expect(warn).toHaveBeenCalledOnce();
  });
});
