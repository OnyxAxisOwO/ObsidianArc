import { afterEach, expect, it, vi } from 'vitest';
import * as image from '../src/chat/image';
import { api } from '../src/api/client';
import { adopt, forget } from '../src/stores/session';
import WallpaperSection from '../src/views/settings/WallpaperSection.vue';
import { mountComponent, settle } from './helpers';
import { makeAccount } from './fixtures/account';

let unmount: (() => void) | undefined;
afterEach(() => { unmount?.(); vi.restoreAllMocks(); forget(); });

it('does not upload a decoded image under the next account', async () => {
  adopt(makeAccount({ id: 'first' }));
  let finish!: (value: Awaited<ReturnType<typeof image.prepareImage>>) => void;
  vi.spyOn(image, 'prepareImage').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const put = vi.spyOn(api, 'put');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const host = document.createElement('div'); document.body.append(host);
  unmount = await mountComponent(WallpaperSection, {}, { host });
  const input = host.querySelector<HTMLInputElement>('input[type=file]')!;
  Object.defineProperty(input, 'files', { value: [new File(['x'], 'x.png', { type: 'image/png' })] });
  input.dispatchEvent(new Event('change', { bubbles: true }));
  await settle();
  forget(); adopt(makeAccount({ id: 'second' }));
  finish({ mime: 'image/png', data: 'private', width: 1, height: 1, bytes: 1, previewURL: 'blob:test' });
  await settle();
  expect(put).not.toHaveBeenCalled();
  host.remove();
});
