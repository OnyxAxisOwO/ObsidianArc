// The one SSE reader the chat, the terminal and the front door's sample
// conversation share.

import { describe, expect, it, vi } from 'vitest';
import { parseSSEFrame, readSSE, type SSEEvent } from '@/api/sse';

function body(chunks: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

describe('parseSSEFrame', () => {
  it('joins a multi-line data field with newlines, as the format says', () => {
    expect(parseSSEFrame('event: delta\ndata: {"text":\ndata: "a"}')).toEqual({ event: 'delta', data: '{"text":\n"a"}' });
  });

  it('takes one space after the colon as the separator and keeps the rest', () => {
    expect(parseSSEFrame('data:  two')?.data).toBe(' two');
    expect(parseSSEFrame('data:none')?.data).toBe('none');
  });

  it('gives a frame without data nothing to dispatch, and one without an event the default', () => {
    expect(parseSSEFrame(': keepalive')).toBeNull();
    expect(parseSSEFrame('data: {}', 'message')).toEqual({ event: 'message', data: '{}' });
  });
});

describe('readSSE', () => {
  it('reassembles frames split across chunks, CRLF included, and drops one left open', async () => {
    const seen: SSEEvent[] = [];
    await readSSE(
      body(['event: a\r\ndata: 1\r', '\n\r\nevent: b\ndata: 2\n', '\nevent: c\ndata: 3']),
      (event) => seen.push(event),
    );
    expect(seen).toEqual([{ event: 'a', data: '1' }, { event: 'b', data: '2' }]);
  });

  it('cancels the body when a handler throws, so the server stops producing into it', async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('data: 1\n\n'));
      },
      cancel,
    });
    await expect(readSSE(stream, () => {
      throw new Error('handler failed');
    })).rejects.toThrow('handler failed');
    expect(cancel).toHaveBeenCalled();
  });
});
