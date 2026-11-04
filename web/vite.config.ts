import { defineConfig, type Plugin } from 'vite';
import vue from '@vitejs/plugin-vue';
import { writeFileSync, mkdirSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { brotliCompressSync, constants as zlibConstants } from 'node:zlib';

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

// Every fingerprinted asset is compressed once here with brotli at its
// strongest setting, so the server sends finished bytes: quality 11 is too slow
// to run per request, and it is what every current browser asks for first.
// The copies live outside assets/, which stays the bundler's output alone
// (test/bundle.test.ts counts it), under the same names with .br added;
// internal/web sends one when the request accepts br and the original
// otherwise.
//
// There is no gzip copy. Node's zlib picks its matching code by CPU, so the
// same file gzipped on an ARM laptop and on an x86 runner comes out hundreds of
// bytes apart, and a first paint quoted from one machine could not be
// reproduced on the other. Go's compressor gives the same bytes everywhere, so
// gzip is left to httpx.Compress on the way out. Brotli's encoder has no such
// variation: both machines produce the same copies.
function precompressAssets(): Plugin {
  return {
    name: 'obsidian-precompress-assets',
    closeBundle() {
      const from = resolve(OUT_DIR, 'assets');
      const to = resolve(OUT_DIR, 'precompressed', 'assets');
      let names: string[];
      try {
        names = readdirSync(from);
      } catch {
        return;
      }
      mkdirSync(to, { recursive: true });
      for (const name of names) {
        const body = readFileSync(resolve(from, name));
        writeFileSync(resolve(to, name + '.br'), brotliCompressSync(body, {
          params: {
            [zlibConstants.BROTLI_PARAM_QUALITY]: zlibConstants.BROTLI_MAX_QUALITY,
            [zlibConstants.BROTLI_PARAM_SIZE_HINT]: body.length,
          },
        }));
      }
    },
  };
}

export default defineConfig({
  root: HERE,
  // The Options API branch is compiled out of the bundle. Nothing in the tree
  // uses it, and the runtime it would keep is on the first paint of everyone.
  plugins: [vue({ features: { optionsAPI: false } }), keepEmbedDirectory(), precompressAssets()],
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
