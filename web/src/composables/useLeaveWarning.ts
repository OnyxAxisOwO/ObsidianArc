import { onBeforeUnmount, onMounted } from 'vue';

/**
 * Asks the browser to confirm before the tab is closed or reloaded while `warn`
 * says the reader would lose something. Leaving the page inside the app is not
 * covered: that does not reload the tab, so the browser's prompt does not apply,
 * and a screen that must survive it keeps what it needs in memory instead.
 */
export function useLeaveWarning(warn: () => boolean): void {
  function onBeforeUnload(event: BeforeUnloadEvent): void {
    if (!warn()) return;
    event.preventDefault();
    event.returnValue = '';
  }
  onMounted(() => window.addEventListener('beforeunload', onBeforeUnload));
  onBeforeUnmount(() => window.removeEventListener('beforeunload', onBeforeUnload));
}
