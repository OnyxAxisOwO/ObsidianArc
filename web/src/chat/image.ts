// Turns a picked, dropped or pasted image File into something worth sending.
//
// Downscaled through a canvas before it leaves the browser. A phone photo is
// four thousand pixels wide and several megabytes, and every attachment is
// re-sent to the provider on every later turn of the conversation — so an
// oversized picture multiplies its cost on every message after the one that
// attached it, as well as filling the server's database.

import { t } from '@/composables/useI18n';

export interface PreparedImage {
  mime: string;
  /** Base64, no data: prefix — what the upload endpoint takes. */
  data: string;
  width: number;
  height: number;
  bytes: number;
  /** A blob: URL for the composer thumbnail. Revoke it when done. */
  previewURL: string;
}

// Anthropic's own guidance for the useful ceiling on an image's long edge.
// Above it, a model gains nothing and the request costs more.
const MAX_DIMENSION = 1568;
const MAX_OUTPUT_BYTES = 4 * 1024 * 1024;
const JPEG_QUALITY_STEPS = [0.85, 0.7, 0.55, 0.4];

export class ImageError extends Error {
  readonly kind: 'unsupported' | 'too-large' | 'unreadable';

  constructor(kind: ImageError['kind'], message: string) {
    super(message);
    this.name = 'ImageError';
    this.kind = kind;
  }
}

// Everything else is re-encoded to JPEG before it is sent, so the list is
// about what the browser can decode, not about what a provider accepts. AVIF
// is on it because the wallpaper picker offers it in its accept list and the
// server stores image/avif: without it, choosing the file the dialog had just
// offered decoded fine and then failed the allowlist with a format error.
const SUPPORTED = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/avif', 'image/gif']);

/** Whether prepareImage can turn this type into something worth sending. */
export function isSupportedImageType(type: string): boolean {
  return SUPPORTED.has(type);
}

export async function prepareImage(file: File): Promise<PreparedImage> {
  if (!file.type.startsWith('image/')) {
    throw new ImageError('unsupported', t('imageNotAnImage'));
  }
  // Refused before the decode: a format this list does not name is not worth
  // the memory of decoding it first, and the decode is the expensive step.
  if (!isSupportedImageType(file.type)) {
    throw new ImageError('unsupported', t('imageFormats'));
  }

  const bitmap = await decode(file);

  // A GIF that fits is passed through untouched: a re-encode would lose the
  // animation, and the providers accept it as-is. Its bytes are capped because
  // nothing downscales it, or a large animation would be re-sent at full cost
  // on every later turn. A GIF beyond the dimension ceiling is sent as its
  // first frame instead, so the picture is not lost to the user.
  if (file.type === 'image/gif') {
    if (file.size > MAX_OUTPUT_BYTES) {
      bitmap.close?.();
      throw new ImageError('too-large', t('imageTooLarge'));
    }
    if (Math.max(bitmap.width, bitmap.height) <= MAX_DIMENSION) {
      // Read before close(): a closed bitmap no longer reports its size.
      const { width, height } = bitmap;
      const data = await toBase64(file);
      bitmap.close?.();
      return {
        mime: 'image/gif',
        data,
        width,
        height,
        bytes: file.size,
        previewURL: URL.createObjectURL(file),
      };
    }
  }

  const longEdge = Math.max(bitmap.width, bitmap.height);
  const scale = longEdge > MAX_DIMENSION ? MAX_DIMENSION / longEdge : 1;
  const width = Math.max(1, Math.round(bitmap.width * scale));
  const height = Math.max(1, Math.round(bitmap.height * scale));

  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext('2d');
  if (!context) {
    bitmap.close?.();
    throw new ImageError('unreadable', t('imageFailed'));
  }
  // The canvas starts transparent and the output is JPEG, which has no alpha:
  // without a backing colour every transparent pixel of a PNG or WebP
  // becomes black, including in the composer's preview.
  context.fillStyle = '#fff';
  context.fillRect(0, 0, width, height);
  context.drawImage(bitmap, 0, 0, width, height);
  bitmap.close?.();

  // Normalised to JPEG: it compresses a photograph far better than PNG, and
  // both provider protocols accept it. Quality drops step by step until the
  // result fits rather than failing on the first try.
  for (const quality of JPEG_QUALITY_STEPS) {
    const blob = await toBlob(canvas, 'image/jpeg', quality);
    if (blob && blob.size <= MAX_OUTPUT_BYTES) {
      return {
        mime: 'image/jpeg',
        data: await toBase64(blob),
        width,
        height,
        bytes: blob.size,
        previewURL: URL.createObjectURL(blob),
      };
    }
  }
  throw new ImageError('too-large', t('imageTooLarge'));
}

// Decoded at full size, then drawn down on a canvas. The target size is worked
// out from the picture's own dimensions, which only the decode provides here,
// and a resize during the decode would encode every upload differently. The
// full-size bitmap is held for the one draw and closed straight after it.
async function decode(file: File): Promise<ImageBitmap> {
  try {
    return await createImageBitmap(file);
  } catch {
    throw new ImageError('unreadable', t('imageFailed'));
  }
}

function toBlob(canvas: HTMLCanvasElement, type: string, quality: number): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, type, quality));
}

async function toBase64(blob: Blob): Promise<string> {
  const buffer = await blob.arrayBuffer();
  const bytes = new Uint8Array(buffer);

  // Chunked: String.fromCharCode with a hundred thousand arguments overflows
  // the call stack on a large image.
  let binary = '';
  const chunk = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunk) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk));
  }
  return btoa(binary);
}
