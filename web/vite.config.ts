import { defineConfig, type Plugin } from 'vite';
import vue from '@vitejs/plugin-vue';
import { writeFileSync, mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { brotliCompressSync, gzipSync, constants as zlibConstants } from 'node:zlib';
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

// Writes a brotli and a gzip copy beside every script, stylesheet and the
// shell, which internal/web sends instead of the file when the browser takes
// one. Brotli at its best level takes seconds on the entry script: paid once
// here, it is a sixth off the first paint; paid per request, it would be a
// stall on every uncached one, which is why the server's own compressor stays
// gzip. gzip is level 9 to match that compressor, so a browser without br
// receives what it did before. Node's own zlib, so this costs no dependency.
function precompress(): Plugin {
  const worth = /\.(js|css|html)$/;
  const write = (file: string) => {
    const body = readFileSync(file);
    writeFileSync(
      file + '.br',
      brotliCompressSync(body, {
        params: {
          [zlibConstants.BROTLI_PARAM_MODE]: zlibConstants.BROTLI_MODE_TEXT,
          [zlibConstants.BROTLI_PARAM_QUALITY]: zlibConstants.BROTLI_MAX_QUALITY,
          [zlibConstants.BROTLI_PARAM_SIZE_HINT]: body.length,
        },
      }),
    );
    writeFileSync(file + '.gz', gzipSync(body, { level: 9 }));
  };
  return {
    name: 'obsidian-precompress',
    apply: 'build',
    closeBundle() {
      const assets = resolve(OUT_DIR, 'assets');
      for (const name of readdirSync(assets)) {
        if (worth.test(name)) write(resolve(assets, name));
      }
      write(resolve(OUT_DIR, 'index.html'));
    },
  };
}

export default defineConfig({
  root: HERE,
  plugins: [vue(), keepEmbedDirectory(), precompress()],
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
    // This limit only prints a warning; it never fails the build. The
    // first-paint budget is asserted in web/test/bundle.test.ts, because every
    // byte here is embedded in the server binary, and the entry script and
    // stylesheet are what the browser downloads on first paint.
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
