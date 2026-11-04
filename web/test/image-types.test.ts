import { afterEach, describe, it, expect, vi } from 'vitest';
import { isSupportedImageType, prepareImage } from '../src/chat/image';

// What prepareImage can decode. Everything except GIF is re-encoded to JPEG
// before it is sent, so the question this answers is what the browser can
// read — not what a provider accepts.
describe('the image types that can be prepared', () => {
  // AVIF is offered by the wallpaper picker's accept list, by the image
  // composer's `image/*`, and stored by the server as image/avif. It was
  // missing here, so choosing the file the dialog had just offered decoded
  // successfully and then failed the allowlist with a format error.
  it('covers every type the pickers offer', () => {
    for (const type of ['image/png', 'image/jpeg', 'image/webp', 'image/avif', 'image/gif']) {
      expect(isSupportedImageType(type), type).toBe(true);
    }
  });

  it('still refuses a type that is not on the list', () => {
    for (const type of ['image/tiff', 'image/bmp', 'application/pdf', 'text/plain', '']) {
      expect(isSupportedImageType(type), type).toBe(false);
    }
  });
});

// The picture is painted onto a canvas and encoded as JPEG, which has no
// alpha. The browser stubs here record what was drawn and in which order.
describe('preparing a transparent picture', () => {
  const realURL = { createObjectURL: URL.createObjectURL };

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    Object.assign(URL, realURL);
  });

  it('paints a backing colour before the picture, so transparency does not encode as black', async () => {
    const calls: string[] = [];
    const context = {
      set fillStyle(value: string) { calls.push(`fillStyle:${value}`); },
      fillRect: () => { calls.push('fillRect'); },
      drawImage: () => { calls.push('drawImage'); },
    };
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockReturnValue(context as unknown as CanvasRenderingContext2D);
    const encoded = {
      size: 4,
      type: 'image/jpeg',
      arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer,
    };
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(function (callback: BlobCallback) {
      callback(encoded as unknown as Blob);
    });
    vi.stubGlobal('createImageBitmap', async () => ({ width: 10, height: 10, close: () => {} }));
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:preview') });

    const prepared = await prepareImage(new File(['png'], 'diagram.png', { type: 'image/png' }));

    expect(prepared.mime).toBe('image/jpeg');
    expect(calls).toEqual(['fillStyle:#fff', 'fillRect', 'drawImage']);
  });
});

// The decode is the expensive step, so what reaches it is decided first. A GIF
// that fits is passed through untouched, which means nothing downscales it, so
// its bytes are capped: a large animation would be re-sent at full cost on
// every later turn. One beyond the dimension ceiling is not refused; it takes
// the canvas path like a still picture.
describe('refusing an image before it is sent', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('refuses an unsupported type without decoding it', async () => {
    const decode = vi.fn(async () => ({ width: 10, height: 10, close: () => {} }));
    vi.stubGlobal('createImageBitmap', decode);

    await expect(prepareImage(new File(['tiff'], 'scan.tiff', { type: 'image/tiff' })))
      .rejects.toMatchObject({ kind: 'unsupported' });
    expect(decode).not.toHaveBeenCalled();
  });

  it('refuses a GIF whose bytes exceed the cap, however small its dimensions', async () => {
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', async () => ({ width: 40, height: 40, close }));
    const oversized = new File([new Uint8Array(4 * 1024 * 1024 + 1)], 'big.gif', { type: 'image/gif' });

    await expect(prepareImage(oversized)).rejects.toMatchObject({ kind: 'too-large' });
    expect(close).toHaveBeenCalled();
  });

  it('sends a GIF beyond the dimension ceiling as its first frame, not refused', async () => {
    const realCreateObjectURL = URL.createObjectURL;
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:first-frame') });
    vi.stubGlobal('createImageBitmap', async () => ({ width: 6000, height: 40, close: () => {} }));
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockReturnValue({ fillStyle: '', fillRect: () => {}, drawImage: () => {} } as unknown as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(function (callback: BlobCallback) {
      callback({
        size: 3,
        type: 'image/jpeg',
        arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer,
      } as unknown as Blob);
    });

    try {
      const prepared = await prepareImage(new File(['GIF89a'], 'wide.gif', { type: 'image/gif' }));
      expect(prepared.mime).toBe('image/jpeg');
      expect(prepared.width).toBe(1568);
      expect(prepared.height).toBe(10);
    } finally {
      Object.assign(URL, { createObjectURL: realCreateObjectURL });
    }
  });

  it('still passes a GIF within the ceiling through', async () => {
    const realCreateObjectURL = URL.createObjectURL;
    Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:gif') });
    // A browser empties a closed bitmap's size, so the size must be read first.
    vi.stubGlobal('createImageBitmap', async () => ({
      width: 800,
      height: 600,
      close() {
        this.width = 0;
        this.height = 0;
      },
    }));

    try {
      const prepared = await prepareImage(new File(['GIF89a'], 'ok.gif', { type: 'image/gif' }));
      expect(prepared.mime).toBe('image/gif');
      expect(prepared.width).toBe(800);
      expect(prepared.height).toBe(600);
    } finally {
      Object.assign(URL, { createObjectURL: realCreateObjectURL });
    }
  });
});
