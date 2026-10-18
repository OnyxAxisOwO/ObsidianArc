// The probe reads its stream through the shared SSE reader. What arrives over
// a real connection is not one frame per chunk: frames split anywhere, proxies
// rewrite line endings, and a payload may span several data lines.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { adminApi, type ProbeProgress } from '../src/admin/api';

const encoder = new TextEncoder();

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function chunked(chunks: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

const done: ProbeProgress = { completed: 2, total: 2, succeeded: 1, failed: 1, skipped: 0 };

describe('the model probe frames', () => {
  it('reassembles frames split across chunks, CRLF-separated, with multi-line data', async () => {
    const stream =
      'event: progress\r\ndata: {"completed":1,"total":2,\r\ndata: "succeeded":1,"failed":0,"skipped":0}\r\n\r\n' +
      `event: done\r\ndata: ${JSON.stringify(done)}\r\n\r\n`;
    // Cut every seven characters, so boundaries land inside field names,
    // inside the JSON and between the two halves of a CRLF.
    const chunks: string[] = [];
    for (let i = 0; i < stream.length; i += 7) chunks.push(stream.slice(i, i + 7));
    vi.stubGlobal('fetch', vi.fn(async () => chunked(chunks)));

    const seen: number[] = [];
    const result = await adminApi.probeAllModels((next) => seen.push(next.completed));

    expect(seen).toEqual([1]);
    expect(result).toEqual(done);
  });

  it('fails the probe on an error frame and cancels the body', async () => {
    const cancel = vi.fn();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('event: error\ndata: {}\n\n'));
      },
      cancel,
    });
    vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status: 200 })));

    await expect(adminApi.probeAllModels(() => {})).rejects.toMatchObject({ status: 500, code: 'probe' });
    expect(cancel).toHaveBeenCalled();
  });

  it('drops an unreadable frame, says so, and still finishes', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.stubGlobal('fetch', vi.fn(async () => chunked([
      'event: progress\ndata: {not json\n\n',
      `event: done\ndata: ${JSON.stringify(done)}\n\n`,
    ])));

    await expect(adminApi.probeAllModels(() => {})).resolves.toEqual(done);
    expect(warn).toHaveBeenCalledTimes(1);
  });

  it('rejects a stream that ends without a done frame', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => chunked(['event: progress\ndata: {"completed":1}\n\n'])));
    await expect(adminApi.probeAllModels(() => {})).rejects.toMatchObject({ status: 0, code: 'stream' });
  });
});
