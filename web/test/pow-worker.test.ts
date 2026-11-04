// The Worker half of the solver. jsdom has no Worker, so the test stands one
// in: it records what the page does with it, and answers the way the worker
// script would. The solver's own code path, the answer, the error and the
// release of the worker and its blob URL, is what is under test.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createHash } from 'node:crypto';
import type { PoWChallenge } from '../src/api/auth';
import { solvePoW } from '../src/lib/pow';

type Mode = 'answer' | 'error' | 'unstartable';
let mode: Mode = 'answer';
const workers: FakeWorker[] = [];

class FakeWorker {
  onmessage: ((event: { data: unknown }) => void) | null = null;
  onerror: ((event: { message: string }) => void) | null = null;
  terminate = vi.fn();

  constructor(readonly url: string) {
    if (mode === 'unstartable') throw new DOMException('blocked by policy', 'SecurityError');
    workers.push(this);
  }

  postMessage(data: { challenge: string; salt: string; maxNumber: number }): void {
    if (mode === 'error') {
      queueMicrotask(() => this.onerror?.({ message: 'worker crashed' }));
      return;
    }
    for (let n = 0; n <= data.maxNumber; n++) {
      if (createHash('sha256').update(data.salt + n).digest('hex') === data.challenge) {
        queueMicrotask(() => this.onmessage?.({ data: { ok: true, nonce: n } }));
        return;
      }
    }
    queueMicrotask(() => this.onmessage?.({ data: { ok: false, error: 'exhausted' } }));
  }
}

const createObjectURL = vi.fn(() => 'blob:pow-test');
const revokeObjectURL = vi.fn();

function challengeFor(nonce: number): PoWChallenge {
  const salt = 'deadbeefdeadbeefdeadbeefdeadbeef';
  return {
    challenge: createHash('sha256').update(salt + nonce).digest('hex'),
    salt,
    maxNumber: 100,
    expires: Date.now() + 300000,
    signature: 'sig',
  };
}

beforeEach(() => {
  mode = 'answer';
  workers.length = 0;
  vi.stubGlobal('Worker', FakeWorker);
  Object.assign(URL, { createObjectURL, revokeObjectURL });
  createObjectURL.mockClear();
  revokeObjectURL.mockClear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('PoW solver through a Worker', () => {
  it('takes the nonce from the worker and releases the worker and its URL', async () => {
    const solution = await solvePoW(challengeFor(7)).promise;

    expect(solution.nonce).toBe(7);
    expect(workers).toHaveLength(1);
    expect(workers[0]!.terminate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:pow-test');
  });

  it('rejects with the worker error and releases the worker', async () => {
    mode = 'error';
    await expect(solvePoW(challengeFor(7)).promise).rejects.toThrow('worker crashed');
    expect(workers[0]!.terminate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:pow-test');
  });

  it('rejects with the solver\'s own error when the worker cannot start, rather than evaluating the solver on the page', async () => {
    mode = 'unstartable';
    const failure = await solvePoW(challengeFor(7)).promise.catch((caught: unknown) => caught);

    expect(failure).toBeInstanceOf(Error);
    expect((failure as Error).message).toBe('PoW worker could not start');
    expect(((failure as Error).cause as Error).name).toBe('SecurityError');
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:pow-test');
  });

  // The script the worker would run is built from the solver's own function by
  // its source, so it only works if that function needs nothing from the
  // module around it. Run the script by itself, with nothing but a `self`.
  it('hands the worker a script that solves the challenge on its own', async () => {
    solvePoW(challengeFor(7)).cancel();
    const blob = (createObjectURL.mock.calls[0] as unknown as [Blob])[0];
    const script = await blob.text();

    const posted: unknown[] = [];
    const self: { onmessage?: (event: { data: unknown }) => void; postMessage: (data: unknown) => void } = {
      postMessage: (data) => posted.push(data),
    };
    new Function('self', script)(self);
    const { challenge, salt, maxNumber } = challengeFor(7);
    self.onmessage!({ data: { challenge, salt, maxNumber } });

    expect(posted).toEqual([{ ok: true, nonce: 7 }]);
  });

  it('cancel stops the worker and releases its URL before it answers', () => {
    const task = solvePoW(challengeFor(7));
    expect(workers).toHaveLength(1);

    task.cancel();

    expect(workers[0]!.terminate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:pow-test');
  });
});
