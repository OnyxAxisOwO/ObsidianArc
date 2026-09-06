// A zero-dependency LaTeX-to-MathML renderer for mathematical expressions.
//
// Modern browsers (Chrome 109+, Firefox, Safari, Edge) have full native
// MathML Core support. Rendering LaTeX directly to MathML elements avoids
// adding hundreds of kilobytes of third-party dependencies (KaTeX / MathJax)
// and web fonts, keeping the frontend bundle lightweight and self-contained.
//
// In accordance with repository conventions, this renderer never generates
// HTML strings and never assigns to innerHTML: all nodes are constructed
// directly via document.createElementNS and textContent.

const MATHML_NS = 'http://www.w3.org/1998/Math/MathML';

function m(tag: string, text?: string): Element {
  const el = document.createElementNS(MATHML_NS, tag);
  if (text !== undefined) el.textContent = text;
  return el;
}

const GREEK_LOWER: Record<string, string> = {
  alpha: 'α', beta: 'β', gamma: 'γ', delta: 'δ', epsilon: 'ε',
  varepsilon: 'ε', zeta: 'ζ', eta: 'η', theta: 'θ', vartheta: 'ϑ',
  iota: 'ι', kappa: 'κ', lambda: 'λ', mu: 'μ', nu: 'ν',
  xi: 'ξ', pi: 'π', varpi: 'ϖ', rho: 'ρ', varrho: 'ϱ',
  sigma: 'σ', varsigma: 'ς', tau: 'τ', upsilon: 'υ', phi: 'ϕ',
  varphi: 'φ', chi: 'χ', psi: 'ψ', omega: 'ω',
};

const GREEK_UPPER: Record<string, string> = {
  Gamma: 'Γ', Delta: 'Δ', Theta: 'Θ', Lambda: 'Λ', Xi: 'Ξ',
  Pi: 'Π', Sigma: 'Σ', Upsilon: 'Υ', Phi: 'Φ', Psi: 'Ψ', Omega: 'Ω',
};

const BIN_OPS: Record<string, string> = {
  pm: '±', mp: '∓', times: '×', div: '÷', cdot: '·', circ: '∘',
  bullet: '•', star: '⋆', ast: '*', cap: '∩', cup: '∪', vee: '∨',
  wedge: '∧', oplus: '⊕', otimes: '⊗', odot: '⊙', ominus: '⊖',
  setminus: '∖', wr: '≀', diamond: '⋄',
};

const REL_OPS: Record<string, string> = {
  le: '≤', leq: '≤', ge: '≥', geq: '≥', ne: '≠', neq: '≠',
  ll: '≪', gg: '≫', approx: '≈', sim: '∼', simeq: '≃', equiv: '≡',
  cong: '≅', propto: '∝', in: '∈', notin: '∉', ni: '∋', subset: '⊂',
  subseteq: '⊆', supset: '⊃', supseteq: '⊇', perp: '⊥', parallel: '∥',
  models: '⊨', vdash: '⊢', dashv: '⊣',
};

const ARROWS: Record<string, string> = {
  leftarrow: '←', gets: '←', rightarrow: '→', to: '→', leftrightarrow: '↔',
  Leftarrow: '⇐', Rightarrow: '⇒', implies: '⇒', Leftrightarrow: '⇔', iff: '⟺',
  uparrow: '↑', downarrow: '↓', updownarrow: '↕', Uparrow: '⇑', Downarrow: '⇓',
  nearrow: '↗', searrow: '↘', swarrow: '↙', nwarrow: '↖', mapsto: '↦',
};

const BIG_OPS: Record<string, string> = {
  sum: '∑', prod: '∏', coprod: '∐', int: '∫', iint: '∬', iiint: '∭',
  oint: '∮', bigcap: '⋂', bigcup: '⋃', bigwedge: '⋀', bigvee: '⋁',
  bigoplus: '⨁', bigotimes: '⨂',
};

const NAMED_FUNCS = new Set([
  'sin', 'cos', 'tan', 'arcsin', 'arccos', 'arctan', 'sinh', 'cosh', 'tanh',
  'cot', 'sec', 'csc', 'log', 'ln', 'lg', 'exp', 'lim', 'max', 'min',
  'sup', 'inf', 'det', 'gcd', 'deg', 'dim', 'ker', 'hom', 'arg',
]);

const MISC_SYMBOLS: Record<string, string> = {
  infty: '∞', partial: '∂', nabla: '∇', forall: '∀', exists: '∃',
  nexists: '∄', emptyset: '∅', varnothing: '∅', hbar: 'ℏ', ell: 'ℓ',
  Re: 'ℜ', Im: 'ℑ', top: '⊤', bot: '⊥', angle: '∠', triangle: '△',
  prime: '′', dots: '…', cdots: '⋯', ldots: '…', vdots: '⋮', ddots: '⋱',
};

const SPACES: Record<string, string> = {
  ',': '0.166em',
  ':': '0.222em',
  ';': '0.277em',
  ' ': '0.25em',
  quad: '1em',
  qquad: '2em',
  '!': '-0.166em',
};

const ACCENTS: Record<string, string> = {
  vec: '→',
  hat: '^',
  widehat: '^',
  bar: '¯',
  overline: '¯',
  underline: '_',
  dot: '˙',
  ddot: '¨',
  tilde: '~',
  widetilde: '~',
};

const BLACKBOARD_LETTERS: Record<string, string> = {
  R: 'ℝ', N: 'ℕ', Z: 'ℤ', Q: 'ℚ', C: 'ℂ', P: 'ℙ', H: 'ℍ', E: '𝔼',
};

const DELIMITER_MAP: Record<string, string> = {
  vert: '|',
  Vert: '∥',
  '|': '|',
  langle: '⟨',
  rangle: '⟩',
  lfloor: '⌊',
  rfloor: '⌋',
  lceil: '⌈',
  rceil: '⌉',
  '{': '{',
  '}': '}',
  lbrace: '{',
  rbrace: '}',
};

interface Token {
  type: 'cmd' | 'num' | 'ident' | 'op' | 'brace' | 'super' | 'sub' | 'amp' | 'newline' | 'text' | 'begin' | 'end' | 'eof';
  value: string;
  cmd?: string;
}

class Lexer {
  private pos = 0;

  constructor(private input: string) {}

  next(): Token {
    while (this.pos < this.input.length && /\s/.test(this.input[this.pos]!)) {
      this.pos++;
    }

    if (this.pos >= this.input.length) {
      return { type: 'eof', value: '' };
    }

    const char = this.input[this.pos]!;

    if (char === '\\') {
      this.pos++;
      if (this.pos >= this.input.length) return { type: 'op', value: '\\' };

      const nextChar = this.input[this.pos]!;
      if (nextChar === '\\') {
        this.pos++;
        return { type: 'newline', value: '\\\\' };
      }

      if (/[a-zA-Z]/.test(nextChar)) {
        let cmd = '';
        while (this.pos < this.input.length && /[a-zA-Z]/.test(this.input[this.pos]!)) {
          cmd += this.input[this.pos++];
        }
        if (cmd === 'text' || cmd === 'mathrm' || cmd === 'operatorname') {
          const text = this.readBracedText();
          return { type: 'text', value: text, cmd };
        }
        if (cmd === 'begin' || cmd === 'end') {
          const env = this.readBracedText().trim();
          return { type: cmd, value: env };
        }
        return { type: 'cmd', value: cmd };
      }

      // Single non-alphabetic escaped char, e.g. \, \{ \} \_ \% \&
      this.pos++;
      return { type: 'cmd', value: nextChar };
    }

    if (char === '{' || char === '}') {
      this.pos++;
      return { type: 'brace', value: char };
    }

    if (char === '^') {
      this.pos++;
      return { type: 'super', value: '^' };
    }

    if (char === '_') {
      this.pos++;
      return { type: 'sub', value: '_' };
    }

    if (char === '&') {
      this.pos++;
      return { type: 'amp', value: '&' };
    }

    if (/[0-9]/.test(char)) {
      let num = '';
      while (this.pos < this.input.length && /[0-9.]/.test(this.input[this.pos]!)) {
        num += this.input[this.pos++];
      }
      return { type: 'num', value: num };
    }

    if (/[a-zA-Z]/.test(char)) {
      this.pos++;
      return { type: 'ident', value: char };
    }

    this.pos++;
    return { type: 'op', value: char };
  }

  readBracedText(): string {
    while (this.pos < this.input.length && /\s/.test(this.input[this.pos]!)) {
      this.pos++;
    }
    if (this.pos >= this.input.length || this.input[this.pos] !== '{') {
      return '';
    }
    this.pos++; // consume '{'
    let depth = 1;
    let text = '';
    while (this.pos < this.input.length && depth > 0) {
      const c = this.input[this.pos]!;
      if (c === '{') {
        depth++;
        text += c;
      } else if (c === '}') {
        depth--;
        if (depth > 0) text += c;
      } else if (c === '\\' && this.input[this.pos + 1] === '{') {
        text += '{';
        this.pos++;
      } else if (c === '\\' && this.input[this.pos + 1] === '}') {
        text += '}';
        this.pos++;
      } else {
        text += c;
      }
      this.pos++;
    }
    return text;
  }
}

class Parser {
  private current: Token;

  constructor(private lexer: Lexer, private display: boolean) {
    this.current = this.lexer.next();
  }

  private peek(): Token {
    return this.current;
  }

  private consume(): Token {
    const t = this.current;
    this.current = this.lexer.next();
    return t;
  }

  parseAll(): Element {
    const row = m('mrow');
    while (this.peek().type !== 'eof') {
      const node = this.parseAtom();
      if (node) row.appendChild(node);
    }
    return row.children.length === 1 ? (row.firstElementChild as Element) : row;
  }

  private parseExpression(stopTokens: string[]): Element {
    const row = m('mrow');
    while (this.peek().type !== 'eof') {
      const token = this.peek();
      if (stopTokens.includes(token.value)) break;
      const node = this.parseAtom(stopTokens);
      if (node) row.appendChild(node);
    }
    return row.children.length === 1 ? (row.firstElementChild as Element) : row;
  }

  private parseArg(): Element {
    if (this.peek().type === 'brace' && this.peek().value === '{') {
      this.consume(); // '{'
      const expr = this.parseExpression(['}']);
      if (this.peek().value === '}') this.consume(); // '}'
      return expr;
    }
    // Single atom as argument, e.g. \frac 1 2 or x^2
    return this.parseSingleAtom() ?? m('mrow');
  }

  private parseSingleAtom(): Element | null {
    const token = this.consume();
    if (token.type === 'eof') return null;

    if (token.type === 'num') {
      return m('mn', token.value);
    }

    if (token.type === 'ident') {
      return m('mi', token.value);
    }

    if (token.type === 'cmd') {
      return this.handleCommand(token.value);
    }

    if (token.type === 'op') {
      return m('mo', token.value);
    }

    return m('mtext', token.value);
  }

  private parseAtom(stopTokens: string[] = []): Element | null {
    let base = this.parseBaseAtom(stopTokens);
    if (!base) return null;

    // Handle postfix sub / super scripts
    while (this.peek().type === 'super' || this.peek().type === 'sub' || this.peek().value === "'") {
      if (this.peek().value === "'") {
        this.consume();
        let primeCount = 1;
        while (this.peek().value === "'") {
          this.consume();
          primeCount++;
        }
        const primeSym = primeCount === 1 ? '′' : primeCount === 2 ? '″' : '‴';
        const supNode = m('mo', primeSym);
        const supElem = m('msup');
        supElem.appendChild(base);
        supElem.appendChild(supNode);
        base = supElem;
        continue;
      }

      let subNode: Element | null = null;
      let supNode: Element | null = null;

      if (this.peek().type === 'sub') {
        this.consume();
        subNode = this.parseArg();
        if (this.peek().type === 'super') {
          this.consume();
          supNode = this.parseArg();
        }
      } else if (this.peek().type === 'super') {
        this.consume();
        supNode = this.parseArg();
        if (this.peek().type === 'sub') {
          this.consume();
          subNode = this.parseArg();
        }
      }

      const isLargeOp = (base as Element).getAttribute('largeop') === 'true';

      if (subNode && supNode) {
        const tag = isLargeOp && this.display ? 'munderover' : 'msubsup';
        const elem = m(tag);
        elem.appendChild(base);
        elem.appendChild(subNode);
        elem.appendChild(supNode);
        base = elem;
      } else if (subNode) {
        const tag = isLargeOp && this.display ? 'munder' : 'msub';
        const elem = m(tag);
        elem.appendChild(base);
        elem.appendChild(subNode);
        base = elem;
      } else if (supNode) {
        const tag = isLargeOp && this.display ? 'mover' : 'msup';
        const elem = m(tag);
        elem.appendChild(base);
        elem.appendChild(supNode);
        base = elem;
      }
    }

    return base;
  }

  private parseBaseAtom(stopTokens: string[]): Element | null {
    const token = this.peek();

    if (token.type === 'eof' || stopTokens.includes(token.value)) {
      return null;
    }

    if (token.type === 'brace' && token.value === '{') {
      this.consume();
      const expr = this.parseExpression(['}']);
      if (this.peek().value === '}') this.consume();
      return expr;
    }

    if (token.type === 'num') {
      this.consume();
      return m('mn', token.value);
    }

    if (token.type === 'ident') {
      this.consume();
      return m('mi', token.value);
    }

    if (token.type === 'op') {
      this.consume();
      return m('mo', token.value);
    }

    if (token.type === 'text') {
      this.consume();
      const txt = m(token.cmd === 'text' ? 'mtext' : 'mi', token.value);
      if (token.cmd !== 'text') txt.setAttribute('mathvariant', 'normal');
      return txt;
    }

    if (token.type === 'begin') {
      this.consume();
      return this.parseEnvironment(token.value);
    }

    if (token.type === 'cmd') {
      this.consume();
      return this.handleCommand(token.value);
    }

    this.consume();
    return m('mtext', token.value);
  }

  private handleCommand(cmd: string): Element | null {
    // Greek letters
    if (cmd in GREEK_LOWER) return m('mi', GREEK_LOWER[cmd]);
    if (cmd in GREEK_UPPER) return m('mo', GREEK_UPPER[cmd]);

    // Operators and relations
    if (cmd in BIN_OPS) return m('mo', BIN_OPS[cmd]);
    if (cmd in REL_OPS) return m('mo', REL_OPS[cmd]);
    if (cmd in ARROWS) return m('mo', ARROWS[cmd]);

    // Big operators
    if (cmd in BIG_OPS) {
      const op = m('mo', BIG_OPS[cmd]);
      op.setAttribute('largeop', 'true');
      return op;
    }

    // Named functions
    if (NAMED_FUNCS.has(cmd)) {
      const fn = m('mi', cmd);
      fn.setAttribute('mathvariant', 'normal');
      if (['lim', 'max', 'min', 'sup', 'inf'].includes(cmd)) {
        fn.setAttribute('largeop', 'true');
      }
      return fn;
    }

    // Misc symbols
    if (cmd in MISC_SYMBOLS) return m('mo', MISC_SYMBOLS[cmd]);

    // Spacing
    if (cmd in SPACES) {
      const sp = m('mspace');
      sp.setAttribute('width', SPACES[cmd]!);
      return sp;
    }

    // Fractions
    if (cmd === 'frac' || cmd === 'dfrac' || cmd === 'tfrac') {
      const num = this.parseArg();
      const den = this.parseArg();
      const frac = m('mfrac');
      frac.appendChild(num);
      frac.appendChild(den);
      return frac;
    }

    // Binomial
    if (cmd === 'binom') {
      const n = this.parseArg();
      const k = this.parseArg();
      const frac = m('mfrac');
      frac.setAttribute('linethickness', '0');
      frac.appendChild(n);
      frac.appendChild(k);
      const row = m('mrow');
      row.appendChild(m('mo', '('));
      row.appendChild(frac);
      row.appendChild(m('mo', ')'));
      return row;
    }

    // Square root / N-th root
    if (cmd === 'sqrt') {
      let indexNode: Element | null = null;
      if (this.peek().value === '[') {
        this.consume(); // '['
        indexNode = this.parseExpression([']']);
        if (this.peek().value === ']') this.consume(); // ']'
      }
      const base = this.parseArg();
      if (indexNode) {
        const root = m('mroot');
        root.appendChild(base);
        root.appendChild(indexNode);
        return root;
      }
      const sqrt = m('msqrt');
      sqrt.appendChild(base);
      return sqrt;
    }

    // Font variants
    if (cmd === 'mathbf' || cmd === 'mathit' || cmd === 'mathbb' || cmd === 'mathcal' || cmd === 'mathsf' || cmd === 'mathtt') {
      const variantMap: Record<string, string> = {
        mathbf: 'bold',
        mathit: 'italic',
        mathbb: 'double-struck',
        mathcal: 'script',
        mathsf: 'sans-serif',
        mathtt: 'monospace',
      };
      const inner = this.parseArg();
      if (cmd === 'mathbb') {
        const text = inner.textContent;
        if (text && text in BLACKBOARD_LETTERS) {
          inner.textContent = BLACKBOARD_LETTERS[text]!;
          return inner;
        }
      }
      inner.setAttribute('mathvariant', variantMap[cmd]!);
      return inner;
    }

    // Delimiter escapes: \{ and \}
    if (cmd === '{' || cmd === '}') {
      return m('mo', cmd);
    }

    // Negations
    if (cmd === 'not') {
      const next = this.parseArg();
      if (next.textContent === '=') next.textContent = '≠';
      else if (next.textContent === '∈') next.textContent = '∉';
      return next;
    }

    // Modulo
    if (cmd === 'pmod') {
      const arg = this.parseArg();
      const row = m('mrow');
      row.appendChild(m('mo', '('));
      row.appendChild(m('mtext', 'mod'));
      const sp = m('mspace');
      sp.setAttribute('width', '0.333em');
      row.appendChild(sp);
      row.appendChild(arg);
      row.appendChild(m('mo', ')'));
      return row;
    }
    if (cmd === 'bmod') {
      return m('mo', 'mod');
    }

    // Accents
    if (cmd in ACCENTS) {
      const arg = this.parseArg();
      const isUnder = cmd === 'underline';
      const mover = m(isUnder ? 'munder' : 'mover');
      mover.setAttribute(isUnder ? 'accentunder' : 'accent', 'true');
      const accentMo = m('mo', ACCENTS[cmd]!);
      accentMo.setAttribute(isUnder ? 'accentunder' : 'accent', 'true');
      mover.appendChild(arg);
      mover.appendChild(accentMo);
      return mover;
    }

    // Sizing delimiters: \left and \right
    if (cmd === 'left' || cmd === 'right') {
      const next = this.peek();
      if (next.type !== 'eof') {
        this.consume();
        if (next.value === '.') {
          return null;
        }
        let sym = next.value;
        if (sym in DELIMITER_MAP) {
          sym = DELIMITER_MAP[sym]!;
        }
        const mo = m('mo', sym);
        mo.setAttribute('stretchy', 'true');
        return mo;
      }
      return null;
    }

    // Unrecognized command fallback
    return m('mi', `\\${cmd}`);
  }

  private parseEnvironment(env: string): Element {
    const table = m('mtable');
    let currentRow = m('mtr');
    let currentCell = m('mtd');

    // Skip column specification for \begin{array}{lcr}
    if (env === 'array' && this.peek().type === 'brace' && this.peek().value === '{') {
      this.consume();
      while (this.peek().type !== 'eof' && this.peek().value !== '}') {
        this.consume();
      }
      if (this.peek().value === '}') this.consume();
    }

    const flushCell = () => {
      currentRow.appendChild(currentCell);
      currentCell = m('mtd');
    };

    const flushRow = () => {
      flushCell();
      table.appendChild(currentRow);
      currentRow = m('mtr');
    };

    while (this.peek().type !== 'eof') {
      const token = this.peek();
      if (token.type === 'end') {
        this.consume();
        break;
      }

      if (token.type === 'amp') {
        this.consume();
        flushCell();
        continue;
      }

      if (token.type === 'newline') {
        this.consume();
        flushRow();
        continue;
      }

      const node = this.parseAtom();
      if (node) currentCell.appendChild(node);
    }

    if (currentCell.children.length > 0 || currentRow.children.length > 0) {
      flushRow();
    }

    // Fences for standard matrix environments
    const fences: Record<string, [string, string]> = {
      pmatrix: ['(', ')'],
      bmatrix: ['[', ']'],
      Bmatrix: ['{', '}'],
      vmatrix: ['|', '|'],
      Vmatrix: ['∥', '∥'],
      cases: ['{', ''],
    };

    if (env in fences) {
      const [open, close] = fences[env]!;
      const row = m('mrow');
      if (open) row.appendChild(m('mo', open));
      row.appendChild(table);
      if (close) row.appendChild(m('mo', close));
      return row;
    }

    return table;
  }
}

/**
 * Parses a LaTeX math string into a native MathML DOM element.
 * Safe, zero innerHTML, zero external libraries.
 */
export function renderMath(latex: string, display = false): HTMLElement {
  const container = document.createElement(display ? 'div' : 'span');
  container.className = display ? 'ai-math ai-math-block' : 'ai-math ai-math-inline';

  try {
    const lexer = new Lexer(latex.trim());
    const parser = new Parser(lexer, display);
    const math = m('math');
    math.setAttribute('display', display ? 'block' : 'inline');
    math.appendChild(parser.parseAll());
    container.appendChild(math);
  } catch {
    // If parsing fails for any reason, fall back to literal representation
    // rather than throwing or breaking message rendering.
    container.textContent = latex;
  }

  return container;
}

export function renderMathInline(latex: string, display = false): HTMLElement {
  return renderMath(latex, display);
}

export function renderMathBlock(latex: string): HTMLElement {
  return renderMath(latex, true);
}
