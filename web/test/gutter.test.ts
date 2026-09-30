import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// jsdom has no layout, so this cannot measure the bug it guards against; it
// holds the rule that prevents it. `.oa-app-viewport` contains its layout,
// which makes it the box every `position: fixed` thing inside it is placed
// against. The gutter around the interface used to be #app's padding, so that
// box was the window less sixteen pixels a side, and a modal's scrim, the
// settings drawer and the side panel below three columns all stopped short of
// the edge they were written to reach — a pale frame down the right of every
// drawer. The gutter belongs to the page inside the box.

const read = (file: string): string => readFileSync(resolve(__dirname, '../src/styles', file), 'utf8');

/** The declarations of the first rule whose selector is exactly `selector`. */
function declarations(source: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\#]/g, '\\$&');
  const match = new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`).exec(source);
  if (!match) throw new Error(`no rule for ${selector}`);
  return match[1]!;
}

describe('the gutter around the interface', () => {
  it('is not on #app, which is outside the box fixed elements are placed against', () => {
    expect(declarations(read('_base.scss'), '#app')).not.toMatch(/padding/);
  });

  it('is on the page inside the contained viewport', () => {
    const app = read('_app.scss');
    expect(declarations(app, '.oa-app-viewport')).toMatch(/contain:\s*layout paint/);
    expect(declarations(app, '.oa-app-viewport')).not.toMatch(/padding/);
    expect(declarations(app, '.oa-app-page')).toMatch(/padding:\s*16px/);
  });

  it('is still dropped for the sign-in card and the landing page', () => {
    const app = read('_app.scss');
    expect(app).toMatch(/\.oa-app-page:has\(\.oa-auth\)[^{]*\{\s*padding:\s*0/);
    expect(app).toMatch(/\.oa-app-page:has\(\.oa-front\)[^{]*\{\s*padding:\s*0/);
  });
});
