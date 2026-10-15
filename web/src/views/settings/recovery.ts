// The recovery codes a regeneration has returned and the reader has not yet
// acknowledged. The server keeps only their digests, so these are the one copy
// there is.
//
// They live here rather than in the section that shows them: leaving the
// settings page inside the app unmounts that section, and a remount in the same
// page load must show the codes again rather than lose them. Sign-out clears
// them, so the next account in this tab does not see the last one's codes.
import { ref } from 'vue';
import { onSignOut } from '@/stores/session';

export const pendingRecovery = ref<{ codes: string[]; saved: boolean } | null>(null);

onSignOut(() => { pendingRecovery.value = null; });
