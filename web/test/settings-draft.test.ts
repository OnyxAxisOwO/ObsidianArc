// The draft behind every settings page's Save. A save writes only the keys the
// page changed since it last saw them, because the endpoint writes whatever it
// is given: a whole-form save would undo another administrator's edit of a key
// this page only displays.

import { describe, expect, it } from 'vitest';
import { reactive } from 'vue';
import { overlongSetting, SETTING_MAX_BYTES, useSettingsDraft } from '../src/views/admin/settingsDraft';

describe('settings draft', () => {
  it('reports only the keys that differ from what the page loaded', () => {
    const form = reactive<Record<string, string>>({ 'api.enabled': 'false', 'chat.max_turns': '40' });
    const draft = useSettingsDraft(() => ({ ...form }));
    draft.accept();
    expect(draft.dirty.value).toBe(false);
    expect(draft.changes()).toEqual({});

    form['chat.max_turns'] = '12';
    expect(draft.dirty.value).toBe(true);
    expect(draft.changes()).toEqual({ 'chat.max_turns': '12' });
  });

  it('keeps an unchanged key out of the payload even when it is stale', () => {
    // The page loaded api.enabled as false; someone else turned it on since.
    // Editing another field must not write the stale false back.
    const form = reactive<Record<string, string>>({ 'api.enabled': 'false', 'site.name': 'Arc' });
    const draft = useSettingsDraft(() => ({ ...form }));
    draft.accept();

    form['site.name'] = 'Arc Two';
    expect(draft.changes()).not.toHaveProperty('api.enabled');
    expect(draft.changes()).toEqual({ 'site.name': 'Arc Two' });
  });

  it('after a save, forgets only the keys it sent, so an edit made in flight stays unsaved', () => {
    const form = reactive<Record<string, string>>({ 'a': '1', 'b': '2' });
    const draft = useSettingsDraft(() => ({ ...form }));
    draft.accept();

    form.a = '9';
    form.b = '8';
    const sent = draft.changes();
    expect(sent).toEqual({ a: '9', b: '8' });

    // Edited while the request was still in flight.
    form.b = '7';
    draft.accept(sent);

    expect(draft.changes()).toEqual({ b: '7' });
    expect(draft.dirty.value).toBe(true);
  });

  it('is clean again once what was edited has been sent and accepted', () => {
    const form = reactive<Record<string, string>>({ 'a': '1' });
    const draft = useSettingsDraft(() => ({ ...form }));
    draft.accept();

    form.a = '2';
    draft.accept(draft.changes());
    expect(draft.dirty.value).toBe(false);
    expect(draft.changes()).toEqual({});
  });
});

describe('overlong setting gate', () => {
  it('takes a value of exactly the byte limit and refuses one byte past it', () => {
    // A euro sign is three bytes, so 2730 of them is 8190 bytes and 2731 is
    // 8193: the limit is counted in bytes, not characters.
    expect(SETTING_MAX_BYTES).toBe(8192);
    expect(overlongSetting({ 'site.notice': '€'.repeat(2730) })).toBe(false);
    expect(overlongSetting({ 'site.notice': '€'.repeat(2731) })).toBe(true);
  });

  it('refuses the whole save when any one value is overlong', () => {
    expect(overlongSetting({ 'site.name': 'Arc', 'site.notice': 'x'.repeat(SETTING_MAX_BYTES + 1) })).toBe(true);
    expect(overlongSetting({ 'site.name': 'Arc', 'site.notice': 'x'.repeat(SETTING_MAX_BYTES) })).toBe(false);
  });
});
