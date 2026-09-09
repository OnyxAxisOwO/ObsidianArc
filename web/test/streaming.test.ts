import { describe, expect, it, beforeEach } from 'vitest';
import { watch } from 'vue';

import { bufferDelta, flushDeltas, settleDeltas, pending } from '../src/chat/useChat';

// A model streams tokens tens of times a second. Applying each one on arrival
// replaced the pending message, which re-parses the whole answer into DOM and
// reads the scroll geometry back — work proportional to the answer so far,
// done once per token, so a long answer grew slower the longer it got. What
// keeps that bounded is that deltas are applied once per animation frame, and
// nothing else in the transcript enforces it.

function frame(): Promise<void> {
  return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

function fresh() {
  pending.value = { answer: '', reasoning: '', startedAt: 0 };
}

/** Counts writes to `pending`, which is what a repaint of the answer costs. */
function countWrites(): { writes: () => number; stop: () => void } {
  let writes = 0;
  const stop = watch(pending, () => { writes += 1; }, { flush: 'sync' });
  return { writes: () => writes, stop };
}

describe('streaming deltas', () => {
  beforeEach(() => {
    // Drain anything a previous test left buffered, then start clean.
    settleDeltas();
    fresh();
  });

  it('applies two hundred deltas as a single write', async () => {
    const counter = countWrites();

    for (let i = 0; i < 200; i++) bufferDelta('x');
    // Nothing is applied before the frame runs.
    expect(counter.writes()).toBe(0);
    expect(pending.value?.answer).toBe('');

    await frame();

    // The assertion the coalescing exists for: 200 deltas, one repaint.
    expect(counter.writes()).toBe(1);
    expect(pending.value?.answer).toBe('x'.repeat(200));
    counter.stop();
  });

  it('writes once per frame across several frames, losing nothing', async () => {
    const counter = countWrites();

    for (let i = 0; i < 50; i++) bufferDelta('a');
    await frame();
    for (let i = 0; i < 50; i++) bufferDelta('b');
    await frame();

    expect(counter.writes()).toBe(2);
    expect(pending.value?.answer).toBe('a'.repeat(50) + 'b'.repeat(50));
    counter.stop();
  });

  it('keeps reasoning and answer apart', async () => {
    bufferDelta('answer');
    bufferDelta('', 'thinking');
    await frame();

    expect(pending.value?.answer).toBe('answer');
    expect(pending.value?.reasoning).toBe('thinking');
  });

  it('applies what is still buffered when a turn settles before the frame runs', () => {
    bufferDelta('tail');
    settleDeltas();

    expect(pending.value?.answer).toBe('tail');
  });

  // The buffers are module state shared by every turn, so the drain at the end
  // of one is the only thing keeping its tail out of the next.
  it('does not carry one turn\'s tail into the next', async () => {
    bufferDelta('from the first turn');
    settleDeltas();

    fresh();
    bufferDelta('second');
    await frame();

    expect(pending.value?.answer).toBe('second');
  });

  // A background tab runs no animation frames at all, so a turn read there
  // would buffer a whole answer if the end of the turn did not drain it.
  it('drains a buffer no frame ever ran for', () => {
    bufferDelta('everything, with no frame');
    settleDeltas();
    expect(pending.value?.answer).toBe('everything, with no frame');

    fresh();
    // And the settle left nothing behind to arrive late.
    flushDeltas();
    expect(pending.value?.answer).toBe('');
  });
});
