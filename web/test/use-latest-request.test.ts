// The backoffice's lists guard their answers with this: only the newest
// request may write what it fetched, and closing a panel retires whatever is
// still in flight for it.

import { describe, expect, it } from 'vitest';
import { useLatestRequest } from '../src/composables/useLatestRequest';

describe('useLatestRequest', () => {
  it('lets only the newest ticket through', () => {
    const latest = useLatestRequest();
    const first = latest.begin();
    expect(latest.isCurrent(first)).toBe(true);
    const second = latest.begin();
    expect(second).not.toBe(first);
    expect(latest.isCurrent(first)).toBe(false);
    expect(latest.isCurrent(second)).toBe(true);
  });

  it('retires the ticket in flight on invalidate without handing out a new one', () => {
    const latest = useLatestRequest();
    const ticket = latest.begin();
    latest.invalidate();
    expect(latest.isCurrent(ticket)).toBe(false);
    // The next request is current again, and still newer than the retired one.
    const next = latest.begin();
    expect(latest.isCurrent(next)).toBe(true);
    expect(latest.isCurrent(ticket)).toBe(false);
  });

  it('reads the newest ticket without retiring it', () => {
    const latest = useLatestRequest();
    const ticket = latest.begin();
    const snapshot = latest.current();
    expect(latest.isCurrent(ticket)).toBe(true);
    expect(latest.isCurrent(snapshot)).toBe(true);
    latest.begin();
    expect(latest.isCurrent(snapshot)).toBe(false);
  });

  it('keeps separate guards apart', () => {
    const pages = useLatestRequest();
    const details = useLatestRequest();
    const page = pages.begin();
    details.begin();
    details.invalidate();
    expect(pages.isCurrent(page)).toBe(true);
  });

  it('settles an out-of-order pair the way a list does', async () => {
    const latest = useLatestRequest();
    let shown = '';
    const load = async (label: string, delay: number): Promise<void> => {
      const ticket = latest.begin();
      await new Promise((resolve) => setTimeout(resolve, delay));
      if (latest.isCurrent(ticket)) shown = label;
    };
    await Promise.all([load('older', 20), load('newer', 0)]);
    expect(shown).toBe('newer');
  });
});
