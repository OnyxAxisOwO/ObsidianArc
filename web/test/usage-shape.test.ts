// The widest audience on a usage board is the largest figure among its rows.
// It is taken with a reduce: a spread of every row into one call overflows the
// stack on a large instance, and the usage page then fails to draw at all.

import { describe, expect, it } from 'vitest';
import { largestUsers } from '../src/views/admin/usage/shape';

describe('the widest audience on a usage board', () => {
  it('takes the largest figure from a very long list of rows', () => {
    const rows = Array.from({ length: 200_000 }, (_, index) => ({ users: index }));
    expect(() => largestUsers(rows)).not.toThrow();
    expect(largestUsers(rows)).toBe(199_999);
  });

  it('is zero for no rows, and a row without a count adds nothing', () => {
    expect(largestUsers([])).toBe(0);
    // A row the server sent without a count is typed as having one; the helper still reads it as zero.
    expect(largestUsers([{ users: undefined as unknown as number }, { users: 3 }])).toBe(3);
  });
});
