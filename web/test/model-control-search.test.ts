import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ModelControl from '../src/chat/ModelControl.vue';
import { models, selectedID } from '../src/chat/useModels';
import * as auth from '../src/api/auth';
import { t } from '../src/composables/useI18n';
import * as clipboard from '../src/lib/clipboard';

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

async function openInfo(index: number): Promise<void> {
  host.querySelectorAll<HTMLButtonElement>('.ai-pop-model-info')[index]!.click();
  await vi.waitFor(() => expect(host.querySelector('.ai-model-info-title')).not.toBeNull());
  await nextTick();
}

it('keeps details and keyboard hints out of the list until information is requested', async () => {
  await openList();
  for (const row of host.querySelectorAll<HTMLElement>('.ai-pop-model-row')) {
    row.dispatchEvent(new Event('pointermove'));
    await nextTick();
    expect(host.querySelector('.ai-model-info-pane')).toBeNull();
  }
  expect(host.querySelector('.ai-pop-spec')).toBeNull();
  expect(host.querySelector('.ai-pop-keys')).toBeNull();
});

it('opens and switches information without selecting, and hides unknown fields', async () => {
  models.value[0] = { ...models.value[0]!, api_name: 'alpha-api', context_window: 128_001, supports_images: true };
  models.value[1]!.api_name = 'beta-api';
  await openList();
  await openInfo(0);
  const sheet = () => host.querySelector('.ai-model-info-pane')!;
  expect(sheet().textContent).toContain('128001');
  expect(sheet().textContent).toContain(t('specVision'));
  expect(sheet().textContent).not.toContain(t('specOutput'));
  expect(sheet().textContent).not.toContain('—');

  await openInfo(1);
  expect(sheet().textContent).toContain('Beta Large');
  expect(sheet().textContent).toContain('long context');
  expect(sheet().textContent).toContain('beta-api');
  expect(sheet().textContent).not.toContain('alpha-api');
  expect(sheet().textContent).not.toContain(t('specContext'));
  expect(selectedID.value).toBe('a');
  expect(host.querySelector('.ai-pop-search-input')).not.toBeNull();
});

it('copies the API name or complete information and resets feedback on another model', async () => {
  const copy = vi.spyOn(clipboard, 'copyToClipboard').mockResolvedValue(true);
  models.value[1] = { ...models.value[1]!, api_name: 'beta-api', max_output_tokens: 8192 };
  await openList();
  await openInfo(1);
  host.querySelector<HTMLButtonElement>('.ai-model-info-api-copy')!.click();
  await vi.waitFor(() => expect(copy).toHaveBeenCalledWith('beta-api'));
  await vi.waitFor(() => expect(host.querySelector('.ai-model-info-api-copy')!.getAttribute('aria-label')).toContain(t('copied')));

  host.querySelector<HTMLButtonElement>('.ai-model-info-copy')!.click();
  await vi.waitFor(() => expect(copy).toHaveBeenCalledWith(
    `Beta Large\nlong context\n${t('specOutput')}: 8192\n${t('apiNameLabel')}: beta-api`,
  ));
  await vi.waitFor(() => expect(host.querySelector('.ai-model-info-copy')!.textContent).toContain(t('copied')));
  await openInfo(0);
  expect(host.querySelector('.ai-model-info-copy')!.textContent).toContain(t('modelInfoCopy'));
});

it('allows information for an unavailable model while preventing selection', async () => {
  models.value[1]!.usable = false;
  await openList();
  expect(host.querySelectorAll<HTMLButtonElement>('.ai-pop-model-pick')[1]!.disabled).toBe(true);
  await openInfo(1);
  expect(host.querySelector('.ai-model-info-pane')!.textContent).toContain(t('modelNotAllowedGroup'));
  expect(selectedID.value).toBe('a');
});

it('returns from details with Escape, restores focus, then closes the picker on a second Escape', async () => {
  await openList();
  const info = host.querySelector<HTMLButtonElement>('.ai-pop-model-info')!;
  await openInfo(0);
  const escape = () => document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
  escape();
  await nextTick();
  await vi.waitFor(() => expect(host.querySelector('.ai-model-pop')!.classList.contains('has-info')).toBe(false));
  await vi.waitFor(() => expect(document.activeElement).toBe(info));
  expect(host.querySelector('.ai-model-chip')!.getAttribute('aria-expanded')).toBe('true');
  escape();
  await nextTick();
  expect(host.querySelector('.ai-model-chip')!.getAttribute('aria-expanded')).toBe('false');
});

it('shows only the model entry when the model has no reasoning control', async () => {
  app = createApp(ModelControl);
  app.mount(host);
  host.querySelector<HTMLButtonElement>('.ai-model-chip')!.click();
  await nextTick();
  expect(host.querySelector('.ai-pop-model')).not.toBeNull();
  expect(host.querySelector('.ai-pop-note')).toBeNull();
  expect(host.querySelector('.ai-effort-range')).toBeNull();
});
