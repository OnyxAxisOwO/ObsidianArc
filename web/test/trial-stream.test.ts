// The done frame is the only place the server says how many trial turns it
// counted. A stream that ends without one must not read as an exhausted trial,
// and a stream whose line endings were rewritten by a proxy must still parse.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@/api/client';
import { streamTrial, type TrialMessage } from '@/api/trial';

const messages: TrialMessage[] = [{ role: 'user', content: 'hi' }];

function streamOf(...chunks: string[]): Response {
  const encoder = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

function answerWith(response: Response): void {
  vi.stubGlobal('fetch', vi.fn(async () => response));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('trial stream', () => {
  it('resolves with the counted turns from the done frame', async () => {
    answerWith(streamOf(
      'event: delta\ndata: {"text":"Hel"}\n\n',
      'event: delta\ndata: {"text":"lo"}\n\n',
      'event: done\ndata: {"continuation":"c2","turns_left":2}\n\n',
    ));
    const deltas: string[] = [];

    const result = await streamTrial(messages, 'c1', (text) => deltas.push(text));

    expect(deltas.join('')).toBe('Hello');
    expect(result).toEqual({ continuation: 'c2', turnsLeft: 2 });
  });

  it('reads frames whose line endings were rewritten to CRLF', async () => {
    answerWith(streamOf(
      'event: delta\r\ndata: {"text":"Hi"}\r\n\r\nevent: done\r\ndata: {"continuation":"c2","turns_left":1}\r\n\r\n',
    ));
    const deltas: string[] = [];

    const result = await streamTrial(messages, 'c1', (text) => deltas.push(text));

    expect(deltas).toEqual(['Hi']);
    expect(result).toEqual({ continuation: 'c2', turnsLeft: 1 });
  });

  it('rejects a stream that ends after deltas without a done frame', async () => {
    answerWith(streamOf('event: delta\ndata: {"text":"partial"}\n\n'));

    const pending = streamTrial(messages, 'c1', () => undefined);

    await expect(pending).rejects.toBeInstanceOf(ApiError);
    await expect(pending).rejects.toMatchObject({ code: 'stream' });
  });

  it('does not count a done frame the body never terminated', async () => {
    answerWith(streamOf('event: done\ndata: {"continuation":"c2","turns_left":2}\n'));

    await expect(streamTrial(messages, 'c1', () => undefined)).rejects.toMatchObject({ code: 'stream' });
  });
});

describe('trial stream when it is cut short', () => {
  it('cancels the body when an error frame ends the answer early', async () => {
    const cancelled = vi.fn();
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('event: error\ndata: {"code":"upstream","message":"gone"}\n\n'));
      },
      cancel: cancelled,
    });
    answerWith(new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }));

    await expect(streamTrial(messages, 'c1', () => undefined)).rejects.toMatchObject({ code: 'upstream' });
    expect(cancelled).toHaveBeenCalledOnce();
  });

  it('hands the caller\'s signal to the request, so leaving the page can end it', async () => {
    const fetchSpy = vi.fn(async (_url: string, _init?: RequestInit) => streamOf('event: done\ndata: {"continuation":"c2","turns_left":1}\n\n'));
    vi.stubGlobal('fetch', fetchSpy);
    const controller = new AbortController();

    await streamTrial(messages, 'c1', () => undefined, controller.signal);

    expect(fetchSpy.mock.calls[0]![1]).toMatchObject({ signal: controller.signal });
  });
});
