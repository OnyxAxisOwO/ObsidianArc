import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// Asserts code-splitting invariants across build artifacts.
// The main entry bundle must not pull in lazy-loaded chunks (admin shell,
// Chinese dictionary, and LaTeX math renderer) via accidental static imports.
describe('code splitting invariants in production bundle', () => {
  const assetsDir = path.resolve(__dirname, '../../internal/web/dist/assets');

  // Verify whether build output is available.
  const hasDist = fs.existsSync(assetsDir);

  it('contains the expected chunk files without extra fragments', () => {
    if (!hasDist) {
      // If tests are executed before build, skip gracefully.
      return;
    }

    const files = fs.readdirSync(assetsDir);

    // Exactly 5 files: index CSS, index JS, admin-page JS, i18n.zh JS, math JS.
    expect(files.length).toBe(5);

    const indexJs = files.filter((f) => /^index-[^.]+\.js$/.test(f));
    const indexCss = files.filter((f) => /^index-[^.]+\.css$/.test(f));
    const adminJs = files.filter((f) => /^admin-page-[^.]+\.js$/.test(f));
    const zhJs = files.filter((f) => /^i18n\.zh-[^.]+\.js$/.test(f));
    const mathJs = files.filter((f) => /^math-[^.]+\.js$/.test(f));

    expect(indexJs.length).toBe(1);
    expect(indexCss.length).toBe(1);
    expect(adminJs.length).toBe(1);
    expect(zhJs.length).toBe(1);
    expect(mathJs.length).toBe(1);
  });

  it('keeps admin, Chinese translation, and math renderer out of the main bundle', () => {
    if (!hasDist) return;

    const files = fs.readdirSync(assetsDir);
    const indexJsFile = files.find((f) => /^index-[^.]+\.js$/.test(f))!;
    const adminJsFile = files.find((f) => /^admin-page-[^.]+\.js$/.test(f))!;
    const zhJsFile = files.find((f) => /^i18n\.zh-[^.]+\.js$/.test(f))!;
    const mathJsFile = files.find((f) => /^math-[^.]+\.js$/.test(f))!;

    const indexContent = fs.readFileSync(path.join(assetsDir, indexJsFile), 'utf8');
    const adminContent = fs.readFileSync(path.join(assetsDir, adminJsFile), 'utf8');
    const zhContent = fs.readFileSync(path.join(assetsDir, zhJsFile), 'utf8');
    const mathContent = fs.readFileSync(path.join(assetsDir, mathJsFile), 'utf8');

    // 1. Admin screen routes: only present in admin-page chunk
    expect(adminContent).toContain('/api/admin/users');
    expect(indexContent).not.toContain('/api/admin/users');
    expect(indexContent).not.toContain('/api/admin/providers');

    // 2. MathML renderer internals: only present in math chunk
    expect(mathContent).toContain('http://www.w3.org/1998/Math/MathML');
    expect(mathContent).toContain('ai-math-block');
    expect(indexContent).not.toContain('http://www.w3.org/1998/Math/MathML');
    expect(indexContent).not.toContain('ai-math-block');

    // 3. Chinese dictionary: only present in i18n.zh chunk
    expect(zhContent).toContain('管理后台');
    expect(zhContent).toContain('对话列表');
    expect(indexContent).not.toContain('管理后台');
    expect(indexContent).not.toContain('对话列表');
  });
});
