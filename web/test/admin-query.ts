// The query strings a mocked admin read was called with, one per call, as the
// server would receive them. The reads take an object or URLSearchParams, so a
// test that compares what was asked for compares this spelling of it rather
// than whichever form the page happened to build.

import { adminQueryParams, type AdminQuery } from '../src/admin/api';

export function queriesSent(mock: { mock: { calls: ArrayLike<unknown>[] } }, position = 0): string[] {
  return mock.mock.calls.map((call) => adminQueryParams(call[position] as AdminQuery | undefined).toString());
}
