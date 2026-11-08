import { afterEach, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ModelControl from '../src/chat/ModelControl.vue';
import { models, selectedID } from '../src/chat/useModels';

const load = vi.hoisted(() => {
  let finish = () => {};
  const ready = new Promise<void>((resolve) => { finish = resolve; });
  return { ready, finish };
});
vi.mock('../src/chat/ModelInfo.vue', async (original) => {
  const component = await original();
  await load.ready;
  return component;
});

let app: App | null = null;
afterEach(() => { load.finish(); app?.unmount(); });

it('keeps the list until details load, then replaces its face and returns to the same search', async () => {
  models.value = [{
    id: 'model', display_name: 'Alpha', api_name: 'alpha-api', description: '', avatar: '',
    supports_reasoning: false, supports_images: false, supports_vision: false, supports_streaming: true,
    supports_system_prompt: true, supports_tools: false, supports_image_gen: false,
    supports_chat_image_gen: false, context_window: 0, max_output_tokens: 0,
  }];
  selectedID.value = 'model';
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp(ModelControl);
  app.mount(host);
  host.querySelector<HTMLButtonElement>('.ai-model-chip')!.click();
  await nextTick();
  host.querySelector<HTMLButtonElement>('.ai-pop-model')!.click();
  await nextTick();
  await nextTick();
  const search = host.querySelector<HTMLInputElement>('.ai-pop-search-input')!;
  search.value = 'Alpha';
  search.dispatchEvent(new Event('input'));
  await nextTick();
  const pop = host.querySelector('.ai-model-pop')!;
  host.querySelector<HTMLButtonElement>('.ai-pop-model-info')!.click();
  await nextTick();
  expect(host.querySelector('.ai-chat-spinner')).not.toBeNull();
  expect(host.querySelector('.ai-model-info-view')).toBeNull();
  expect(host.querySelector('.ai-pop-search-input')).toBe(search);
  expect(pop.classList.contains('has-info')).toBe(false);

  load.finish();
  await vi.waitFor(() => expect(host.querySelector('.ai-model-info-view')).not.toBeNull());
  expect(host.querySelector('.ai-model-pop')).toBe(pop);
  expect(host.querySelector('.ai-model-pop-body')!.contains(host.querySelector('.ai-model-info-view'))).toBe(true);
  expect(host.querySelector('.ai-pop-search-input')).toBeNull();
  expect(selectedID.value).toBe('model');
  host.querySelector<HTMLButtonElement>('.ai-model-info-head .ai-pop-back')!.click();
  await nextTick();
  await nextTick();
  expect(host.querySelector<HTMLInputElement>('.ai-pop-search-input')!.value).toBe('Alpha');
  expect(host.querySelector('.ai-model-info-view')).toBeNull();
});
