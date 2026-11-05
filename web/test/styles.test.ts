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

  // Every stylesheet the build ships, the entry's first: the backoffice, the
  // terminal, the front page and the landing page each bring their own, and
  // a rule in one of those reaches the same rows as a rule in the entry.
  function builtCss(): string {
    if (!fs.existsSync(assetsDir)) {
      throw new Error('no build to inspect — run `make web` first.');
    }
    const files = fs.readdirSync(assetsDir).filter((name) => name.endsWith('.css'));
    const entry = files.find((name) => /^index-[^.]+\.css$/.test(name));
    if (!entry) throw new Error('no stylesheet in the build output');
    return [entry, ...files.filter((name) => name !== entry).sort()]
      .map((name) => fs.readFileSync(path.join(assetsDir, name), 'utf8'))
      .join('\n');
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

describe('hidden rows in the stylesheet source', () => {
  it('hides a flex input row that carries the hidden attribute', () => {
    const source = fs.readFileSync(path.resolve(__dirname, '../src/styles/_workspace.scss'), 'utf8');
    const rule = /\.oa-input-row\[hidden\]\s*\{[^}]*\}/.exec(source)?.[0] ?? '';

    expect(rule, 'the redeem row would stay on screen when it closes').toMatch(/display:\s*none/);
  });
});

// Source-level checks for the cascade details jsdom cannot see. Each one
// fails on the rule as it was written, so a later edit that brings the
// problem back is caught without a build.
describe('component rules in the stylesheet source', () => {
  const source = (name: string) => fs.readFileSync(path.resolve(__dirname, '../src/styles', name), 'utf8');

  it('keeps a focus ring on plugin tabs, which have no outline', () => {
    expect(source('_workbench.scss')).toMatch(
      /\.oa-plugin-tab:focus-visible \{[^}]*box-shadow:\s*0 0 0 3px var\(--ai-focus-shadow\)/,
    );
  });

  it('keeps the field hover fill and focus ring off the range sliders', () => {
    // OaRangeField wraps the slider in a .oa-field, so the field's hover and
    // focus rules match it at the same specificity and come later. The
    // slider needs rules that out-rank them, and the field rules must stay
    // as they are for every other input.
    const workspace = source('_workspace.scss');
    const override = /\.oa-range-field input\[type="range"\]:hover,\s*\.oa-range-field input\[type="range"\]:focus \{([^}]*)\}/
      .exec(workspace)?.[1] ?? '';
    const slider = /\.oa-range-field input\[type="range"\] \{([^}]*)\}/.exec(workspace)?.[1] ?? '';

    expect(override).toMatch(/background-color:\s*transparent/);
    expect(override).toMatch(/box-shadow:\s*none/);
    expect(slider).toMatch(/outline:\s*none/);
    expect(workspace).toMatch(/\.oa-field input:hover,/);
    expect(workspace).toMatch(/\.oa-field input:focus,/);
  });

  it('gives the terminal its own width only in the three-column layout', () => {
    // Below 1101px the panel is a fixed overlay sized by _surfaces.scss; a
    // bare width: auto here would shrink it to fit its content.
    const terminal = source('_terminal.scss');

    expect(terminal).toMatch(/@media \(min-width: 1101px\) \{\s*\.ai-chat-wide \.oa-panel\.oa-terminal-panel \{\s*width: auto;/);
    expect(terminal).not.toMatch(/\.ai-chat-wide \.oa-panel\.oa-terminal-panel \{\s*flex: 1 1 0%;\s*width: auto/);
  });

});

// The leaderboards render the usage report's board class names, and
// _surfaces.scss, which loads first, defines their rows. A bare rule in
// _analytics.scss wins over it on every shared property, so each rule that
// names one of these classes has to stay under the frame the usage report
// always wraps.
describe('ranked board rules in the stylesheet source', () => {
  const analytics = fs
    .readFileSync(path.resolve(__dirname, '../src/styles/_analytics.scss'), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');
  const shared = /^(button\.)?\.oa-board-(row|rank|main|name|notes|value|track)(?![\w-])/;

  it('scopes every rule that shares a leaderboard class name under the usage frame', () => {
    const bare = [...analytics.matchAll(/([^{}]+)\{/g)]
      .map((m) => m[1]!.split(';').pop()!)
      .flatMap((prelude) => prelude.split(','))
      .map((selector) => selector.trim())
      .filter((selector) => shared.test(selector));

    expect(bare).toEqual([]);
  });

  it('clips a leaderboard name on one line where the frame does not wrap it', () => {
    // The leaderboard panel and the admin preview take this rule with the
    // name as bare text, so the box itself has to clip it. The nested span
    // rule comes out first, since its own overflow would satisfy the check.
    const surfaces = fs
      .readFileSync(path.resolve(__dirname, '../src/styles/_surfaces.scss'), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '');
    const name = /^\.oa-board-name \{([\s\S]*?)^\}/m.exec(surfaces)?.[1] ?? '';
    const box = name.replace(/>\s*span:first-child\s*\{[^}]*\}/, '');

    expect(box).toMatch(/overflow:\s*hidden/);
    expect(box).toMatch(/white-space:\s*nowrap/);
  });
});

// Phone-width and state rules for the chat, the front page and the dashboard.
// jsdom has no layout, so each of these is checked in the source: a rule that
// a narrow screen or an idle page would break, written as it should be.
describe('phone and state rules in the stylesheet source', () => {
  // Comments are stripped so a rule that is only described in a comment
  // cannot satisfy the check.
  const sheet = (name: string): string => fs
    .readFileSync(path.resolve(__dirname, '../src/styles', name), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');

  /** The declarations of every flat rule whose selector list names `selector`. */
  function bodies(css: string, selector: string): string[] {
    return [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
      .filter((m) => m[1]!.split(',').some((part) => part.trim() === selector))
      .map((m) => m[2]!);
  }

  /** The inside of every block whose prelude starts with `query`, e.g. a media query. */
  function blocks(css: string, query: string): string {
    const inside: string[] = [];
    for (let at = css.indexOf(query); at !== -1; at = css.indexOf(query, at + query.length)) {
      const open = css.indexOf('{', at);
      let depth = 0;
      for (let i = open; i < css.length; i += 1) {
        if (css[i] === '{') depth += 1;
        if (css[i] === '}') {
          depth -= 1;
          if (depth === 0) {
            inside.push(css.slice(open + 1, i));
            break;
          }
        }
      }
    }
    return inside.join('\n');
  }

  it('keeps the empty-history footer hidden', () => {
    // The footer is `display: flex`, which beats the user agent's [hidden] rule,
    // so the attribute needs a rule of its own or the button shows on an
    // empty history.
    expect(bodies(sheet('_chat.scss'), '.ai-chat-sidebar-foot[hidden]').join('')).toMatch(/display:\s*none/);
  });

  it('drops the desktop rail feather on the phone overlay', () => {
    // A persisted rail collapse carries the desktop mask into the overlay,
    // which fades the overlay's right edge and clips its shadow.
    const overlay = bodies(
      blocks(sheet('_chat.scss'), '@media (max-width: 900px)'),
      '.ai-chat-wide .ai-chat-sidebar',
    ).join('');

    expect(overlay).toMatch(/(^|[;\s])mask-image:\s*none/);
  });

  it('gives a closed side panel no transform override under reduced motion', () => {
    // On narrow screens a closed panel is parked off the right edge by its
    // transform. Zeroing that under reduced motion left it at the edge,
    // transparent and taking the taps meant for the page.
    const reduced = blocks(sheet('_surfaces.scss'), '@media (prefers-reduced-motion: reduce)');

    expect(bodies(reduced, '.oa-panel').join('')).not.toMatch(/transform:\s*none/);
  });

  it('writes each reduced-motion reset after the rule it resets', () => {
    // A media query adds no weight, so a reset written above its base rule
    // loses to it: the settings navigation kept fading, and the issued key
    // kept glowing, for readers who had asked for less motion.
    const surfaces = sheet('_surfaces.scss');
    const reduced = blocks(surfaces, '@media (prefers-reduced-motion: reduce)');
    const cases: Array<[string, 'transition' | 'animation']> = [
      ['.oa-settings-rail-item', 'transition'],
      ['.oa-settings-tab', 'transition'],
      ['.oa-key-token-pulse', 'animation'],
      ['.oa-key-row', 'animation'],
    ];

    for (const [selector, property] of cases) {
      const declared = bodies(surfaces, selector).filter((body) => new RegExp(`(^|[;\\s])${property}:`).test(body));
      expect(declared.at(-1), selector).toMatch(new RegExp(`${property}:\\s*none`));
      expect(bodies(reduced, selector).join(''), selector).toMatch(new RegExp(`${property}:\\s*none`));
    }
  });

  it('keeps a focus ring on the aspect ratio tiles, which have no outline', () => {
    // The selected tile keeps its accent ring inside the focus ring; a bare
    // focus shadow would replace it while the tile has focus.
    const tile = /^\.oa-ratio-tile \{([\s\S]*?)^\}/m.exec(sheet('_surfaces.scss'))?.[1] ?? '';

    expect(tile).toMatch(/&:focus-visible \{\s*box-shadow:\s*0 0 0 2px var\(--ai-focus-shadow\)/);
    expect(tile).toMatch(
      /&\.active:focus-visible \{\s*box-shadow:\s*0 0 0 1px var\(--ai-primary\),\s*0 0 0 3px var\(--ai-focus-shadow\)/,
    );
  });

  it('caps the row stagger so a long page does not hold its last rows hidden', () => {
    // `backwards` fill keeps each row at opacity 0 for its whole delay, so an
    // uncapped index-based delay hid row 199 for about eight seconds.
    // The backoffice's half of the surfaces is in its own file since the
    // split; the two rows that stagger are one in each.
    const surfaces = sheet('_surfaces.scss') + sheet('_admin-surfaces.scss');

    expect(surfaces).not.toMatch(/animation-delay:\s*calc\(var\(--item-idx/);
    expect(surfaces.match(/animation-delay:\s*min\(calc\(var\(--item-idx, 0\) \* 40ms\), 240ms\)/g)).toHaveLength(2);
  });

  it('pins a leaving toast to the stack width on a phone', () => {
    // Absolutely positioned with auto width and no insets, it shrinks to its
    // text as it leaves; the insets hold it to the stack's width.
    const phone = blocks(sheet('_surfaces.scss'), '@media (max-width: 640px)');
    const leaving = bodies(phone, '.oa-toast-leave-active').join('');

    expect(leaving).toMatch(/left:\s*0;/);
    expect(leaving).toMatch(/right:\s*0;/);
  });

  it('keeps the conversation menu reachable without a pointer', () => {
    // A touch screen has no hover to reveal the trigger, and rename, archive
    // and delete live behind it.
    const touch = bodies(
      blocks(sheet('_chat.scss'), '@media (hover: none)'),
      '.ai-chat-list-more',
    ).join('');

    expect(touch).toMatch(/opacity:\s*(0\.[1-9]|1\b)/);
  });

  it('lets the front page grids shrink below their nominal track width', () => {
    // A bare 340px or 290px minimum is wider than the content box of a phone
    // under about 376px, and the page clips the overflow rather than scrolling.
    const front = sheet('_front.scss');

    expect(bodies(front, '.oa-front-panes').join('')).toMatch(/minmax\(min\(340px,\s*100%\),\s*1fr\)/);
    expect(bodies(front, '.oa-front-cards').join('')).toMatch(/minmax\(min\(290px,\s*100%\),\s*1fr\)/);
  });

  it('lets the scroll cue leave, which its nudge animation would otherwise prevent', () => {
    expect(bodies(sheet('_front.scss'), '.oa-front-scroll.gone').join('')).toMatch(/animation:\s*none/);
  });

  it('pauses the hero and the flow wherever the observers can reach them', () => {
    const front = sheet('_front.scss');

    expect(bodies(front, '.oa-front-hero.idle .oa-front-scroll').join('')).toMatch(/animation-play-state:\s*paused/);
    expect(bodies(front, '.oa-front-hero.idle .oa-front-line.accent > span').join(''))
      .toMatch(/animation-play-state:\s*paused/);
    expect(bodies(front, '.oa-front-flow.idle .oa-front-wire').join('')).toMatch(/animation-play-state:\s*paused/);
    expect(bodies(front, '.oa-front-flow.idle .oa-front-core-ring').join(''))
      .toMatch(/animation-play-state:\s*paused/);
  });

});

// Focus, width and cascade details in the shipped stylesheet source. Each one
// was true of the rule as written: a focus that no ring replaced, a width cap
// that a table cell ignores, a narrow override that an equal-specificity rule
// later in the file undid. The checks read the source so they hold without a
// build.
describe('focus, width and cascade rules in the stylesheet source', () => {
  const sheet = (name: string): string => fs
    .readFileSync(path.resolve(__dirname, '../src/styles', name), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '');

  /** The declarations of every flat rule whose selector list names `selector`. */
  function bodies(css: string, selector: string): string {
    return [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
      .filter((m) => m[1]!.split(';').pop()!.split(',').some((part) => part.trim() === selector))
      .map((m) => m[2]!)
      .join('');
  }

  it('rings the landing composer, because its field has no outline of its own', () => {
    expect(bodies(sheet('_landing.scss'), '.oa-landing-composer:focus-within'))
      .toMatch(/box-shadow:\s*0 0 0 3px var\(--ai-focus-shadow\)/);
  });

  it('rings a board whose scroller takes focus, and keeps the native outline off it', () => {
    const analytics = sheet('_analytics.scss');

    expect(analytics).toMatch(/\.oa-board-wrap:has\(> \.oa-board-scroll:focus-visible\) \{[^}]*var\(--ai-focus-shadow\)/);
    expect(bodies(analytics, '.oa-board-scroll:focus-visible')).toMatch(/outline:\s*none/);
  });

  it('declares every theme token it reads without a fallback', () => {
    // A var() whose token nothing declares makes the declaration invalid, which
    // computes as unset: a colour inherits, a background or border takes its
    // initial value, and nothing reports an error, so the rule looks right and
    // does nothing. Uses with an explicit fallback are deliberate and are left
    // out.
    const walk = (dir: string): string[] => fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) return walk(full);
      return /\.(scss|ts|vue)$/.test(entry.name) ? [fs.readFileSync(full, 'utf8')] : [];
    });
    const all = walk(path.resolve(__dirname, '../src')).join('\n');
    const used = new Set([...all.matchAll(/var\(\s*(--ai-[\w-]+)\s*\)/g)].map((m) => m[1]!));
    const declared = (token: string) => new RegExp(`${token}\\s*:|['"\`]${token}['"\`]`).test(all);

    expect([...used].filter((token) => !declared(token))).toEqual([]);
  });
});

// Rules that change what a reader sees in a state jsdom never renders: a
// disabled button, a touch screen, a jump to a section.
describe('control states in the stylesheet source', () => {
  const sheet = (name: string) => fs.readFileSync(path.resolve(__dirname, '../src/styles', name), 'utf8');

  it('reveals a folder’s actions on hover only where hover ends, and never reflows the row for them', () => {
    const chat = sheet('folders.scss');
    // A tap leaves :hover on a touch screen, so a hover rule outside this
    // query would lay the actions over the count until the next tap.
    const hover = /@media \(hover: hover\) \{([\s\S]*?)\n\}/.exec(chat.slice(chat.indexOf('.ai-folder-actions {')));
    expect(hover?.[1]).toMatch(/\.ai-folder-row:hover \.ai-folder-actions/);
    expect(chat.match(/\.ai-folder-row:hover/g)).toHaveLength(1);
    // Laid over the row, so revealing them moves nothing beside them.
    expect(chat).toMatch(/\.ai-folder-actions \{[^}]*position:\s*absolute;/);
    expect(chat).not.toMatch(/\.ai-folder-tail/);
  });

  it('keeps the row menu from scrolling itself, and its folders off the system scrollbar', () => {
    const chat = sheet('_chat.scss');

    expect(chat).toMatch(/\.oa-menu\.ai-chat-context-menu \{[^}]*max-height:\s*none;[^}]*overflow:\s*visible;/);
    expect(chat).toMatch(/\.ai-chat-menu-folders \{[^}]*max-height:\s*\d+px;[^}]*scrollbar-width:\s*none;\s*@include hidden-scrollbar;/);
  });

  it('draws every armed danger second click from one rule', () => {
    const surfaces = sheet('_surfaces.scss');

    expect(surfaces).toMatch(/\.oa-btn-danger-solid,\s*\.oa-bulk-bar \.oa-btn\.armed,\s*\.oa-ban-confirm\.armed \{/);
    expect(surfaces.match(/\.oa-bulk-bar \.oa-btn\.armed\b/g)).toHaveLength(1);
    expect(surfaces.match(/\.oa-ban-confirm\.armed\b/g)).toHaveLength(1);
  });
});

// Rules that only a keyboard or a reduced-motion setting ever reaches: each
// was missing from one copy of a control that had it in another.
describe('focus and motion on every copy of a control', () => {
  const sheet = (name: string) => fs.readFileSync(path.resolve(__dirname, '../src/styles', name), 'utf8');
  const reducedMotion = (source: string) =>
    [...source.matchAll(/@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/g)].map((m) => m[1]!).join('\n');

  it('rings and stills every pill group, and marks the chosen option with aria-pressed alone', () => {
    const analytics = sheet('_segment.scss');

    expect(analytics).toMatch(/\.oa-segment button:focus-visible,\s*\.oa-segmented-option:focus-visible,\s*\.oa-settings-tab:focus-visible \{[^}]*box-shadow:\s*0 0 0 3px/);
    expect(reducedMotion(analytics)).toMatch(/\.oa-segment button,\s*\.oa-settings-tab \{\s*transition: none;/);
    expect(sheet('_app.scss')).toMatch(/\.oa-segmented-option\[aria-pressed='true'\] \{/);
    expect(sheet('_app.scss')).not.toMatch(/\.oa-segmented-option\.active/);
  });

  it('draws the safe-mode switch from the shared switch rules', () => {
    const surfaces = sheet('_surfaces.scss');

    expect(surfaces).toMatch(/\.oa-menu-safe-mode \.oa-safe-switch input\[type="checkbox"\]:focus-visible \{[^}]*box-shadow/);
    expect(reducedMotion(surfaces)).toMatch(/\.oa-menu-safe-mode \.oa-safe-switch input\[type="checkbox"\]::after/);
    expect(sheet('_app.scss')).not.toMatch(/input\[type="checkbox"\]/);
  });

  it('keeps a menu from growing open under reduced motion', () => {
    const motion = reducedMotion(sheet('_workspace.scss'));

    expect(motion).toMatch(/\.oa-menu\.open,/);
    expect(motion).toMatch(/\.ai-model-pop\.open \{\s*transition: none;/);
  });

  it('rings the model popover rows the way a menu item is ringed', () => {
    expect(sheet('_chat.scss')).toMatch(/\.ai-pop-model-row:focus-visible \{[^}]*box-shadow:\s*inset 0 0 0 1px var\(--ai-primary\)/);
  });

  it('stills every scrim and the cards that rise in them', () => {
    const app = reducedMotion(sheet('_app.scss'));

    for (const name of ['oa-modal-overlay', 'oa-announce-overlay', 'oa-lightbox-overlay', 'oa-modal-card']) {
      expect(app, name).toMatch(new RegExp(`\\.${name}[,\\s]`));
    }
    expect(reducedMotion(sheet('_surfaces.scss'))).toMatch(/\.oa-lightbox-card \{\s*transition: none;/);
    expect(reducedMotion(sheet('_workbench.scss'))).toMatch(/\.oa-plugin-tab \{ transition: none; \}/);
  });

  it('keeps an armed clear-all button red under the pointer', () => {
    // Both rules are one class and one pseudo-class, so the later one wins.
    // The hover rule came second, and the grey hover fill covered the armed red.
    const chat = sheet('_chat.scss');
    const hover = chat.indexOf('.ai-chat-clear-all:hover {');

    expect(hover).toBeGreaterThan(-1);
    expect(chat.indexOf('.ai-chat-clear-all.armed {')).toBeGreaterThan(hover);
  });

  it('rings every icon control and link that takes keyboard focus', () => {
    // These reached focus with only the browser's outline. Each one has to
    // take the shared mixin, so the ring stays one token and one width.
    const everything = fs.readdirSync(path.resolve(__dirname, '../src/styles'))
      .filter((name) => name.endsWith('.scss'))
      .map((name) => sheet(name))
      .join('\n');
    const ringed = [
      '.oa-icon-btn', '.ai-chat-send', '.ai-chat-plus', '.ai-chat-list-more', '.ai-chat-mini-btn',
      '.ai-chat-setup-action', '.ai-chat-new', '.ai-chat-clear-all', '.ai-chat-attachment-remove',
      '.oa-reference-remove', '.oa-gallery-btn', '.oa-thread-remove', '.oa-toast-close',
      '.oa-menu-head-action', '.oa-admin-nav', '.oa-admin-subnav-item', '.oa-settings-rail-item',
      '.ai-project-action-btn', '.ai-history-new-btn', '.ai-code-btn', '.oa-btn', '.oa-chip-btn', '.oa-text-btn',
      '.oa-account-btn', '.ai-chat-list-open', '.ai-folder-open', '.ai-project-new-chat',
    ];

    for (const selector of ringed) {
      const literal = selector.replace(/\./g, '\\.');
      expect(everything, selector).toMatch(new RegExp(`${literal}:focus-visible[^{]*\\{\\s*@include mixins\\.focus-ring;`));
    }
    // The lightbox's close button nests its ring under the class.
    expect(sheet('_surfaces.scss')).toMatch(/\.oa-lightbox-close \{[\s\S]*?&:focus-visible \{\s*@include mixins\.focus-ring;/);
  });
});
