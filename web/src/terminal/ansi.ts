// ANSI SGR -> token array, for rendering console output without `innerHTML`.
//
// The stream arrives one SSE `out` chunk at a time, which is an arbitrary
// byte boundary with no relation to an escape sequence's length — a
// `\x1b[3` can land at the very end of one chunk and `1m` at the start of
// the next. Losing that seam would either print the raw escape bytes as text
// or drop the colour it was about to set, so the tokeniser carries an
// unfinished sequence forward as state rather than assuming a chunk is
// self-contained.

const ESC = '\x1b';

type AnsiTokenKind = 'text' | 'clear';

/**
 * One piece of output. `kind: 'clear'` carries no text — it means "the pane
 * should wipe what it has drawn so far", for `\x1b[2J` / `\x1b[H` / `\x1b[3J`
 * (what `watch` writes between frames). Everything else is `'text'`, with
 * `cls` the CSS classes the accumulated SGR state maps to; a consumer that
 * only wants the simple case can still do `<span :class="tok.cls">{{ tok.text }}</span>`
 * and skip `kind === 'clear'` tokens.
 */
export interface AnsiToken {
  kind: AnsiTokenKind;
  text: string;
  cls: string[];
}

interface SgrState {
  bold: boolean;
  dim: boolean;
  underline: boolean;
  inverse: boolean;
  /** 30-37 or 90-97; null is the default foreground (SGR 0 or 39). */
  fg: number | null;
}

function defaultSgr(): SgrState {
  return { bold: false, dim: false, underline: false, inverse: false, fg: null };
}

function classesFor(state: SgrState): string[] {
  const cls: string[] = [];
  if (state.bold) cls.push('ansi-bold');
  if (state.dim) cls.push('ansi-dim');
  if (state.underline) cls.push('ansi-underline');
  if (state.inverse) cls.push('ansi-inverse');
  if (state.fg !== null) cls.push(`ansi-fg-${state.fg}`);
  return cls;
}

/**
 * Applies one already-split SGR parameter, in place. Anything render.go
 * never emits — 256-colour, truecolor, blink, strike — is dropped rather
 * than guessed at: an unrecognised code must never reach a class name the
 * stylesheet has no rule for.
 */
function applySgrParam(state: SgrState, param: number): void {
  switch (true) {
    case param === 0:
      Object.assign(state, defaultSgr());
      return;
    case param === 1:
      state.bold = true;
      return;
    case param === 2:
      state.dim = true;
      return;
    case param === 4:
      state.underline = true;
      return;
    case param === 7:
      state.inverse = true;
      return;
    case param === 22: // "normal intensity" cancels both bold and dim
      state.bold = false;
      state.dim = false;
      return;
    case param === 24:
      state.underline = false;
      return;
    case param === 27:
      state.inverse = false;
      return;
    case param === 39:
      state.fg = null;
      return;
    case param >= 30 && param <= 37:
    case param >= 90 && param <= 97:
      state.fg = param;
      return;
    default:
      return; // unknown: swallowed, never printed
  }
}

/** A CSI final byte is 0x40-0x7E; everything between `[` and it (digits, `;`, `?`) is a parameter byte, never a final one, so this scan cannot stop early on them. */
function isFinalByte(ch: string): boolean {
  const code = ch.charCodeAt(0);
  return code >= 0x40 && code <= 0x7e;
}

interface AnsiTokenizerState {
  sgr: SgrState;
  /** Bytes of an escape sequence seen so far but not yet resolved — from the `ESC` on, verbatim. Carried whole into the next chunk. */
  pending: string;
}

export function initialAnsiState(): AnsiTokenizerState {
  return { sgr: defaultSgr(), pending: '' };
}

/**
 * Tokenises one chunk against the state left over from the previous one.
 * Pure — mutates neither argument — so a caller can replay or fork a stream
 * for a test without the tokeniser class in the way.
 */
export function tokenizeAnsiChunk(
  chunk: string,
  state: AnsiTokenizerState,
): { tokens: AnsiToken[]; state: AnsiTokenizerState } {
  const input = state.pending + chunk;
  const sgr: SgrState = { ...state.sgr };
  const tokens: AnsiToken[] = [];
  let textBuf = '';
  let pendingOut = '';

  const flushText = (): void => {
    if (textBuf) {
      tokens.push({ kind: 'text', text: textBuf, cls: classesFor(sgr) });
      textBuf = '';
    }
  };

  let i = 0;
  while (i < input.length) {
    const ch = input[i];
    if (ch !== ESC) {
      textBuf += ch;
      i += 1;
      continue;
    }

    // The flush is deliberately *not* here. Flushing on every ESC would
    // split the surrounding text into extra tokens around a sequence that
    // turns out to be unknown and gets swallowed — visually identical once
    // rendered, but it turns one span into two for nothing. Each branch
    // below flushes for itself, exactly when it is about to change what the
    // next token's classes are or emit a token of its own; the "unknown,
    // swallow" fallthrough at the bottom does not, so surrounding text
    // stays one token.

    if (i + 1 >= input.length) {
      // A lone ESC at the very end: cannot even tell if it starts a CSI
      // sequence yet. Carry it whole.
      flushText();
      pendingOut = input.slice(i);
      i = input.length;
      break;
    }

    if (input[i + 1] !== '[') {
      // A non-CSI escape (ESC followed by one plain byte, e.g. cursor-save).
      // render.go never emits these; swallow the two bytes and move on.
      i += 2;
      continue;
    }

    let j = i + 2;
    while (j < input.length && !isFinalByte(input[j]!)) j += 1;

    if (j >= input.length) {
      // The final byte has not arrived yet — this is the split this module
      // exists to survive. Carry the whole ESC..end span forward untouched.
      flushText();
      pendingOut = input.slice(i);
      i = input.length;
      break;
    }

    const finalByte = input[j];
    const params = input.slice(i + 2, j);

    if (finalByte === 'm') {
      flushText(); // text already seen keeps the old classes
      const parts = params.length ? params.split(';') : ['0'];
      for (let k = 0; k < parts.length; k += 1) {
        const n = parts[k] === '' ? 0 : Number(parts[k]);
        if (!Number.isFinite(n)) continue;
        // 38 and 48 open an extended colour: `5;n` (256-colour) or `2;r;g;b`
        // (truecolor). Those arguments are values, not SGR codes, so they are
        // read past here. Without this, `38;5;7` switched inverse on and
        // `38;2;30;30;30` switched dim on and picked a foreground.
        if (n === 38 || n === 48) {
          const mode = Number(parts[k + 1]);
          if (mode === 5) k += 2;
          else if (mode === 2) k += 4;
          continue;
        }
        applySgrParam(sgr, n);
      }
    } else if ((finalByte === 'J' && (params === '2' || params === '3')) || (finalByte === 'H' && params === '')) {
      flushText();
      tokens.push({ kind: 'clear', text: '', cls: [] });
    }
    // Any other CSI final byte (cursor moves, private modes, …) is unknown
    // and swallowed — never printed as literal escape bytes, and no flush,
    // so it never fragments the text around it.

    i = j + 1;
  }

  flushText();
  return { tokens, state: { sgr, pending: pendingOut } };
}

/**
 * Holds tokeniser state across an arbitrary number of chunks, for a caller
 * that just wants to feed SSE `out` text in and get tokens out — one
 * instance per running command, since SGR state is scoped to one command's
 * output the way a real terminal's is scoped to one screen.
 */
interface AnsiTokenizer {
  push(chunk: string): AnsiToken[];
  reset(): void;
}

export function createAnsiTokenizer(): AnsiTokenizer {
  let state = initialAnsiState();
  return {
    push(chunk: string): AnsiToken[] {
      const result = tokenizeAnsiChunk(chunk, state);
      state = result.state;
      return result.tokens;
    },
    reset(): void {
      state = initialAnsiState();
    },
  };
}
