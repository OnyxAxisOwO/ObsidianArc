// The image uploads of the settings page: the site logo and the backgrounds.
//
// Each upload checks the file here before sending it, so an operator who picks
// the wrong one hears why at once instead of after a 6 MB body has crossed the
// network. The checks only repeat the server's: it decides, and a limit moved
// there and forgotten here makes this an early answer that is wrong, never a
// way past the server.

import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';

// settings.MaxLoginBackgroundBytes and settings.MaxLogoBytes. Both sides count
// the file's own bytes, before base64, so the same number means the same file.
export const MAX_BACKGROUND_BYTES = 6 * 1024 * 1024;
export const MAX_LOGO_BYTES = 4 * 1024 * 1024;

// What a browser may report for each type, and the extensions that stand in
// when it reports nothing: some systems hand over an empty type for AVIF or ICO.
const IMAGE_TYPES = {
  'image/jpeg': { aliases: ['image/jpg'], extensions: ['.jpg', '.jpeg'] },
  'image/png': { aliases: [], extensions: ['.png'] },
  'image/webp': { aliases: [], extensions: ['.webp'] },
  'image/avif': { aliases: [], extensions: ['.avif'] },
  'image/gif': { aliases: [], extensions: ['.gif'] },
  'image/x-icon': { aliases: ['image/vnd.microsoft.icon'], extensions: ['.ico'] },
  'image/svg+xml': { aliases: [], extensions: ['.svg'] },
} as const satisfies Record<string, { aliases: readonly string[]; extensions: readonly string[] }>;

type ImageType = keyof typeof IMAGE_TYPES;

// Each upload names its own list, because each mirrors what the server takes
// for that slot: a logo may be an SVG or an icon, a background may not.
export const BACKGROUND_TYPES: readonly ImageType[] = ['image/jpeg', 'image/png', 'image/webp', 'image/avif'];
export const LOGO_TYPES: readonly ImageType[] = [
  'image/jpeg', 'image/png', 'image/webp', 'image/avif', 'image/gif', 'image/x-icon', 'image/svg+xml',
];

/** The type to send for this file, or '' when it is not one of `accepted`. */
export function imageType(file: File, accepted: readonly ImageType[]): ImageType | '' {
  // The reported type wins over the name, as it always has: a file renamed
  // to .png is still whatever the system says it is.
  const reported = file.type.toLowerCase();
  for (const type of accepted) {
    if (reported === type || (IMAGE_TYPES[type].aliases as readonly string[]).includes(reported)) return type;
  }
  const name = file.name.toLowerCase();
  for (const type of accepted) {
    if (IMAGE_TYPES[type].extensions.some((ext) => name.endsWith(ext))) return type;
  }
  return '';
}

/**
 * Why this file cannot be sent as one of `accepted` within `maxBytes`, or ''
 * when it can. A file of the right type that is too big hears that it is too
 * big, not the list of formats.
 */
export function imageProblem(file: File, accepted: readonly ImageType[], maxBytes: number, formats: string): string {
  if (!imageType(file, accepted)) return formats;
  if (file.size > maxBytes) return t('imageTooLarge');
  return '';
}

/** Opens the file picker for `accepted` and hands over the file chosen, if any. */
export function pickImage(accepted: readonly ImageType[], onFile: (file: File) => void): void {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = [...accepted, ...accepted.flatMap((type) => IMAGE_TYPES[type].extensions)].join(',');
  input.onchange = () => {
    const file = input.files?.[0];
    if (file) onFile(file);
  };
  input.click();
}

/**
 * The file's bytes as base64, the way the upload endpoints take them.
 *
 * A file that cannot be read rejects with an ApiError, so the callers that
 * show an ApiError's message show a translated sentence rather than
 * "Error: …" in English.
 */
export function readAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const res = String(reader.result ?? '');
      const comma = res.indexOf(',');
      resolve(comma !== -1 ? res.slice(comma + 1) : res);
    };
    reader.onerror = () => reject(new ApiError(0, 'unreadable', t('imageFailed')));
    reader.readAsDataURL(file);
  });
}
