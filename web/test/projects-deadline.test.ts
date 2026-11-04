// The project list hands one promise to every caller until it settles, so a
// request that is accepted and never answered must not become the answer for
// the rest of the tab's life.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/api/projects', () => ({
  createProject: vi.fn(),
  deleteProject: vi.fn(),
  listProjects: vi.fn(),
  updateProject: vi.fn(),
}));

import { listProjects } from '@/api/projects';
import { loadProjects, projectList, resetProjects } from '@/stores/projects';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  resetProjects();
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe('loadProjects', () => {
  it('asks again once a list that never answered has passed its deadline', async () => {
    vi.mocked(listProjects)
      .mockReturnValueOnce(new Promise(() => {}))
      .mockResolvedValueOnce({
        projects: [{ id: 'p1', name: 'Plans', instructions: '', conversations: 0, created_at: 0, updated_at: 0 }],
        max: 5,
      });

    const stalled = loadProjects().catch((failure: unknown) => failure);
    // A second screen mounting while the first ask is out shares it.
    void loadProjects().catch(() => undefined);
    expect(listProjects).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(30_000);
    expect(await stalled).toBeInstanceOf(Error);

    await loadProjects();
    expect(listProjects).toHaveBeenCalledTimes(2);
    expect(projectList.value.map((project) => project.id)).toEqual(['p1']);
  });
});
