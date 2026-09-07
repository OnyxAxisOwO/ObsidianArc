import { defineConfig, type Plugin } from 'vite';
import vue from '@vitejs/plugin-vue';
import { writeFileSync, mkdirSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// The config is ESM (package.json sets "type": "module"), so __dirname does
// not exist here.
const HERE = dirname(fileURLToPath(import.meta.url));
const OUT_DIR = resolve(HERE, '../internal/web/dist');

// The Go build embeds `internal/web/dist` with `//go:embed all:dist`, which
// fails outright if the directory has no files. `emptyOutDir` wipes it on
// every build, so the marker that keeps a fresh clone compilable has to be
// put back afterwards.
function keepEmbedDirectory(): Plugin {
  return {
    name: 'obsidian-keep-embed-dir',
    closeBundle() {
      mkdirSync(OUT_DIR, { recursive: true });
      writeFileSync(
        resolve(OUT_DIR, '.gitkeep'),
        '# Keeps //go:embed all:dist compilable before the frontend is built.\n',
      );
    },
  };
}

export default defineConfig({
  root: HERE,
  plugins: [vue(), keepEmbedDirectory()],
  resolve: {
    alias: { '@': resolve(HERE, 'src') },
  },
  css: {
    preprocessorOptions: {
      scss: {
        // Dart Sass 2 removes the legacy JS API; opting in now keeps the
        // build quiet and forward-compatible.
        api: 'modern-compiler',
      },
    },
  },
  build: {
    outDir: OUT_DIR,
    emptyOutDir: true,
    // Every byte here ends up inside the server binary and in the browser on
    // first paint, so a regression is worth failing the build over rather
    // than discovering in production.
    chunkSizeWarningLimit: 400,
    target: 'es2022',
    sourcemap: false,
  },
  server: {
    // IPv4 explicitly, not Vite's default `localhost`.
    //
    // The Go server's dev proxy targets `http://127.0.0.1:5173` — a literal
    // address, not a name. On a machine where `localhost` resolves to `::1`
    // first, Vite binds only the IPv6 loopback, the proxy's connection is
    // refused, and every page through :8080 is a 502 while Vite's own banner
    // says it is ready. Binding the address the proxy actually dials is what
    // keeps the two halves of `make dev` agreeing.
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    // `npm run dev` is only ever reached through the Go server's dev proxy,
    // so API calls go to the same origin the page came from.
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
});
