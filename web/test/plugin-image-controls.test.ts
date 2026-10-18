// The pictures a plugin's form asks for, as somebody who cannot use a pointer
// meets them: adding one, opening one, and taking one away.
//
// A label cannot take focus and a hidden input cannot be reached by Tab, so the
// add control has to be a button, and the thumbnails have to answer the keyboard
// as well as the mouse.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import * as chatImage from '@/chat/image';
import { changeLanguage, t } from '@/composables/useI18n';
import PluginUserControls from '@/views/PluginUserControls.vue';
import type { UserFormControl } from '@/plugins/types';

const PICTURE = 'data:image/png;base64,AAAA';

const control = {
  kind: 'images',
  key: 'photos',
  label: () => 'Photos',
  max: 3,
} as UserFormControl;

let app: App | null = null;
let host: HTMLElement;

// Not mountComponent: it waits a single tick rather than settle's four rounds; moving it would change that under these tests.
async function mount(values: Record<string, unknown>, handlers: { onView?: (url: string) => void } = {}): Promise<void> {
  app = createApp({
    render: () => h(PluginUserControls, { controls: [control], values: values as never, ...handlers }),
  });
  app.mount(host);
  await nextTick();
}

function addButton(): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === t('pluginImagesAdd'));
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  host.remove();
  vi.restoreAllMocks();
});

describe('the pictures a plugin form asks for', () => {
  it('adds a picture from a button a keyboard can reach, which opens the picker', async () => {
    await mount({ photos: [] });

    const add = addButton();
    expect(add).toBeDefined();
    expect(add!.tagName).toBe('BUTTON');
    expect(add!.type).toBe('button');

    const picker = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    const click = vi.spyOn(picker, 'click');
    add!.click();
    expect(click).toHaveBeenCalledOnce();
  });

  it('stops offering to add once the most pictures are there', async () => {
    await mount({ photos: [PICTURE, PICTURE, PICTURE] });

    expect(addButton()).toBeUndefined();
  });

  it('opens a picture from the keyboard as well as the mouse', async () => {
    const onView = vi.fn();
    await mount({ photos: [PICTURE] }, { onView });

    const thumb = host.querySelector<HTMLImageElement>('.oa-plugin-image img')!;
    expect(thumb.getAttribute('role')).toBe('button');
    expect(thumb.tabIndex).toBe(0);
    expect(thumb.getAttribute('aria-label')).toBe(t('zoomImage'));

    thumb.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(onView).toHaveBeenLastCalledWith(PICTURE);

    thumb.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }));
    expect(onView).toHaveBeenCalledTimes(2);
  });

  it('gives each remove button a name, not only a tooltip', async () => {
    await mount({ photos: [PICTURE, PICTURE] });

    const removers = host.querySelectorAll<HTMLButtonElement>('.oa-plugin-image button');
    expect(removers).toHaveLength(2);
    for (const remover of removers) {
      expect(remover.getAttribute('aria-label')).toBe(t('pluginImagesRemove'));
    }
  });
});

describe('pictures picked while an earlier pick is still being decoded', () => {
  function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
  }

  function choose(picker: HTMLInputElement, file: File): void {
    Object.defineProperty(picker, 'files', { value: [file], configurable: true });
    picker.dispatchEvent(new Event('change'));
  }

  it('keeps both picks, rather than the earlier one overwriting the later', async () => {
    const values: Record<string, unknown> = { photos: [] };
    const slow = deferred<Awaited<ReturnType<typeof chatImage.prepareImage>>>();
    const prepare = vi.spyOn(chatImage, 'prepareImage')
      .mockReturnValueOnce(slow.promise)
      .mockResolvedValueOnce({ mime: 'image/png', data: 'BBBB', previewURL: 'blob:b' } as never);
    Object.assign(URL, { revokeObjectURL: vi.fn() });
    app = createApp({
      render: () => h(PluginUserControls, {
        controls: [control],
        values: values as never,
        onSet: (key: string, value: unknown) => { values[key] = value; },
      }),
    });
    app.mount(host);
    await nextTick();

    const picker = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    choose(picker, new File(['a'], 'a.png', { type: 'image/png' }));
    choose(picker, new File(['b'], 'b.png', { type: 'image/png' }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(prepare).toHaveBeenCalledTimes(2);
    expect(values['photos']).toEqual(['data:image/png;base64,BBBB']);

    slow.resolve({ mime: 'image/png', data: 'AAAA', previewURL: 'blob:a' } as never);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(values['photos']).toEqual(['data:image/png;base64,BBBB', 'data:image/png;base64,AAAA']);
  });
});
