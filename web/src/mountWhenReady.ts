import type { App } from 'vue';
import type { Router } from 'vue-router';

/**
 * Mounts the app once the first navigation has settled, whether or not it
 * succeeded.
 *
 * A first navigation that fails (a lazy route whose chunk did not arrive)
 * rejects isReady. Mounting regardless shows the shell, and the console names
 * the failure; letting the rejection through would leave the spinner for good.
 */
export async function mountWhenReady(app: App, router: Router, root: Element): Promise<void> {
  await router.isReady().catch((failure: unknown) => console.error('first navigation failed', failure));
  app.mount(root);
}
