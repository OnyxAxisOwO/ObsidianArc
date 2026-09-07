import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

// DOM-dependent modules (safeIntro, markdown, math) and every component test
// need browser globals. jsdom runs them in-process with zero browser
// binaries or external drivers.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': resolve(HERE, 'src') },
  },
  test: {
    environment: 'jsdom',
  },
});
