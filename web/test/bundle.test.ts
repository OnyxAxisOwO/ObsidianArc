import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';

// Asserts code-splitting invariants across build artifacts.
//
// The main entry bundle must not pull in the lazy-loaded chunks: the admin
// shell, the terminal, the Chinese dictionary, the LaTeX renderer, the public
// front page and the landing page. Those six are the only splits this project
// has, and each is deliberate — a quarter of the code most accounts can never
// reach, a panel every account may open and few ever will, a dictionary only
// half the readers need, a renderer only a maths answer wakes, a marketing
// page and a landing page only an instance that has chosen one ever draws.
describe('code splitting invariants in production bundle', () => {
  const assetsDir = path.resolve(__dirname, '../../internal/web/dist/assets');

  // These assertions are about what the bundler produced, so they are
  // meaningless without a build — and a check that quietly passes when it
  // cannot run is not a check. It says what to do instead.
  //
  // CI builds before it tests, so this only ever fires on a working copy that
  // has not run `make web` yet.
  function builtAssets(): string[] {
    if (!fs.existsSync(assetsDir)) {
      throw new Error(
        'no build to inspect at ' + assetsDir + ' — run `make web` first. ' +
        'These assertions are the only thing holding the code splitting in ' +
        'place, and skipping them would report a green that checked nothing.',
      );
    }
    return fs.readdirSync(assetsDir);
  }

  // Every plugin's browser half is a chunk of its own, named for its entry
  // (src/plugins/<name>/<name>.plugin.ts), and nothing else: see
  // src/plugins/registry.ts. They are counted from the source tree, so adding
  // a plugin adds its chunk here without anyone editing a number.
  const pluginNames = fs.readdirSync(path.resolve(__dirname, '../src/plugins'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name);
  const pluginChunk = (name: string) => new RegExp(`^${name}\\.plugin-[^.]+\\.js$`);

  it('contains the expected chunk files without extra fragments', () => {
    const files = builtAssets();
    const pluginFiles = files.filter((f) => pluginNames.some((name) => pluginChunk(name).test(f)));
    expect(pluginFiles, `plugin chunks: ${pluginFiles.join(', ')}`).toHaveLength(pluginNames.length);
    const core = files.filter((f) => !pluginFiles.includes(f));

    // Exactly eight besides the plugins', and deliberately exact: the project ships no fonts, no
    // images and no other chunks, so a ninth file is either a split that
    // was not meant to happen or an asset nobody decided to ship. Route-level
    // splitting in particular produces a dozen of these on its own and is
    // switched off for everything but the backoffice and the terminal — see
    // src/router. It was five until the terminal moved out of the backoffice
    // into every account's menu, six until the front page arrived, and seven
    // when the landing page moved out of RootView's static imports.
    // A chunk shared between two plugins — a helper both import — shows up
    // here as a ninth file: keep such helpers in the entry instead.
    expect(core, `unexpected build output: ${core.join(', ')}`).toHaveLength(8);

    const indexJs = files.filter((f) => /^index-[^.]+\.js$/.test(f));
    const indexCss = files.filter((f) => /^index-[^.]+\.css$/.test(f));
    const adminJs = files.filter((f) => /^AdminPage-[^.]+\.js$/.test(f));
    const zhJs = files.filter((f) => /^i18n\.zh-[^.]+\.js$/.test(f));
    const mathJs = files.filter((f) => /^math-[^.]+\.js$/.test(f));
    const terminalJs = files.filter((f) => /^TerminalPanel-[^.]+\.js$/.test(f));
    const frontJs = files.filter((f) => /^FrontPage-[^.]+\.js$/.test(f));
    const landingJs = files.filter((f) => /^LandingView-[^.]+\.js$/.test(f));

    expect(indexJs.length).toBe(1);
    expect(indexCss.length).toBe(1);
    expect(adminJs.length).toBe(1);
    expect(zhJs.length).toBe(1);
    expect(mathJs.length).toBe(1);
    expect(terminalJs.length).toBe(1);
    expect(frontJs.length).toBe(1);
    expect(landingJs.length).toBe(1);
  });

  it('keeps admin, Chinese translation, and math renderer out of the main bundle', () => {
    const files = builtAssets();
    const indexJsFile = files.find((f) => /^index-[^.]+\.js$/.test(f))!;
    const adminJsFile = files.find((f) => /^AdminPage-[^.]+\.js$/.test(f))!;
    const zhJsFile = files.find((f) => /^i18n\.zh-[^.]+\.js$/.test(f))!;
    const mathJsFile = files.find((f) => /^math-[^.]+\.js$/.test(f))!;

    const indexContent = fs.readFileSync(path.join(assetsDir, indexJsFile), 'utf8');
    const adminContent = fs.readFileSync(path.join(assetsDir, adminJsFile), 'utf8');
    const zhContent = fs.readFileSync(path.join(assetsDir, zhJsFile), 'utf8');
    const mathContent = fs.readFileSync(path.join(assetsDir, mathJsFile), 'utf8');

    // 1. Admin screen routes: only present in the admin chunk
    expect(adminContent).toContain('/api/admin/users');
    expect(indexContent).not.toContain('/api/admin/users');
    expect(indexContent).not.toContain('/api/admin/providers');

    // 2. The LaTeX renderer: only present in the math chunk.
    //
    // Its class names rather than the MathML namespace, which used to be the
    // marker here. Vue's own runtime-dom carries that namespace — it has to,
    // to create elements inside a <math> — so the string is now in the entry
    // whether or not this project's renderer is, and asserting on it would
    // fail for a reason that has nothing to do with code splitting.
    expect(mathContent).toContain('ai-math-block');
    expect(mathContent).toContain('ai-math-inline');
    expect(indexContent).not.toContain('ai-math-block');
    expect(indexContent).not.toContain('ai-math-inline');

    // 3. The terminal's client: only present in its own chunk. The account
    // menu names the route; the emulator behind it is not on the first paint.
    const terminalJsFile = files.find((f) => /^TerminalPanel-[^.]+\.js$/.test(f))!;
    const terminalContent = fs.readFileSync(path.join(assetsDir, terminalJsFile), 'utf8');
    expect(terminalContent).toContain('/api/console/exec');
    expect(indexContent).not.toContain('/api/console/exec');

    // 4. Chinese dictionary: only present in the i18n.zh chunk
    expect(zhContent).toContain('管理后台');
    expect(zhContent).toContain('对话列表');
    expect(indexContent).not.toContain('管理后台');
    expect(indexContent).not.toContain('对话列表');

    // 5. The front page: only present in its own chunk. Its markup and its
    // whole reveal/marquee/counter apparatus are for visitors to an instance
    // that has chosen it as its front door, which is not the default — so
    // none of it belongs on the first paint of somebody opening the chat.
    // Its class prefix rather than a string from the page: the page's own
    // sentences all live in the dictionary, which is a different chunk.
    const frontJsFile = files.find((f) => /^FrontPage-[^.]+\.js$/.test(f))!;
    const frontContent = fs.readFileSync(path.join(assetsDir, frontJsFile), 'utf8');
    expect(frontContent).toContain('oa-front-marquee');
    expect(indexContent).not.toContain('oa-front-marquee');

    // 6. The landing page and its trial client: only present in their own
    // chunk. RootView draws it only for a signed-out visitor to an instance
    // whose front door is the intro or the trial, so the trial's streaming
    // client is not on the first paint of everyone who signed in. Its class
    // prefix, as with the front page above.
    const landingJsFile = files.find((f) => /^LandingView-[^.]+\.js$/.test(f))!;
    const landingContent = fs.readFileSync(path.join(assetsDir, landingJsFile), 'utf8');
    expect(landingContent).toContain('oa-landing-chat');
    expect(indexContent).not.toContain('oa-landing-chat');
  });

  it('ships one copy of the framework, in the entry', () => {
    const files = builtAssets();
    const indexJsFile = files.find((f) => /^index-[^.]+\.js$/.test(f))!;
    const adminJsFile = files.find((f) => /^AdminPage-[^.]+\.js$/.test(f))!;

    // A string literal from Vue's runtime-dom — the namespace it needs to
    // create an element inside <math>. A literal rather than an identifier,
    // because minification renames every identifier and would make this
    // assertion pass by accident. Two copies would mean the backoffice had
    // been given a framework of its own, which is several times the size of
    // the screens in it.
    const marker = 'http://www.w3.org/1998/Math/MathML';
    expect(fs.readFileSync(path.join(assetsDir, indexJsFile), 'utf8')).toContain(marker);
    expect(fs.readFileSync(path.join(assetsDir, adminJsFile), 'utf8')).not.toContain(marker);
  });

  it('keeps each plugin out of the main bundle, and the framework out of each plugin', () => {
    const files = builtAssets();
    const marker = 'http://www.w3.org/1998/Math/MathML';

    for (const name of pluginNames) {
      const file = files.find((f) => pluginChunk(name).test(f))!;
      const content = fs.readFileSync(path.join(assetsDir, file), 'utf8');
      // A plugin that pulled the framework, or anything else the entry
      // already has, into its own chunk would ship it twice.
      expect(content, `${name} carries a copy of the framework`).not.toContain(marker);
      expect(content.length, `${name}'s chunk is suspiciously large`).toBeLessThan(40_000);
      // Its own name is in its chunk as the plugin's registered name, and
      // nowhere in the entry but the registry's list of loaders.
      expect(content).toContain(`name:"${name}"`);
    }
  });

  // What a first paint downloads: the entry script and its stylesheet, gzipped
  // by zlib at its default level. That is the build's own estimate, the figure
  // the docs call the build's gzip column, and it is not what a server sends:
  // the server compresses at a different level, and internal/web/wire_test.go
  // holds the budget on the bytes it sends. This check still matters, because
  // Vite's chunk-size limit only warns, so it is what fails a build which grew
  // the core. The budget only ever ratchets down toward the 135 kB target in
  // AGENTS.md: raising it to let a change pass is the change being refused,
  // not the budget being wrong.
  function firstPaintGzipBytes(): number {
    const files = builtAssets();
    const firstPaint = files.filter((f) => /^index-[^.]+\.(js|css)$/.test(f));
    expect(firstPaint).toHaveLength(2);
    return firstPaint.reduce(
      (sum, f) => sum + zlib.gzipSync(fs.readFileSync(path.join(assetsDir, f))).length,
      0,
    );
  }

  it('keeps the first paint within its gzip budget', () => {
    const FIRST_PAINT_GZIP_BUDGET = 250_000;
    const gzipped = firstPaintGzipBytes();
    expect(gzipped, `first paint is ${gzipped} bytes gzipped, budget ${FIRST_PAINT_GZIP_BUDGET}`).toBeLessThanOrEqual(
      FIRST_PAINT_GZIP_BUDGET,
    );
  });

  // The docs quote this figure, and they drifted once while the bundle moved
  // under them. Naming the documents that quote it means the next change that
  // moves the bundle has to move them in the same change.
  it('quotes the built first paint in the README, the architecture notes and AGENTS.md', () => {
    const kb = (firstPaintGzipBytes() / 1000).toFixed(2);
    // Not a bare substring: 1248.80 kB would otherwise quote 248.80 kB by accident.
    const quoted = new RegExp(`(?<![\\d.])${kb.replace('.', '\\.')} kB`);
    for (const doc of ['README.md', 'docs/ARCHITECTURE.md', 'AGENTS.md']) {
      const text = fs.readFileSync(path.resolve(__dirname, '../../', doc), 'utf8');
      expect(quoted.test(text), `${doc} does not quote the first paint as ${kb} kB`).toBe(true);
    }
  });
});
