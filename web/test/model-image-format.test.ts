import { describe, expect, it } from 'vitest';
import { blankModelForm, formFromModel, modelPayload } from '../src/views/admin/modelForm';

// The image response format is a form control of its own whose value lives
// inside the request override JSON: the payload a save sends is where the
// two meet, so that is where the round trip is held.
describe('the model form image response format', () => {
  it('starts unset on a blank form', () => {
    expect(blankModelForm().imageResponseFormat).toBe('');
  });

  it('merges the chosen format into the override the payload sends', () => {
    const form = { ...blankModelForm(), imageResponseFormat: 'url', requestOverride: '{"reasoning_effort":"low"}' };
    const payload = modelPayload(form, form.requestOverride);
    expect(JSON.parse(payload.request_override)).toEqual({
      reasoning_effort: 'low',
      image_response_format: 'url',
    });
  });

  it('writes the format on its own when the typed override is empty', () => {
    const form = { ...blankModelForm(), imageResponseFormat: 'url' };
    const payload = modelPayload(form, '');
    expect(JSON.parse(payload.request_override)).toEqual({ image_response_format: 'url' });
  });

  it('removes the key when the selection is cleared, rather than writing ""', () => {
    const form = {
      ...blankModelForm(),
      imageResponseFormat: '',
      requestOverride: '{"image_response_format":"url"}',
    };
    const payload = modelPayload(form, form.requestOverride);
    expect(payload.request_override).toBe('{}');
  });

  it('reads the format back out of a stored model override', () => {
    const stored = { request_override: '{"image_response_format":"url"}' } as any;
    expect(formFromModel(stored, null, [], '').imageResponseFormat).toBe('url');
    const broken = { request_override: '{"image_response_format":' } as any;
    expect(formFromModel(broken, null, [], '').imageResponseFormat).toBe('');
    const unset = { request_override: '' } as any;
    expect(formFromModel(unset, null, [], '').imageResponseFormat).toBe('');
  });
});
