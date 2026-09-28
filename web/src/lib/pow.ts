import type { PoWChallenge, PoWSolution } from '@/api/auth';

// Pure synchronous SHA-256 implementation suitable for running inside Web Workers
// or in-thread fallback without external dependencies.
const SHA256_SRC = `
function sha256(ascii) {
  function rightRotate(value, amount) {
    return (value >>> amount) | (value << (32 - amount));
  }
  var i, j;
  var result = '';
  var words = [];
  var asciiBitLength = ascii.length * 8;
  
  var hash = [
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
    0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19
  ];

  var k = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ];

  words[asciiBitLength >> 5] |= 0x80 << (24 - (asciiBitLength % 32));
  words[(((asciiBitLength + 64) >> 9) << 4) + 15] = asciiBitLength;

  for (i = 0; i < ascii.length; i++) {
    words[i >> 2] |= ascii.charCodeAt(i) << (24 - (i % 4) * 8);
  }

  var w = new Array(64);
  for (i = 0; i < words.length; i += 16) {
    var a = hash[0], b = hash[1], c = hash[2], d = hash[3], e = hash[4], f = hash[5], g = hash[6], h = hash[7];

    for (j = 0; j < 64; j++) {
      if (j < 16) {
        w[j] = words[i + j] | 0;
      } else {
        var s0 = rightRotate(w[j - 15], 7) ^ rightRotate(w[j - 15], 18) ^ (w[j - 15] >>> 3);
        var s1 = rightRotate(w[j - 2], 17) ^ rightRotate(w[j - 2], 19) ^ (w[j - 2] >>> 10);
        w[j] = (w[j - 16] + s0 + w[j - 7] + s1) | 0;
      }

      var temp1 = (h + (rightRotate(e, 6) ^ rightRotate(e, 11) ^ rightRotate(e, 25)) + ((e & f) ^ (~e & g)) + k[j] + w[j]) | 0;
      var temp2 = ((rightRotate(a, 2) ^ rightRotate(a, 13) ^ rightRotate(a, 22)) + ((a & b) ^ (a & c) ^ (b & c))) | 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) | 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) | 0;
    }

    hash[0] = (hash[0] + a) | 0;
    hash[1] = (hash[1] + b) | 0;
    hash[2] = (hash[2] + c) | 0;
    hash[3] = (hash[3] + d) | 0;
    hash[4] = (hash[4] + e) | 0;
    hash[5] = (hash[5] + f) | 0;
    hash[6] = (hash[6] + g) | 0;
    hash[7] = (hash[7] + h) | 0;
  }

  for (i = 0; i < 8; i++) {
    for (j = 3; j >= 0; j--) {
      var byte = (hash[i] >> (j * 8)) & 255;
      result += (byte < 16 ? '0' : '') + byte.toString(16);
    }
  }
  return result;
}
`;

function solveDirectly(challenge: PoWChallenge): number {
  // Evaluated only where Web Workers are unavailable (e.g. testing environments).
  const fn = new Function('challenge', 'salt', 'maxNumber', `${SHA256_SRC}
    for (var n = 0; n <= maxNumber; n++) {
      if (sha256(salt + n) === challenge) return n;
    }
    return -1;
  `);
  return fn(challenge.challenge, challenge.salt, challenge.maxNumber) as number;
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

  const cancel = () => {
    cancelled = true;
    if (worker) {
      worker.terminate();
      worker = null;
    }
    if (workerUrl) {
      URL.revokeObjectURL(workerUrl);
      workerUrl = null;
    }
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
        ${SHA256_SRC}
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
    } catch {
      // In case Worker or Blob fails (e.g. strict CSP in unexpected browser), fall back to main thread
      cleanup();
      const nonce = solveDirectly(challenge);
      if (nonce < 0) {
        reject(new Error('PoW challenge exhausted without solution'));
      } else {
        resolve({ ...challenge, nonce });
      }
    }

    function cleanup() {
      if (worker) {
        worker.terminate();
        worker = null;
      }
      if (workerUrl) {
        URL.revokeObjectURL(workerUrl);
        workerUrl = null;
      }
    }
  });

  return { promise, cancel };
}
