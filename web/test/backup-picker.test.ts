// The import picker is where a visitor's file meets the account, and the
// failures it reports are shown on screen as they are. Dismissing the picker
// is not an error and must settle the promise; the three refusals must read
// in the reader's language, not in the English the code was written in.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { changeLanguage, t } from '@/composables/useI18n';
import { pickJSONFile } from '@/api/backup';
import { ApiError } from '@/api/client';

/** Captures the file input pickJSONFile creates, so the test can choose a file in it. */
function capturePicker(): () => HTMLInputElement {
  const real = document.createElement.bind(document);
  let input: HTMLInputElement | null = null;
  vi.spyOn(document, 'createElement').mockImplementation((tag: string, options?: ElementCreationOptions) => {
    const element = real(tag, options);
    if (tag === 'input') input = element as HTMLInputElement;
    return element;
  });
  return () => {
    if (!input) throw new Error('no picker was created');
    return input;
  };
}

function choose(input: HTMLInputElement, file: File | null): void {
  Object.defineProperty(input, 'files', { value: file ? [file] : [], configurable: true });
  input.dispatchEvent(new Event('change'));
}

afterEach(async () => {
  vi.restoreAllMocks();
  await changeLanguage('en');
});

describe('pickJSONFile', () => {
  it('resolves null when the picker is dismissed', async () => {
    const picker = capturePicker();
    const pending = pickJSONFile();

    picker().dispatchEvent(new Event('cancel'));

    await expect(pending).resolves.toBeNull();
  });

  it('refuses a compact body over the limit with a message in the reader\'s language', async () => {
    await changeLanguage('zh');
    const picker = capturePicker();
    const pending = pickJSONFile(4);

    // Valid JSON whose compact body is over the limit: the size check that
    // counts the body is the one that refuses it.
    choose(picker(), new File(['{"a":"0123456789"}'], 'export.json', { type: 'application/json' }));

    const error = await pending.catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('too_large');
    expect((error as ApiError).message).toBe(t('importFileTooLarge'));
    expect((error as ApiError).message).not.toBe('That file is too large to import.');
  });

  it('refuses a file that is not JSON with a translated message', async () => {
    await changeLanguage('zh');
    const picker = capturePicker();
    const pending = pickJSONFile();

    choose(picker(), new File(['not json at all'], 'export.json', { type: 'application/json' }));

    const error = await pending.catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('malformed');
    expect((error as ApiError).message).toBe(t('importFileNotJSON'));
    expect((error as ApiError).message).not.toBe('That file is not valid JSON.');
  });

  it('parses a JSON file', async () => {
    const picker = capturePicker();
    const pending = pickJSONFile();

    choose(picker(), new File(['{"obsidian_arc_export":1}'], 'export.json', { type: 'application/json' }));

    await expect(pending).resolves.toEqual({ obsidian_arc_export: 1 });
  });

  it('measures an indented file by the compact body that is sent, not by its indented size', async () => {
    // 21 bytes saved with indentation, 16 once sent compact: the server counts
    // the compact body, so this is within a limit of 18.
    const picker = capturePicker();
    const pending = pickJSONFile(18);

    choose(picker(), new File(['{\n  "a": "bcdefghi"\n}'], 'export.json', { type: 'application/json' }));

    await expect(pending).resolves.toEqual({ a: 'bcdefghi' });
  });

  it('accepts an indented file more than twice the size of the body it sends', async () => {
    // 60 bytes on disk, 9 once sent compact. The server counts only the body,
    // and an old indented export can be far larger on disk than that.
    const picker = capturePicker();
    const pending = pickJSONFile(18);

    choose(picker(), new File([`{${' '.repeat(40)}"a": "x"${' '.repeat(10)}}`], 'export.json', { type: 'application/json' }));

    await expect(pending).resolves.toEqual({ a: 'x' });
  });

  it('refuses a document whose compact body is over the limit, however it is indented', async () => {
    const picker = capturePicker();
    const pending = pickJSONFile(18);

    choose(picker(), new File(['{ "a" : "bcdefghijklmnopq" }'], 'export.json', { type: 'application/json' }));

    const error = await pending.catch((reason: unknown) => reason);
    expect((error as ApiError).code).toBe('too_large');
  });

  it('refuses a file over eight times the limit as too large, before it is read', async () => {
    // 15 bytes against a cap of 8: the file is not JSON, so only the size check
    // can answer too_large. Without it this file would be refused as malformed.
    const picker = capturePicker();
    const pending = pickJSONFile(1);

    choose(picker(), new File(['not json at all'], 'export.json', { type: 'application/json' }));

    const error = await pending.catch((reason: unknown) => reason);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('too_large');
  });
});
