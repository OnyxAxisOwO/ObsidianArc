// Filing a conversation is one request, and the rail's tests stand in for it
// with a mock. This is the one place the request itself is looked at: the
// method, the path and the body the server reads, including the empty
// project that takes a conversation out of every folder.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { moveConversation } from '@/api/chat';

afterEach(() => {
  vi.unstubAllGlobals();
});

function answer(projectID: string): Response {
  return new Response(JSON.stringify({ conversation: { id: 'c1', project_id: projectID } }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('moveConversation', () => {
  for (const projectID of ['p2', '']) {
    it(`sends PATCH /api/conversations/{id} with project_id ${JSON.stringify(projectID)}`, async () => {
      const fetchSpy = vi.fn(async () => answer(projectID));
      vi.stubGlobal('fetch', fetchSpy);

      const result = await moveConversation('c1', projectID);

      expect(fetchSpy).toHaveBeenCalledTimes(1);
      const [url, init] = fetchSpy.mock.calls[0] as unknown as [string, RequestInit];
      expect(url).toBe('/api/conversations/c1');
      expect(init.method).toBe('PATCH');
      expect(JSON.parse(String(init.body))).toEqual({ project_id: projectID });
      expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
      expect(result.conversation.project_id).toBe(projectID);
    });
  }
});
