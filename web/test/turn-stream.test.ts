// A turn whose body breaks while it is being read must end the request, not
// leave the connection open, and a frame the parser cannot use must not take
// the whole turn down with it.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { sendTurn, type TurnHandlers } from '@/api/chat';

const request = { model_id: 'm', content: 'hi' };
const encoder = new TextEncoder();

function responseWith(source: UnderlyingDefaultSource<Uint8Array>): Response {
  return new Response(new ReadableStream<Uint8Array>(source), {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

function answerWith(source: UnderlyingDefaultSource<Uint8Array>): void {
  vi.stubGlobal('fetch', vi.fn(async () => responseWith(source)));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('turn stream', () => {
  it('cancels the body when a handler throws, so the server stops writing into it', async () => {
    let cancelled = false;
    answerWith({
      start(controller) {
        controller.enqueue(encoder.encode(
          'event: delta\ndata: {"text":"a"}\n\nevent: delta\ndata: {"text":"b"}\n\n',
        ));
      },
      cancel() {
        cancelled = true;
      },
    });
    const handlers: TurnHandlers = {
      onDelta: () => {
        throw new Error('handler broke');
      },
    };

    await expect(sendTurn(request, handlers, new AbortController().signal)).rejects.toThrow('handler broke');
    expect(cancelled).toBe(true);
  });

  it('rejects when the body breaks partway through, after the frames before the break', async () => {
    let step = 0;
    answerWith({
      pull(controller) {
        if (step++ === 0) controller.enqueue(encoder.encode('event: delta\ndata: {"text":"partial"}\n\n'));
        else controller.error(new TypeError('network error'));
      },
    });
    const deltas: string[] = [];

    await expect(
      sendTurn(request, { onDelta: (text) => deltas.push(text) }, new AbortController().signal),
    ).rejects.toThrow('network error');
    expect(deltas).toEqual(['partial']);
  });

  it('drops a delta whose text is not a string instead of writing "undefined" into the answer', async () => {
    answerWith({
      start(controller) {
        controller.enqueue(encoder.encode(
          'event: delta\ndata: {}\n\nevent: delta\ndata: {"text":"ok"}\n\nevent: done\ndata: {"message_id":"m1","stopped":false,"streamed":true}\n\n',
        ));
        controller.close();
      },
    });
    const deltas: string[] = [];

    await sendTurn(request, { onDelta: (text) => deltas.push(text) }, new AbortController().signal);

    expect(deltas).toEqual(['ok']);
  });

  it('skips a frame whose JSON is null and still reads the done frame after it', async () => {
    answerWith({
      start(controller) {
        controller.enqueue(encoder.encode(
          'event: delta\ndata: null\n\nevent: done\ndata: {"message_id":"m1","stopped":false,"streamed":true}\n\n',
        ));
        controller.close();
      },
    });
    const onDelta = vi.fn();
    const onDone = vi.fn();

    await sendTurn(request, { onDelta, onDone }, new AbortController().signal);

    expect(onDelta).not.toHaveBeenCalled();
    expect(onDone).toHaveBeenCalledWith({ message_id: 'm1', stopped: false, streamed: true });
  });
});
