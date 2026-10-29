import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ModelControl from '../src/chat/ModelControl.vue';
import { models, selectedID } from '../src/chat/useModels';
import * as auth from '../src/api/auth';

// The model list is a palette: typing narrows it, the arrows move through
// what is left without leaving the field, and Enter chooses.

function model(id: string, name: string, description = '') {
  return {
    id, display_name: name, description, avatar: '',
    supports_reasoning: false, supports_images: false, supports_vision: false, supports_streaming: true,
    supports_system_prompt: true, supports_tools: false, supports_image_gen: false,
    supports_chat_image_gen: false, context_window: 0, max_output_tokens: 0,
  };
}

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  vi.spyOn(auth, 'savePreferences').mockResolvedValue({ preferences: {} });
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
  models.value = [model('a', 'Alpha Mini'), model('b', 'Beta Large', 'long context'), model('c', 'Gamma')];
  selectedID.value = 'a';
});

afterEach(() => {
  app?.unmount();
  app = null;
  vi.restoreAllMocks();
});

async function openList(): Promise<HTMLInputElement> {
  app = createApp(ModelControl);
  app.mount(host);
  host.querySelector<HTMLButtonElement>('.ai-model-chip')!.click();
  await nextTick();
  host.querySelector<HTMLButtonElement>('.ai-pop-model')!.click();
  await nextTick();
  await nextTick();
  return host.querySelector<HTMLInputElement>('.ai-pop-search-input')!;
}

function rows(): string[] {
  // By the row's own first name: the marquee may draw a second copy of a name
  // to scroll, which is decoration and not a second row.
  const names = ['Alpha Mini', 'Beta Large', 'Gamma'];
  return [...host.querySelectorAll('.ai-pop-model-row')]
    .map((node) => names.find((name) => node.textContent?.includes(name)) ?? '');
}

it('opens on the current model and takes the keyboard into the search field', async () => {
  const search = await openList();
  expect(document.activeElement).toBe(search);
  expect(host.querySelector('.ai-pop-model-row.highlight')?.textContent).toContain('Alpha Mini');
});

it('narrows by name or description, and says so when nothing matches', async () => {
  const search = await openList();
  search.value = 'context';
  search.dispatchEvent(new Event('input'));
  await nextTick();
  expect(rows()).toEqual(['Beta Large']);

  search.value = 'nothing like it';
  search.dispatchEvent(new Event('input'));
  await nextTick();
  expect(rows()).toEqual([]);
  expect(host.querySelector('.ai-pop-note')).not.toBeNull();
});

it('chooses with the arrows and Enter, wrapping at the ends', async () => {
  const search = await openList();
  const key = (name: string) => search.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true }));
  key('ArrowUp');
  await nextTick();
  expect(host.querySelector('.ai-pop-model-row.highlight')?.textContent).toContain('Gamma');
  key('Enter');
  await nextTick();
  expect(selectedID.value).toBe('c');
});

// The sheet under the list follows the pointer, and the popover hangs from its
// bottom edge: a sheet that changed height as it followed moved the list under
// the pointer, lit the next row, and changed again — the list shook.
it('keeps the sheet one height whichever row the pointer is on', async () => {
  models.value = [
    { ...model('a', 'Alpha Mini'), context_window: 128_000, max_output_tokens: 8192, supports_images: true },
    model('b', 'Beta Large'),
    { ...model('c', 'Gamma'), context_window: 32_000 },
  ];
  await openList();
  const heights = new Set<number>();
  for (const row of host.querySelectorAll<HTMLElement>('.ai-pop-model-row')) {
    row.dispatchEvent(new Event('pointermove'));
    await nextTick();
    heights.add(host.querySelectorAll('.ai-pop-spec > div').length);
  }
  expect([...heights]).toEqual([3]);
});
