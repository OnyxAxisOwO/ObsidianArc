import { describe, expect, it } from 'vitest';
import { solvePoW } from '../src/lib/pow';
import type { PoWChallenge } from '../src/api/auth';
import { createHash } from 'node:crypto';

describe('PoW solver', () => {
  it('finds the correct nonce for a known challenge', async () => {
    const salt = 'a1b2c3d4e5f60718293a4b5c6d7e8f90';
    const targetNonce = 42;
    const challengeHex = createHash('sha256').update(salt + targetNonce).digest('hex');

    const challenge: PoWChallenge = {
      challenge: challengeHex,
      salt,
      maxNumber: 100,
      expires: Date.now() + 300000,
      signature: 'dummy-sig',
    };

    const task = solvePoW(challenge);
    const solution = await task.promise;

    expect(solution.nonce).toBe(targetNonce);
    expect(solution.challenge).toBe(challengeHex);
    expect(solution.salt).toBe(salt);
    expect(solution.maxNumber).toBe(100);
    expect(solution.signature).toBe('dummy-sig');
  });

  it('rejects when target nonce exceeds maxNumber', async () => {
    const salt = '11223344556677889900aabbccddeeff';
    const targetNonce = 200;
    const challengeHex = createHash('sha256').update(salt + targetNonce).digest('hex');

    const challenge: PoWChallenge = {
      challenge: challengeHex,
      salt,
      maxNumber: 50,
      expires: Date.now() + 300000,
      signature: 'dummy-sig',
    };

    const task = solvePoW(challenge);
    await expect(task.promise).rejects.toThrow();
  });
});
