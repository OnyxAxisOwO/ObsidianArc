// The model probe is one long stream. A frame may carry its payload over several
// data lines, which the SSE spec joins with newlines, and stopping the probe has
// to reach the network: aborting the request must end the read with the abort
// rather than leave the stream waiting.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { adminApi } from '../src/admin/api';

const encoder = new TextEncoder();

afterEach(() => {
  vi.unstubAllGlobals();
});

function sseResponse(frames: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const frame of frames) controller.enqueue(encoder.encode(frame));
      controller.close();
    },
  });
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

describe('the model probe stream', () => {
  it('reads a frame whose data spans several lines as one payload', async () => {
    const progress: number[] = [];
    vi.stubGlobal('fetch', vi.fn(async () => sseResponse([
      'event: progress\ndata: {"completed":1,"total":2,"succeeded":1,"failed":0,"skipped":0}\n\n',
      'event: done\ndata: {"completed":2,\ndata: "total":2,"succeeded":2,"failed":0,"skipped":0}\n\n',
    ])));

    const result = await adminApi.probeAllModels((next) => progress.push(next.completed));

    expect(progress).toEqual([1]);
    expect(result).toEqual({ completed: 2, total: 2, succeeded: 2, failed: 0, skipped: 0 });
  });

  it('ends the read with the abort, and passes the signal to the request', async () => {
    const controller = new AbortController();
    const request = vi.fn((_url: string, init: RequestInit) => {
      const body = new ReadableStream<Uint8Array>({
        start(streamController) {
          // What the browser does to a body read when the request is aborted.
          init.signal?.addEventListener('abort', () => {
            streamController.error(new DOMException('The operation was aborted.', 'AbortError'));
          });
        },
      });
      return Promise.resolve(new Response(body, { status: 200 }));
    });
    vi.stubGlobal('fetch', request);

    const running = adminApi.probeAllModels(() => {}, { signal: controller.signal });
    await new Promise((resolve) => setTimeout(resolve, 0));
    controller.abort();

    await expect(running).rejects.toMatchObject({ name: 'AbortError' });
    expect(request.mock.calls[0]![1].signal).toBe(controller.signal);
  });
});
