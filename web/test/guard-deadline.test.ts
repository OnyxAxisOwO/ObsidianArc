// A plugin guard that never answers must not hold a submit button for good, and
// a guard that refuses must not be mistaken for one that timed out.

import { describe, expect, it, vi } from 'vitest';
import { GuardTimeout, withDeadline } from '@/lib/deadline';

describe('the deadline on a plugin guard\'s answer', () => {
  it('passes an answer through when it comes in time', async () => {
    await expect(withDeadline(Promise.resolve('token'), 1000)).resolves.toBe('token');
  });

  it('gives up on a check that never answers', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const pending = withDeadline(new Promise<string>(() => {}), 500);
      vi.advanceTimersByTime(500);
      await expect(pending).rejects.toBeInstanceOf(GuardTimeout);
    } finally {
      vi.useRealTimers();
    }
  });

  it('passes a refusal through rather than calling it a timeout', async () => {
    await expect(withDeadline(Promise.reject(new Error('refused')), 1000)).rejects.toThrow('refused');
  });
});
