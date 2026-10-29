import { afterEach, beforeAll, expect, it } from 'vitest';
import { createApp, h, type App } from 'vue';
import ModelTile from '../src/chat/ModelTile.vue';
import { guessMark, loadVendorMarks, modelMarks } from '../src/chat/model-marks';
import { blankModelForm, modelPayload } from '../src/views/admin/modelForm';

// A model's tile is decided by its avatar field, and an empty one is a guess
// from the name — so an instance that never opens the editor still gets marks
// for the families people recognise, and initials for the rest.

// The vendors' marks are a chunk of their own; every test here wants them.
beforeAll(() => loadVendorMarks());

let app: App | null = null;
function reset(): void { app?.unmount(); app = null; document.body.textContent = ''; }
afterEach(reset);

function tile(avatar: string, name: string): HTMLElement {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({ render: () => h(ModelTile, { avatar, name }) });
  app.mount(host);
  return host.querySelector('.ai-model-tile')!;
}

it('guesses the family from the words vendors put in names', () => {
  expect(guessMark('Claude Opus 5')?.id).toBe('claude');
  expect(guessMark('gpt-5.6-sol')?.id).toBe('openai');
  expect(guessMark('o3 mini')?.id).toBe('openai');
  expect(guessMark('DeepSeek V4')?.id).toBe('deepseek');
  expect(guessMark('Gemini 3 Pro')?.id).toBe('gemini');
  expect(guessMark('Qwen3 Max')?.id).toBe('qwen');
  expect(guessMark('Mixtral 8x22B')?.id).toBe('mistral');
  expect(guessMark('GLM-4.6')?.id).toBe('zhipu');
  expect(guessMark('豆包 1.6')?.id).toBe('doubao');
  // "o3" inside another word is not OpenAI's.
  expect(guessMark('Pro3 house model')).toBeNull();
  expect(guessMark('Our house model')).toBeNull();
});

it('draws every mark with a path and a label', () => {
  expect(modelMarks.value.map((mark) => mark.id)).toEqual(expect.arrayContaining(['openai', 'claude', 'deepseek', 'gemini', 'qwen']));
  for (const mark of modelMarks.value) {
    expect(mark.shapes.length, mark.id).toBeGreaterThan(0);
    expect(mark.name ?? mark.labelKey, mark.id).toBeTruthy();
  }
});

it('wears the chosen mark, the guess, the initials or a picture', () => {
  expect(tile('mark:gemini', 'Anything').querySelector('svg path')).not.toBeNull();
  expect(tile('', 'Claude Y').querySelector('svg')).not.toBeNull();
  reset();
  expect(tile('mark:none', 'Claude Y').textContent?.trim()).not.toBe('');
  reset();
  expect(tile('mark:none', 'Claude Y').querySelector('svg')).toBeNull();
  reset();
  expect(tile('data:image/png;base64,AAAA', 'X').querySelector('img')?.getAttribute('src')).toBe('data:image/png;base64,AAAA');
});

it('does not point an image at another site, which the page would refuse to load', () => {
  const node = tile('https://elsewhere.example/logo.png', 'House Model');
  expect(node.querySelector('img')).toBeNull();
  expect(node.textContent?.trim()).not.toBe('');
});

it('saves the avatar with the rest of the form', () => {
  const form = { ...blankModelForm(), avatar: ' mark:qwen ' };
  expect(modelPayload(form, '').avatar).toBe('mark:qwen');
});
