import { defineConfig } from 'vitest/config';

// DOM-dependent modules (safeIntro, markdown, math) need browser globals
// (document, window, Element) to render. jsdom runs them in-process with
// zero browser binaries or external drivers.
export default defineConfig({
  test: {
    environment: 'jsdom',
  },
});