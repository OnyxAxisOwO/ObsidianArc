import { afterEach, describe, expect, it, vi } from 'vitest';

import { currentModel, loadModels, models, selectModel, selectedID } from '../src/chat/useModels';
import type { AvailableModel } from '../src/chat/useModels';

// Being able to generate in the image lab and being able to hold a
// conversation are two different capabilities, and for a while one flag stood
// for both: ticking "image generation" so a model could be used in the lab
// took it out of the composer's picker entirely, which is not what an operator
// ticking that box is asking for. Most models that draw can also talk.
//
// What a conversation shows is the other flag, supports_chat_image_gen, and
// it is a tag rather than a gate.

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
    supports_chat_image_gen: false,
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
  it('keeps a model the image lab can use', async () => {
    answerWith([PAINTER, TALKER]);
    await loadModels();

    expect(models.value.map((entry) => entry.id)).toEqual(['painter', 'talker']);
    // It is the first usable model, so it is what the composer settles on —
    // it used to be skipped over as though it were not a chat model at all.
    expect(selectedID.value).toBe('painter');
    expect(currentModel.value?.id).toBe('painter');
  });

  it('lets one be chosen outright', () => {
    models.value = [PAINTER, TALKER];
    selectedID.value = 'talker';
    selectModel('painter');
    expect(selectedID.value).toBe('painter');
  });

  it('still falls back when the remembered model is gone', async () => {
    selectedID.value = 'retired';
    answerWith([TALKER]);
    await loadModels();
    expect(selectedID.value).toBe('talker');
  });

  it('reports no model when there is nothing usable', async () => {
    answerWith([model('shelved', { usable: false })]);
    await loadModels();
    expect(selectedID.value).toBe('');
    // What the composer reads to decide whether it can send at all.
    expect(currentModel.value).toBeNull();
  });
});
