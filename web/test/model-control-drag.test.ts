import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import ModelControl from '../src/chat/ModelControl.vue';
import { models, reasoning, selectedID, setReasoning } from '../src/chat/useModels';

// The real setter reaches the account's preferences over the network; the
// only thing these tests need from it is whether it was called.
vi.mock('../src/chat/useModels', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/chat/useModels')>()),
  setReasoning: vi.fn(),
}));

const model = {
  id: 'm1',
  display_name: 'Model One',
  description: '',
  avatar: '',
  supports_reasoning: true,
  supports_images: false,
  supports_vision: false,
  supports_streaming: true,
  supports_system_prompt: true,
  supports_tools: false,
  supports_image_gen: false,
  supports_chat_image_gen: false,
  context_window: 0,
  max_output_tokens: 0,
};

let app: App | null = null;
let host: HTMLElement;

/** Opens the popover and returns the effort slider inside it. */
async function openSlider(): Promise<HTMLInputElement> {
  host.querySelector<HTMLButtonElement>('.ai-model-chip')!.click();
  await nextTick();
  const range = host.querySelector<HTMLInputElement>('.ai-effort-range');
  expect(range).not.toBeNull();
  return range!;
}

describe('ModelControl effort drag', () => {
  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    models.value = [model];
    selectedID.value = model.id;
    reasoning.value = { enabled: false, effort: 'medium' };
    vi.mocked(setReasoning).mockClear();
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.body.textContent = '';
  });

  it('commits the stop it was released on', async () => {
    app = createApp(ModelControl);
    app.mount(host);
    const range = await openSlider();

    range.dispatchEvent(new Event('pointerdown'));
    range.value = '2';
    range.dispatchEvent(new Event('input'));
    window.dispatchEvent(new Event('pointerup'));

    expect(setReasoning).toHaveBeenCalledTimes(1);
    expect(setReasoning).toHaveBeenCalledWith({ enabled: true, effort: 'medium' });
  });

  it('does not commit a drag the component was unmounted during', async () => {
    app = createApp(ModelControl);
    app.mount(host);
    const range = await openSlider();

    range.dispatchEvent(new Event('pointerdown'));
    range.value = '2';
    range.dispatchEvent(new Event('input'));
    app.unmount();
    app = null;

    // Without the teardown the window listeners would still be attached here,
    // and this release would write the account's setting from a dead component.
    window.dispatchEvent(new Event('pointerup'));

    expect(setReasoning).not.toHaveBeenCalled();
  });

  it('does not let a second finger orphan the first drag', async () => {
    app = createApp(ModelControl);
    app.mount(host);
    const range = await openSlider();

    // Two fingers on the track: the second press replaces the first drag's
    // teardown, so the first drag's release listener has to come off with it.
    range.dispatchEvent(new Event('pointerdown'));
    range.dispatchEvent(new Event('pointerdown'));
    range.value = '2';
    range.dispatchEvent(new Event('input'));
    app.unmount();
    app = null;

    window.dispatchEvent(new Event('pointerup'));

    expect(setReasoning).not.toHaveBeenCalled();
  });

  // A side panel beside the composer also closes on Escape, and yields only to
  // a key already handled. The chip's popover answered in the bubble phase
  // without saying so, and one press closed both.
  it('takes Escape before a panel beneath it, and marks it handled', async () => {
    app = createApp(ModelControl);
    app.mount(host);
    await openSlider();

    let seenByPanel: boolean | null = null;
    const panel = (event: KeyboardEvent) => { seenByPanel = event.defaultPrevented; };
    document.addEventListener('keydown', panel);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    document.removeEventListener('keydown', panel);

    expect(seenByPanel).toBe(true);
    await nextTick();
    expect(host.querySelector('.ai-model-chip')!.getAttribute('aria-expanded')).not.toBe('true');
  });
});
