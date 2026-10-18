import type { PoWChallenge, PoWSolution } from '@/api/auth';

// SHA-256 written out rather than taken from crypto.subtle, which is
// asynchronous: a search over thousands of nonces would await each one. It is
// a real function rather than source text, so the jsdom fallback below calls
// it without eval and the Worker is handed the same function by its source —
// one copy in the bundle, not a function and a string of it. That is also
// why it must not reach anything outside its own body: in the Worker there
// is nothing else.
function sha256(ascii: string): string {
  const rotr = (value: number, amount: number): number => (value >>> amount) | (value << (32 - amount));
  // Every read below is in bounds or a hole the message padding leaves; `?? 0`
  // is what JavaScript's `undefined | 0` made of a hole, said so tsc sees it.
  const at = (list: number[], index: number): number => list[index] ?? 0;

  const hash = [
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ];
  const k = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];

  const bits = ascii.length * 8;
  const words: number[] = [];
  words[bits >> 5] = at(words, bits >> 5) | (0x80 << (24 - (bits % 32)));
  words[(((bits + 64) >> 9) << 4) + 15] = bits;
  for (let i = 0; i < ascii.length; i++) {
    words[i >> 2] = at(words, i >> 2) | (ascii.charCodeAt(i) << (24 - (i % 4) * 8));
  }

  const w: number[] = [];
  for (let i = 0; i < words.length; i += 16) {
    let a = at(hash, 0), b = at(hash, 1), c = at(hash, 2), d = at(hash, 3);
    let e = at(hash, 4), f = at(hash, 5), g = at(hash, 6), h = at(hash, 7);

    for (let j = 0; j < 64; j++) {
      if (j < 16) {
        w[j] = at(words, i + j) | 0;
      } else {
        const w15 = at(w, j - 15);
        const w2 = at(w, j - 2);
        const s0 = rotr(w15, 7) ^ rotr(w15, 18) ^ (w15 >>> 3);
        const s1 = rotr(w2, 17) ^ rotr(w2, 19) ^ (w2 >>> 10);
        w[j] = (at(w, j - 16) + s0 + at(w, j - 7) + s1) | 0;
      }

      const temp1 = (h + (rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)) + ((e & f) ^ (~e & g)) + at(k, j) + at(w, j)) | 0;
      const temp2 = ((rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) | 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) | 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) | 0;
    }

    const round = [a, b, c, d, e, f, g, h];
    for (let n = 0; n < 8; n++) hash[n] = (at(hash, n) + at(round, n)) | 0;
  }

  let result = '';
  for (const word of hash) {
    for (let j = 3; j >= 0; j--) {
      const byte = (word >> (j * 8)) & 255;
      result += (byte < 16 ? '0' : '') + byte.toString(16);
    }
  }
  return result;
}

// Used only where Web Workers and Blobs do not exist at all (the jsdom tests).
// It is deliberately not a fallback for a Worker that fails to start: a search
// on the page's own thread would freeze the very screen it runs for.
function solveDirectly(challenge: PoWChallenge): number {
  for (let n = 0; n <= challenge.maxNumber; n++) {
    if (sha256(challenge.salt + n) === challenge.challenge) return n;
  }
  return -1;
}

export interface PoWTask {
  promise: Promise<PoWSolution>;
  cancel: () => void;
}

/**
 * Searches for a nonce satisfying SHA256(salt + nonce) == challenge.
 *
 * Runs inside a Web Worker so long computations never hitch the UI thread or
 * animations on the registration screen.
 */
export function solvePoW(challenge: PoWChallenge): PoWTask {
  let cancelled = false;
  let worker: Worker | null = null;
  let workerUrl: string | null = null;

  // Shared by the answer, the error, the failed start and cancel, so every way
  // out releases the worker and its blob URL the same way.
  const cleanup = () => {
    if (worker) {
      worker.terminate();
      worker = null;
    }
    if (workerUrl) {
      URL.revokeObjectURL(workerUrl);
      workerUrl = null;
    }
  };

  const cancel = () => {
    cancelled = true;
    cleanup();
  };

  const promise = new Promise<PoWSolution>((resolve, reject) => {
    if (typeof Worker === 'undefined' || typeof Blob === 'undefined') {
      const nonce = solveDirectly(challenge);
      if (nonce < 0) {
        reject(new Error('PoW challenge exhausted without solution'));
      } else {
        resolve({ ...challenge, nonce });
      }
      return;
    }

    try {
      const code = `
        var sha256 = ${sha256.toString()};
        self.onmessage = function(e) {
          var c = e.data.challenge;
          var s = e.data.salt;
          var max = e.data.maxNumber;
          for (var n = 0; n <= max; n++) {
            if (sha256(s + n) === c) {
              self.postMessage({ ok: true, nonce: n });
              return;
            }
          }
          self.postMessage({ ok: false, error: 'exhausted' });
        };
      `;

      const blob = new Blob([code], { type: 'application/javascript' });
      workerUrl = URL.createObjectURL(blob);
      worker = new Worker(workerUrl);

      worker.onmessage = (event: MessageEvent<{ ok: boolean; nonce?: number; error?: string }>) => {
        cleanup();
        if (cancelled) return;
        if (event.data.ok && typeof event.data.nonce === 'number') {
          resolve({ ...challenge, nonce: event.data.nonce });
        } else {
          reject(new Error(event.data.error || 'PoW solve failed'));
        }
      };

      worker.onerror = (err) => {
        cleanup();
        if (cancelled) return;
        reject(new Error(err.message || 'PoW worker error'));
      };

      worker.postMessage({
        challenge: challenge.challenge,
        salt: challenge.salt,
        maxNumber: challenge.maxNumber,
      });
    } catch (failure) {
      // A Worker that will not start (a CSP that blocks it, say) is not solved
      // on the page's own thread, for the reason solveDirectly gives, so the
      // failure is reported as the solver's own error.
      cleanup();
      reject(new Error('PoW worker could not start', { cause: failure }));
    }
  });

  return { promise, cancel };
}
