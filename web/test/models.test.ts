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

describe('worstCase and priciest calculations', () => {
  function adminModel(id: string, req: number, out: number, maxOut: number): any {
    return {
      id,
      provider_id: 'p1',
      provider_name: 'P1',
      model_id: id,
      display_name: id,
      enabled: true,
      max_output_tokens: maxOut,
      request_weight: req,
      output_token_weight: out,
    };
  }

  it('caps reservation ceiling at 4096 so large output limits do not inflate worst case', async () => {
    const { worstCase, priciest } = await import('../src/views/admin/shared');

    const deepseek = adminModel('deepseek', 1, 1, 256004);
    // 1 + (4096 / 1000) * 1 = 5.096
    expect(worstCase(deepseek)).toBeCloseTo(5.096, 3);

    const claude = adminModel('claude', 0, 15, 0);
    // 0 + (4096 / 1000) * 15 = 61.44
    expect(worstCase(claude)).toBeCloseTo(61.44, 2);

    const small = adminModel('small', 0, 2, 1000);
    // 0 + (1000 / 1000) * 2 = 2
    expect(worstCase(small)).toBe(2);

    expect(priciest([deepseek, claude, small])?.id).toBe('claude');
  });

  it('lists the dearest few, dearest first', async () => {
    const { priciestFew } = await import('../src/views/admin/shared');
    const models = [adminModel('a', 0, 1, 0), adminModel('b', 0, 3, 0), adminModel('c', 0, 2, 0)];
    expect(priciestFew(models, 2).map((m) => m.id)).toEqual(['b', 'c']);
    expect(models.map((m) => m.id)).toEqual(['a', 'b', 'c']);
  });
});
