// A lazy route whose chunk did not arrive rejects the router's first
// navigation. The shell must still mount, or the reader stares at the spinner
// with no way to see what failed.

import { afterEach, expect, it, vi } from 'vitest';
import type { App } from 'vue';
import type { Router } from 'vue-router';
import { mountWhenReady } from '../src/mountWhenReady';

afterEach(() => {
  vi.restoreAllMocks();
});

function stubs(isReady: () => Promise<void>) {
  const app = { mount: vi.fn() };
  const router = { isReady: vi.fn(isReady) };
  return {
    app: app as unknown as App & { mount: ReturnType<typeof vi.fn> },
    router: router as unknown as Router,
    root: document.createElement('div'),
  };
}

it('mounts and logs the failure when the first navigation rejects', async () => {
  const error = vi.spyOn(console, 'error').mockImplementation(() => {});
  const failure = new Error('chunk did not arrive');
  const { app, router, root } = stubs(() => Promise.reject(failure));

  await expect(mountWhenReady(app, router, root)).resolves.toBeUndefined();

  expect(app.mount).toHaveBeenCalledWith(root);
  expect(error).toHaveBeenCalledWith('first navigation failed', failure);
});

it('mounts without logging when the first navigation settles', async () => {
  const error = vi.spyOn(console, 'error').mockImplementation(() => {});
  const { app, router, root } = stubs(() => Promise.resolve());

  await mountWhenReady(app, router, root);

  expect(app.mount).toHaveBeenCalledWith(root);
  expect(error).not.toHaveBeenCalled();
});
