// Which project the composer is pointed into.
//
// Held here rather than passed down because the rail, the composer, the
// greeting and the transcript all need to agree about it, and passing it
// through four component layers is how two of them end up disagreeing.
//
// Only the *pending* choice lives here — the project the next new
// conversation will open in. Once one exists it carries its own project,
// read back from the server with the thread.

import { ref, type Ref } from 'vue';

const projectID = ref('');

/** The project a new conversation would be opened in, empty for none. */
export const pendingProjectID: Ref<string> = projectID;

export function setProject(id: string): void {
  projectID.value = id;
}
