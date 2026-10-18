import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  BACKGROUND_TYPES,
  imageProblem,
  imageType,
  LOGO_TYPES,
  MAX_BACKGROUND_BYTES,
  MAX_LOGO_BYTES,
  pickImage,
  readAsBase64,
} from '../src/admin/upload';
import { ApiError } from '../src/api/client';
import { t } from '../src/composables/useI18n';

function sized(name: string, type: string, size: number): File {
  const file = new File(['x'], name, { type });
  Object.defineProperty(file, 'size', { value: size });
  return file;
}

describe('admin image uploads', () => {
  afterEach(() => vi.restoreAllMocks());

  it('reads the type the browser reports before the name', () => {
    expect(imageType(new File([''], 'a.bin', { type: 'image/JPG' }), BACKGROUND_TYPES)).toBe('image/jpeg');
    expect(imageType(new File([''], 'a.png', { type: 'image/webp' }), BACKGROUND_TYPES)).toBe('image/webp');
    expect(imageType(new File([''], 'photo.AVIF', { type: '' }), BACKGROUND_TYPES)).toBe('image/avif');
  });

  it('keeps each slot to what its server endpoint takes', () => {
    const icon = new File([''], 'favicon.ico', { type: 'image/vnd.microsoft.icon' });
    expect(imageType(icon, LOGO_TYPES)).toBe('image/x-icon');
    expect(imageType(icon, BACKGROUND_TYPES)).toBe('');
    expect(imageType(new File([''], 'logo.svg', { type: '' }), BACKGROUND_TYPES)).toBe('');
    expect(imageType(new File([''], 'logo.svg', { type: '' }), LOGO_TYPES)).toBe('image/svg+xml');
  });

  it('says a file is too large rather than listing the formats again', () => {
    const formats = t('loginBgFormats');
    expect(imageProblem(sized('a.png', 'image/png', MAX_BACKGROUND_BYTES), BACKGROUND_TYPES, MAX_BACKGROUND_BYTES, formats)).toBe('');
    const big = imageProblem(sized('a.png', 'image/png', MAX_BACKGROUND_BYTES + 1), BACKGROUND_TYPES, MAX_BACKGROUND_BYTES, formats);
    expect(big).toBe(t('imageTooLarge'));
    expect(imageProblem(sized('a.gif', 'image/gif', 10), BACKGROUND_TYPES, MAX_BACKGROUND_BYTES, formats)).toBe(formats);
    expect(imageProblem(sized('a.svg', 'image/svg+xml', MAX_LOGO_BYTES + 1), LOGO_TYPES, MAX_LOGO_BYTES, 'f'))
      .toBe(t('imageTooLarge'));
  });

  it('offers the picker the types and extensions of the slot', () => {
    const input = document.createElement('input');
    vi.spyOn(document, 'createElement').mockReturnValue(input);
    const click = vi.spyOn(input, 'click').mockImplementation(() => {});
    pickImage(BACKGROUND_TYPES, () => {});
    expect(click).toHaveBeenCalledOnce();
    expect(input.type).toBe('file');
    expect(input.accept).toBe('image/jpeg,image/png,image/webp,image/avif,.jpg,.jpeg,.png,.webp,.avif');
  });

  it('sends the bytes as bare base64', async () => {
    expect(await readAsBase64(new File(['hello'], 'a.png', { type: 'image/png' }))).toBe(btoa('hello'));
  });

  it('reports a file it cannot read in the interface language', async () => {
    vi.spyOn(FileReader.prototype, 'readAsDataURL').mockImplementation(function (this: FileReader) {
      queueMicrotask(() => this.onerror?.(new ProgressEvent('error') as ProgressEvent<FileReader>));
    });
    const failure = await readAsBase64(new File(['x'], 'a.png')).catch((err: unknown) => err);
    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).message).toBe(t('imageFailed'));
  });
});
