// The three selectors read one document. A page that asks for two of them in
// the same moment should send one request, and a call made after the answer has
// come back should read again, since a save in between may have changed it.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { adminApi } from '../src/admin/api';
import { api } from '../src/api/client';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the reference selectors', () => {
  it('answers two selectors asked in the same moment with one request', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      groups: [{ id: 'g1', name: 'Pro' }],
      providers: [{ id: 'p1', name: 'Prov', kind: 'openai', enabled: true }],
    });

    const [groups, providers] = await Promise.all([adminApi.groupOptions(), adminApi.providerOptions()]);

    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith('/api/admin/references');
    expect(groups.groups[0]!.id).toBe('g1');
    expect(providers.providers[0]!.id).toBe('p1');
  });

  it('reads again when asked after the earlier answer has come back', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ groups: [] });

    await adminApi.groupOptions();
    await adminApi.groupOptions();

    expect(get).toHaveBeenCalledTimes(2);
  });

  it('gives both callers the same failure when the one shared request fails', async () => {
    const failure = new Error('network down');
    vi.spyOn(api, 'get').mockRejectedValue(failure);

    const results = await Promise.allSettled([adminApi.groupOptions(), adminApi.modelOptions()]);

    expect(results.map((result) => result.status)).toEqual(['rejected', 'rejected']);
    expect(results.map((result) => (result as PromiseRejectedResult).reason)).toEqual([failure, failure]);
  });
});
