import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// Invariants about the shipped stylesheet that no component test can see.
//
// These are here because of a bug that took a browser to find: the
// conversation rail slid out when it collapsed and snapped back when it
// opened, and every rule involved looked right on its own.
describe('stylesheet invariants', () => {
  const assetsDir = path.resolve(__dirname, '../../internal/web/dist/assets');

  function builtCss(): string {
    if (!fs.existsSync(assetsDir)) {
      throw new Error('no build to inspect — run `make web` first.');
    }
    const file = fs.readdirSync(assetsDir).find((name) => /^index-[^.]+\.css$/.test(name));
    if (!file) throw new Error('no stylesheet in the build output');
    return fs.readFileSync(path.join(assetsDir, file), 'utf8');
  }

  /** Every rule in the sheet, as [selector list, declarations]. */
  function rules(css: string): Array<[string, string]> {
    return [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].map((m) => [m[1]!.trim(), m[2]!]);
  }

  it('does not reset the transitions of everything in a row', () => {
    // `transition` is a shorthand, so a rule that sets it on
    // `.ai-chat > :not(.oa-panel)` to arrange one property's timing throws
    // away every other transition on the rail and the transcript. That rule
    // existed, at the same specificity as the rail's own and in a file that
    // loads later, so it won — and the rail lost its `margin-left` transition
    // in precisely the state that had no third class to outrank it. The
    // collapse animated; the expansion snapped.
    //
    // Nothing that matches a whole row of children may set the shorthand. If
    // one property needs its own timing there, say so on the state that needs
    // it, not on the resting state of everything.
    const offenders = rules(builtCss())
      .filter(([selector, body]) => /\.(ai-chat|oa-admin)\s*>\s*:not\(\.oa-panel\)/.test(selector)
        && /(^|;)\s*transition\s*:/.test(body)
        // The hiding half is allowed: it only ever applies while a panel is
        // actually covering the row, and it is the timing being arranged.
        && !/:has\(/.test(selector));

    expect(offenders.map(([selector]) => selector)).toEqual([]);
  });

  it('keeps the conversation rail able to travel in both directions', () => {
    const css = builtCss();
    const rail = rules(css).filter(([selector]) => /\.ai-chat-wide[^,{]*\.ai-chat-sidebar/.test(selector));

    // The open state and the collapsed state each have to carry the movement,
    // because a transition is read from whichever style is being moved *to*.
    const open = rail.find(([selector]) => /^\.ai-chat-wide \.ai-chat-sidebar$/.test(selector.split(',')[0]!.trim()));
    const collapsed = rail.find(([selector]) => /\.rail-collapsed/.test(selector));

    expect(open?.[1]).toMatch(/transition:[^;]*margin-left/);
    expect(collapsed?.[1]).toMatch(/transition:[^;]*margin-left/);
  });
});
