// A project: a name and standing instructions a conversation started inside
// it inherits. Every route is scoped to the caller on the server, so there is
// no lookup-by-id-for-other-users shape here to accidentally expose.

import { api } from './client';

export interface Project {
  id: string;
  name: string;
  instructions: string;
  /** How many conversations sit in it, for the rail. */
  conversations: number;
  created_at: number;
  updated_at: number;
}

export interface ProjectList {
  projects: Project[];
  /** The account-wide cap, so the rail can grey out "new project" without a second request. */
  max: number;
}

export function listProjects(): Promise<ProjectList> {
  return api.get<ProjectList>('/api/projects');
}

export function createProject(name: string, instructions = ''): Promise<Project> {
  return api.post<Project>('/api/projects', { name, instructions });
}

export interface ProjectPatch {
  name?: string;
  instructions?: string;
}

export function updateProject(id: string, patch: ProjectPatch): Promise<Project> {
  return api.patch<Project>(`/api/projects/${id}`, patch);
}

export function deleteProject(id: string): Promise<void> {
  return api.delete<void>(`/api/projects/${id}`);
}
