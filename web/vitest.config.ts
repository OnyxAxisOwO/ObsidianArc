import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

// DOM-dependent modules (safeIntro, markdown, math) and every component test
// need browser globals. jsdom runs them in-process with zero browser
// binaries or external drivers.
export default defineConfig({
  // The same flag as vite.config.ts, so the tests run the component code the
  // build ships.
  plugins: [vue({ features: { optionsAPI: false } })],
  resolve: {
    alias: { '@': resolve(HERE, 'src') },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['test/setup.ts'],
  },
});
