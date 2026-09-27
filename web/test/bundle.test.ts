import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// Asserts code-splitting invariants across build artifacts.
//
// The main entry bundle must not pull in the lazy-loaded chunks: the admin
// shell, the terminal, the Chinese dictionary, the LaTeX renderer and the
// public front page. Those five are the only splits this project has, and
// each is deliberate — a quarter of the code most accounts can never reach, a
// panel every account may open and few ever will, a dictionary only half the
// readers need, a renderer only a maths answer wakes, and a marketing page
// only an instance that has chosen it ever draws.
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

  it('contains the expected chunk files without extra fragments', () => {
    const files = builtAssets();

    // Exactly seven, and deliberately exact: the project ships no fonts, no
    // images and no other chunks, so an eighth file is either a split that
    // was not meant to happen or an asset nobody decided to ship. Route-level
    // splitting in particular produces a dozen of these on its own and is
    // switched off for everything but the backoffice and the terminal — see
    // src/router. It was five until the terminal moved out of the backoffice
    // into every account's menu, and six until the front page arrived.
    expect(files, `unexpected build output: ${files.join(', ')}`).toHaveLength(7);

    const indexJs = files.filter((f) => /^index-[^.]+\.js$/.test(f));
    const indexCss = files.filter((f) => /^index-[^.]+\.css$/.test(f));
    const adminJs = files.filter((f) => /^AdminPage-[^.]+\.js$/.test(f));
    const zhJs = files.filter((f) => /^i18n\.zh-[^.]+\.js$/.test(f));
    const mathJs = files.filter((f) => /^math-[^.]+\.js$/.test(f));
    const terminalJs = files.filter((f) => /^TerminalPanel-[^.]+\.js$/.test(f));
    const frontJs = files.filter((f) => /^FrontPage-[^.]+\.js$/.test(f));

    expect(indexJs.length).toBe(1);
    expect(indexCss.length).toBe(1);
    expect(adminJs.length).toBe(1);
    expect(zhJs.length).toBe(1);
    expect(mathJs.length).toBe(1);
    expect(terminalJs.length).toBe(1);
    expect(frontJs.length).toBe(1);
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
});
