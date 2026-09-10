import { afterEach, describe, expect, it, vi } from 'vitest';

import { chatModels, currentModel, loadModels, models, selectModel, selectedID } from '../src/chat/useModels';
import type { AvailableModel } from '../src/chat/useModels';

// An image model answers in the image lab and nowhere else — the gateway
// refuses a turn asked of one. The composer therefore must never be able to
// point at it: not by listing it, not by restoring it from a remembered
// preference, and not by falling back onto it when the previous choice is
// gone. This is the half of that rule the browser owns.

function model(id: string, extra: Partial<AvailableModel> = {}): AvailableModel {
  return {
    id,
    display_name: id,
    description: '',
    avatar: '',
    supports_reasoning: false,
    supports_images: false,
    supports_vision: false,
    supports_streaming: true,
    supports_system_prompt: true,
    supports_tools: false,
    supports_image_gen: false,
    context_window: 0,
    max_output_tokens: 0,
    ...extra,
  };
}

const PAINTER = model('painter', { supports_image_gen: true });
const TALKER = model('talker');

function answerWith(list: AvailableModel[]): void {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ models: list }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })));
}

afterEach(() => {
  vi.unstubAllGlobals();
  models.value = [];
  selectedID.value = '';
});

describe('which models a conversation may use', () => {
  it('leaves image models to the lab, which reads the full list itself', () => {
    models.value = [PAINTER, TALKER];
    expect(chatModels.value.map((entry) => entry.id)).toEqual(['talker']);
    // The lab's own source is untouched: it is the one place these belong.
    expect(models.value.length).toBe(2);
  });

  it('does not settle on one when it is first in the list', async () => {
    answerWith([PAINTER, TALKER]);
    await loadModels();
    expect(selectedID.value).toBe('talker');
    expect(currentModel.value?.id).toBe('talker');
  });

  it('drops a remembered choice that has since become an image model', async () => {
    selectedID.value = 'painter';
    answerWith([PAINTER, TALKER]);
    await loadModels();
    expect(selectedID.value).toBe('talker');
  });

  it('refuses to select one, whatever asks for it', () => {
    models.value = [PAINTER, TALKER];
    selectedID.value = 'talker';
    selectModel('painter');
    expect(selectedID.value).toBe('talker');
  });

  it('reports no model rather than an unusable one when the list is only images', async () => {
    answerWith([PAINTER]);
    await loadModels();
    expect(selectedID.value).toBe('');
    // What the composer reads to decide whether it can send at all.
    expect(currentModel.value).toBeNull();
  });
});
