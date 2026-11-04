// The account's projects, held in one place.
//
// Loaded once and kept in memory, same reasoning as session.ts: the rail and
// the composer both need the list, and a second `/api/projects` call every
// time a panel mounts would just be racing the first one. Plain refs and
// functions, no store library — a handful of values and actions do not need
// Pinia.

import { ref, type Ref } from 'vue';
import {
  createProject as apiCreateProject,
  deleteProject as apiDeleteProject,
  listProjects,
  updateProject as apiUpdateProject,
  type Project,
} from '@/api/projects';
import { within } from '@/lib/deadline';
import { onSignOut } from '@/stores/session';

const projects = ref<Project[]>([]);
const max = ref(0);
const loaded = ref(false);
// Shared by every caller so a rail and a composer mounting in the same tick
// await one request instead of two racing to set `loaded`.
let inFlight: Promise<void> | null = null;
// The list is the one load here whose promise is handed to every later
// caller, so an ask that is accepted and never answered would be the answer
// for the rest of the tab's life: the rail showing no folders, and the
// composer offering none, until a reload. Given up on after this, the next
// mount asks again.
const LIST_DEADLINE_MS = 30_000;
// Bumped on sign-out. An answer to a request made before it belongs to the
// account that signed out, and is dropped rather than shown to the next one.
let generation = 0;
// Every read of the list, and every change made to it here, takes the next
// number; an answer is applied only if nothing newer has been. Two quick moves
// start two refreshes, and the first answering last would put back counts the
// second had already replaced — and a read begun before a folder was made
// would answer without it.
let stamp = 0;
let applied = 0;

/** Takes a number for a change that is about to be written, and marks it written. */
function wrote(): void {
  stamp += 1;
  applied = stamp;
}

function apply(read: number, result: { projects?: Project[] | null; max: number }): void {
  if (read <= applied) return;
  applied = read;
  // The rail reads the list on every chat screen, so an answer without one —
  // a proxy's page, a stubbed fetch — must leave a list, or every screen that
  // counts it throws.
  projects.value = result.projects ?? [];
  max.value = result.max;
  loaded.value = true;
}

export const projectList: Ref<Project[]> = projects;
/** The account-wide cap on how many projects it may hold. Zero until loaded. */
export const projectMax: Ref<number> = max;

/**
 * Fetches the list if it has not been fetched yet. Safe to call from every
 * screen that shows projects; only the first caller after boot (or after a
 * failed attempt) actually reaches the network.
 */
export function loadProjects(): Promise<void> {
  if (loaded.value) return Promise.resolve();
  if (!inFlight) {
    const gen = generation;
    const read = (stamp += 1);
    inFlight = within(listProjects(), LIST_DEADLINE_MS)
      .then((result) => {
        if (gen !== generation) return;
        apply(read, result);
      })
      .finally(() => {
        // Only the request that started this generation may clear it: a
        // newer one may already be in flight for the next account.
        if (gen === generation) inFlight = null;
      });
  }
  return inFlight;
}

/**
 * Reads the list again whether or not it was loaded. Filing a conversation
 * moves two folders' counts at once, and the server's count is the one that
 * also holds the archived conversations this client never lists, so it is
 * asked rather than worked out here.
 */
export async function refreshProjects(): Promise<void> {
  const gen = generation;
  const read = (stamp += 1);
  const result = await within(listProjects(), LIST_DEADLINE_MS);
  if (gen !== generation) return;
  apply(read, result);
}

export async function createProject(name: string, instructions = ''): Promise<Project> {
  const gen = generation;
  const record = await apiCreateProject(name, instructions);
  // Newest first, matching the order the server itself returns on List
  // (most recently changed first).
  if (gen === generation) {
    wrote();
    projects.value = [record, ...projects.value];
  }
  return record;
}

/** Signed out: the next account's projects are fetched afresh, not shown from this one's. */
export function resetProjects(): void {
  generation += 1;
  inFlight = null;
  projects.value = [];
  max.value = 0;
  loaded.value = false;
}

onSignOut(resetProjects);

export async function renameProject(id: string, name: string): Promise<Project> {
  const record = await apiUpdateProject(id, { name });
  replace(record);
  return record;
}

export async function updateProjectInstructions(id: string, instructions: string): Promise<Project> {
  const record = await apiUpdateProject(id, { instructions });
  replace(record);
  return record;
}

export async function deleteProject(id: string): Promise<void> {
  await apiDeleteProject(id);
  wrote();
  projects.value = projects.value.filter((project) => project.id !== id);
}

// The list is only ever touched with the server's own answer in hand, so a
// rejected request leaves it exactly as it was rather than half-applied.
function replace(record: Project): void {
  wrote();
  projects.value = projects.value.map((project) => (project.id === record.id ? record : project));
}
