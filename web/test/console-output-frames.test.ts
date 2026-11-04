// A terminal output frame without a string in it is dropped, rather than
// written to the screen as the literal "undefined".

import { afterEach, describe, expect, it, vi } from 'vitest';
import { execConsoleLine } from '@/api/console';

const encoder = new TextEncoder();

afterEach(() => {
  vi.unstubAllGlobals();
});

function answerWith(body: string): void {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(body));
      controller.close();
    },
  });
  vi.stubGlobal('fetch', vi.fn(async () => new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  })));
}

describe('console output frames', () => {
  it('passes on only the output frames whose text is a string', async () => {
    answerWith(
      'event: out\ndata: {"text":5}\n\n'
      + 'event: out\ndata: {}\n\n'
      + 'event: out\ndata: {"text":"hello"}\n\n'
      + 'event: done\ndata: {"exit_code":0}\n\n',
    );
    const lines: string[] = [];

    await execConsoleLine(
      { line: 'help', cols: 80, json: false },
      { onOut: (text) => lines.push(text) },
      new AbortController().signal,
    );

    expect(lines).toEqual(['hello']);
  });
});
