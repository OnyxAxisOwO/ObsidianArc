// A small GitHub-flavoured-Markdown renderer for the transcript.
//
// It exists instead of a library because the input is model output — text a
// prompt-injected document or a hostile page could have influenced — and
// because a renderer is one of the few places where a dependency's next
// version can turn into an XSS bug in this one.
//
// The property that matters: it never produces an HTML string. parse() turns
// markdown into a plain token tree, render() walks that tree building
// elements, and every piece of text lands in textContent. There is no
// innerHTML path for a payload to travel down, which is not something a
// regex-based markdown-to-HTML converter can offer.
//
// Two deliberate narrowings versus real GFM:
//   - Images render as a link, never an <img>. A model that emitted
//     ![](https://tracker/x.png) would otherwise turn every reader of the
//     transcript into a beacon hit.
//   - Only http(s) and mailto links survive. Everything else — javascript:,
//     data:, bare relative paths that mean nothing here — renders as its own
//     literal text.

import { t } from '../i18n';
import { IconCheck, IconCopy, IconDownload, iconElement, type OaIcon } from '../icons';
import { describeCode, type CodeLanguage } from './code';

// The LaTeX renderer is a fifth of this file's weight and most conversations
// never contain a formula, so it arrives as a chunk of its own, requested by
// the first math node anyone actually renders.
//
// Until it lands, a formula renders as its own source text — which is exactly
// what math.ts falls back to for a formula it cannot parse — and every element
// that drew one is painted again once it is here. renderInto is the only way
// in, so remembering which elements those were costs one map entry for the
// length of a single round trip.
type MathRenderer = typeof import('./math');

let math: MathRenderer | null = null;
let arriving: Promise<void> | null = null;
const awaitingMath = new Map<Element, string>();
let drewPlaceholder = false;

function mathRenderer(): MathRenderer | null {
  if (math) return math;
  arriving ??= import('./math')
    .then((module) => {
      math = module;
      const pending = [...awaitingMath];
      awaitingMath.clear();
      for (const [element, text] of pending) renderInto(element, text);
    })
    // A chunk that did not arrive leaves the formula as its own source text,
    // which is readable. Clearing the handle either way is what lets the next
    // formula ask again, instead of one failed fetch deciding for the rest of
    // the page's life.
    .catch(() => undefined)
    .finally(() => {
      arriving = null;
    });
  return null;
}

function mathSource(latex: string, display: boolean): HTMLElement {
  drewPlaceholder = true;
  const node = document.createElement(display ? 'div' : 'span');
  node.textContent = latex;
  return node;
}

const ESCAPABLE = '\\`*_{}[]()#+-.!~>|$';
const FENCE_RE = /^ {0,3}(`{3,}|~{3,})[ \t]*([^`\s]*)[ \t]*$/;
const MATH_BLOCK_FENCE = /^ {0,3}(?:\$\$|\\\[)[ \t]*$/;
const MATH_BLOCK_CLOSE = /^ {0,3}(?:\$\$|\\\])[ \t]*$/;
const MATH_SINGLE_RE = /^ {0,3}(?:\$\$([^\n]+?)\$\$|\\\[([^\n]+?)\\\])[ \t]*$/;
const HR_RE = /^ {0,3}([-*_])[ \t]*(?:\1[ \t]*){2,}$/;
const HEADING_RE = /^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$/;
const QUOTE_RE = /^ {0,3}> ?(.*)$/;
const ITEM_RE = /^(\s*)([-*+]|\d{1,9}[.)])[ \t]+(.*)$/;
const TABLE_DIVIDER_RE = /^ {0,3}\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;
const SAFE_LANG_RE = /^[a-z0-9][a-z0-9+#-]{0,19}$/i;

type Inline =
  | { type: 'text'; value: string }
  | { type: 'br' }
  | { type: 'codespan'; value: string }
  | { type: 'link'; href: string; children: Inline[] }
  | { type: 'strong' | 'em' | 'del' | 'span'; children: Inline[] }
  | { type: 'math'; value: string; display: boolean };

type Block =
  | { type: 'paragraph'; children: Inline[] }
  | { type: 'heading'; level: number; children: Inline[] }
  | { type: 'code'; lang: string; text: string }
  | { type: 'blockquote'; children: Block[] }
  | { type: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { type: 'table'; align: string[]; header: Inline[][]; rows: Inline[][][] }
  | { type: 'hr' }
  | { type: 'math'; text: string };

// --- inline ------------------------------------------------------------------

export function safeHref(value: string): string | null {
  const href = String(value ?? '').trim();
  // Anything with a scheme that is not one of these two, and anything with no
  // scheme at all, is not something this interface can usefully open.
  return /^(?:https?:\/\/|mailto:)[^\s]+$/i.test(href) ? href : null;
}

function matchLink(text: string): { length: number; node: Inline } | null {
  const match = /^(!?)\[((?:[^[\]\\]|\\.)*)\]\([ \t]*<?([^\s<>)]*)>?(?:[ \t]+"[^"]*")?[ \t]*\)/.exec(text);
  if (!match) return null;

  const label = (match[2] ?? '').replace(/\\(.)/g, '$1');
  const href = safeHref(match[3] ?? '');
  const children = parseInline(label);
  const kids = children.length ? children : [{ type: 'text' as const, value: label }];

  // An image keeps its alt text and becomes an ordinary link; a link whose
  // target was rejected keeps its label and becomes plain text.
  return {
    length: match[0].length,
    node: href ? { type: 'link', href, children: kids } : { type: 'span', children: kids },
  };
}

function isWordChar(char: string | undefined): boolean {
  return !!char && /[\w一-鿿]/.test(char);
}

export function parseInline(text: string): Inline[] {
  const source = String(text ?? '');
  const nodes: Inline[] = [];
  let buffer = '';
  let index = 0;

  const flush = () => {
    if (buffer) {
      nodes.push({ type: 'text', value: buffer });
      buffer = '';
    }
  };
  const push = (node: Inline) => {
    flush();
    nodes.push(node);
  };

  while (index < source.length) {
    const char = source[index]!;
    const rest = source.slice(index);

    if (rest.startsWith('\\(')) {
      const mathParen = /^\\\(((?:[^\\]|\\.)+?)\\\)/.exec(rest);
      if (mathParen) {
        push({ type: 'math', value: mathParen[1]!.trim(), display: false });
        index += mathParen[0].length;
        continue;
      }
    }

    if (char === '\\' && ESCAPABLE.includes(source[index + 1] ?? '')) {
      buffer += source[index + 1];
      index += 2;
      continue;
    }

    if (char === '\n') {
      push({ type: 'br' });
      index += 1;
      continue;
    }

    if (char === '`') {
      // The longest run of backticks opens the span, so `` ` `` works.
      const code = /^(`+)([\s\S]*?[^`])\1(?!`)/.exec(rest);
      if (code) {
        push({ type: 'codespan', value: (code[2] ?? '').replace(/^ ([\s\S]*) $/, '$1') });
        index += code[0].length;
        continue;
      }
    }

    if (char === '[' || (char === '!' && source[index + 1] === '[')) {
      const link = matchLink(rest);
      if (link) {
        push(link.node);
        index += link.length;
        continue;
      }
    }

    if (char === '<') {
      const auto = /^<((?:https?:\/\/|mailto:)[^>\s]+)>/i.exec(rest);
      if (auto) {
        const href = auto[1]!;
        push({ type: 'link', href, children: [{ type: 'text', value: href }] });
        index += auto[0].length;
        continue;
      }
    }

    if (char === '~' && source[index + 1] === '~') {
      const del = /^~~(?=\S)([\s\S]*?\S)~~/.exec(rest);
      if (del) {
        push({ type: 'del', children: parseInline(del[1] ?? '') });
        index += del[0].length;
        continue;
      }
    }

    // An underscore inside a word is snake_case, not emphasis — the single
    // most common false positive when a model talks about code or CSS.
    if ((char === '*' || char === '_') && !(char === '_' && isWordChar(source[index - 1]))) {
      const marker = char === '*' ? '\\*' : '_';
      const strong = new RegExp(`^${marker}${marker}(?=\\S)([\\s\\S]*?\\S)${marker}${marker}`).exec(rest);
      if (strong) {
        push({ type: 'strong', children: parseInline(strong[1] ?? '') });
        index += strong[0].length;
        continue;
      }
      const em = new RegExp(`^${marker}(?=\\S)([\\s\\S]*?\\S)${marker}(?!${marker})`).exec(rest);
      if (em && !(char === '_' && isWordChar(source[index + em[0].length]))) {
        push({ type: 'em', children: parseInline(em[1] ?? '') });
        index += em[0].length;
        continue;
      }
    }

    if (rest.startsWith('$$')) {
      const mathBlock = /^\$\$((?:[^\\]|\\.)+?)\$\$/.exec(rest);
      if (mathBlock) {
        push({ type: 'math', value: mathBlock[1]!.trim(), display: true });
        index += mathBlock[0].length;
        continue;
      }
    }

    if (char === '$') {
      const mathInline = /^\$((?!\s)(?:[^\$\\\n]|\\.)*?(?<!\s))\$(?!\d)/.exec(rest);
      if (mathInline && mathInline[1]) {
        push({ type: 'math', value: mathInline[1], display: false });
        index += mathInline[0].length;
        continue;
      }
    }

    buffer += char;
    index += 1;
  }

  flush();
  return nodes;
}

// --- blocks -------------------------------------------------------------------

function splitRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split(/(?<!\\)\|/)
    .map((cell) => cell.trim().replace(/\\\|/g, '|'));
}

function columnAlign(cell: string): string {
  const start = cell.startsWith(':');
  const end = cell.endsWith(':');
  if (start && end) return 'center';
  if (end) return 'right';
  if (start) return 'left';
  return '';
}

export function parse(text: string): Block[] {
  const lines = String(text ?? '').replace(/\r\n?/g, '\n').split('\n');
  const blocks: Block[] = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index]!;

    if (!line.trim()) {
      index += 1;
      continue;
    }

    const fence = FENCE_RE.exec(line);
    if (fence) {
      const marker = fence[1]![0] === '`' ? '`' : '~';
      const closing = new RegExp(`^ {0,3}${marker}{${fence[1]!.length},}[ \\t]*$`);
      const body: string[] = [];
      index += 1;
      while (index < lines.length && !closing.test(lines[index]!)) {
        body.push(lines[index]!);
        index += 1;
      }
      index += 1; // the closing fence, or the end of the input
      blocks.push({ type: 'code', lang: fence[2] ?? '', text: body.join('\n') });
      continue;
    }

    const mathSingle = MATH_SINGLE_RE.exec(line);
    if (mathSingle) {
      const content = (mathSingle[1] ?? mathSingle[2] ?? '').trim();
      blocks.push({ type: 'math', text: content });
      index += 1;
      continue;
    }

    if (MATH_BLOCK_FENCE.test(line)) {
      const body: string[] = [];
      index += 1;
      while (index < lines.length && !MATH_BLOCK_CLOSE.test(lines[index]!)) {
        body.push(lines[index]!);
        index += 1;
      }
      index += 1; // the closing fence, or the end of the input
      blocks.push({ type: 'math', text: body.join('\n') });
      continue;
    }

    if (HR_RE.test(line)) {
      blocks.push({ type: 'hr' });
      index += 1;
      continue;
    }

    const heading = HEADING_RE.exec(line);
    if (heading) {
      blocks.push({ type: 'heading', level: heading[1]!.length, children: parseInline(heading[2] ?? '') });
      index += 1;
      continue;
    }

    if (QUOTE_RE.test(line)) {
      const body: string[] = [];
      while (index < lines.length && (QUOTE_RE.test(lines[index]!) || (body.length > 0 && lines[index]!.trim()))) {
        const quoted = QUOTE_RE.exec(lines[index]!);
        body.push(quoted ? quoted[1]! : lines[index]!);
        index += 1;
      }
      blocks.push({ type: 'blockquote', children: parse(body.join('\n')) });
      continue;
    }

    // A table needs its delimiter row to be the very next line; without it
    // the pipes are just pipes in a paragraph.
    const next = lines[index + 1];
    if (line.includes('|') && next !== undefined && TABLE_DIVIDER_RE.test(next) && next.includes('-')) {
      const header = splitRow(line);
      const align = splitRow(next).map(columnAlign);
      index += 2;
      const rows: string[][] = [];
      while (index < lines.length && lines[index]!.trim() && lines[index]!.includes('|')) {
        rows.push(splitRow(lines[index]!));
        index += 1;
      }
      blocks.push({
        type: 'table',
        align,
        header: header.map(parseInline),
        rows: rows.map((row) => row.map(parseInline)),
      });
      continue;
    }

    const item = ITEM_RE.exec(line);
    if (item) {
      const ordered = /\d/.test(item[2]!);
      const start = ordered ? parseInt(item[2]!, 10) : 1;
      const baseIndent = item[1]!.length;
      const items: Block[][] = [];

      while (index < lines.length) {
        const entry = ITEM_RE.exec(lines[index]!);
        if (!entry || entry[1]!.length > baseIndent + 1 || /\d/.test(entry[2]!) !== ordered) break;

        const body = [entry[3] ?? ''];
        index += 1;
        // Continuation lines: the item's own wrapped text, plus anything
        // indented under its marker — which is how a nested list stays nested
        // instead of flattening into its parent.
        while (index < lines.length && lines[index]!.trim() && !FENCE_RE.test(lines[index]!)) {
          const nested = ITEM_RE.exec(lines[index]!);
          if (nested && nested[1]!.length <= baseIndent + 1) break;
          body.push(lines[index]!.replace(/^ {1,4}/, ''));
          index += 1;
        }
        items.push(parse(body.join('\n')));
      }
      blocks.push({ type: 'list', ordered, start, items });
      continue;
    }

    const paragraph: string[] = [];
    while (
      index < lines.length &&
      lines[index]!.trim() &&
      !FENCE_RE.test(lines[index]!) &&
      !MATH_SINGLE_RE.test(lines[index]!) &&
      !MATH_BLOCK_FENCE.test(lines[index]!) &&
      !HR_RE.test(lines[index]!) &&
      !HEADING_RE.test(lines[index]!) &&
      !QUOTE_RE.test(lines[index]!) &&
      !ITEM_RE.test(lines[index]!)
    ) {
      paragraph.push(lines[index]!.trim());
      index += 1;
    }
    blocks.push({ type: 'paragraph', children: parseInline(paragraph.join('\n')) });
  }

  return blocks;
}

// --- rendering ------------------------------------------------------------------

function renderInline(nodes: Inline[], parent: Node): void {
  for (const node of nodes) {
    switch (node.type) {
      case 'text':
        parent.appendChild(document.createTextNode(node.value));
        break;
      case 'br':
        parent.appendChild(document.createElement('br'));
        break;
      case 'codespan': {
        const code = document.createElement('code');
        code.textContent = node.value;
        parent.appendChild(code);
        break;
      }
      case 'math': {
        const renderer = mathRenderer();
        parent.appendChild(renderer
          ? renderer.renderMathInline(node.value, node.display)
          : mathSource(node.value, node.display));
        break;
      }
      case 'link': {
        const link = document.createElement('a');
        link.href = node.href;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        renderInline(node.children, link);
        parent.appendChild(link);
        break;
      }
      default: {
        const element = document.createElement(node.type === 'span' ? 'span' : node.type);
        renderInline(node.children, element);
        parent.appendChild(element);
      }
    }
  }
}

// Long enough for the word to be read, short enough that a reader copying two
// blocks in a row is not left wondering which one the label belongs to.
const COPIED_SHOWN_MS = 1500;

/**
 * Writes text to the clipboard, reporting whether it landed.
 *
 * It lives here, next to the code block that needed it first, rather than in
 * a file of its own: the transcript's own copy buttons call it too, and one
 * implementation that both share is the point — two would drift apart on the
 * fallback, which is the half nobody exercises.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // navigator.clipboard does not exist on a plain-http origin and its
    // permission can be refused outright, so without the path below the copy
    // control would silently do nothing on exactly the deployments that have
    // no TLS in front of them.
  }

  const staging = document.createElement('textarea');
  staging.value = text;
  staging.readOnly = true;
  // Off-screen rather than hidden: execCommand copies a selection, and an
  // element that is not rendered cannot hold one. Fixed positioning keeps the
  // page from scrolling to it.
  staging.style.position = 'fixed';
  staging.style.top = '-1000px';
  staging.style.opacity = '0';
  document.body.appendChild(staging);
  try {
    staging.select();
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    staging.remove();
  }
}

/** A round icon button for the header above a code block. */
function codeControl(icon: OaIcon, label: string, onClick: () => void): HTMLButtonElement {
  const control = document.createElement('button');
  control.type = 'button';
  control.className = 'ai-code-btn';
  control.title = label;
  control.setAttribute('aria-label', label);
  control.appendChild(iconElement(icon, 15));
  control.addEventListener('click', onClick);
  return control;
}

function copyControl(text: string): HTMLButtonElement {
  // renderInto rebuilds the transcript on every streamed delta, so this button
  // is thrown away and made again constantly. Everything it owns — the
  // listener and the revert timer — is held on the element itself; nothing is
  // registered on document, and a detached button's pending timer only ever
  // writes to the node that is already gone.
  let revert = 0;
  const control = codeControl(IconCopy, t('copy'), () => {
    void copyToClipboard(text).then((copied) => {
      if (!copied) return;
      // The glyph is the whole message now that the control is an icon: a
      // tick where the two sheets were says it landed without the header
      // changing width mid-read.
      control.replaceChildren(iconElement(IconCheck, 15));
      control.classList.add('copied');
      control.title = t('copied');
      window.clearTimeout(revert);
      revert = window.setTimeout(() => {
        control.replaceChildren(iconElement(IconCopy, 15));
        control.classList.remove('copied');
        control.title = t('copy');
      }, COPIED_SHOWN_MS);
    });
  });
  return control;
}

/**
 * Hands the reader the block as a file, built in their own browser.
 *
 * A blob and an object URL rather than a round trip: the text is already on
 * screen, so asking the server to send it back would be a request that can
 * only fail. The URL is released on the next task — revoking it in the same
 * one races the download the click just started.
 */
function saveText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  // Attached because a link that is not in the document does not reliably
  // receive a synthetic click in every engine.
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

/**
 * The bar over a fenced block: what it is written in, and the two things a
 * reader wants to do with it.
 *
 * The language is the fence's own tag when the model wrote one and a guess
 * from the source when it did not, which is most of the time — see
 * `chat/code.ts`.
 */
function codeHead(language: CodeLanguage, text: string): HTMLDivElement {
  const head = document.createElement('div');
  head.className = 'ai-code-head';

  const name = document.createElement('span');
  name.className = 'ai-code-lang';
  name.textContent = language.label;
  head.appendChild(name);

  const actions = document.createElement('span');
  actions.className = 'ai-code-actions';
  actions.appendChild(codeControl(IconDownload, t('download'), () => {
    saveText(`snippet.${language.extension}`, text);
  }));
  actions.appendChild(copyControl(text));
  head.appendChild(actions);
  return head;
}

function renderBlock(block: Block): Node {
  switch (block.type) {
    case 'hr':
      return document.createElement('hr');

    case 'math': {
      const renderer = mathRenderer();
      return renderer ? renderer.renderMathBlock(block.text) : mathSource(block.text, true);
    }

    case 'heading': {
      const level = Math.min(6, Math.max(1, block.level));
      const heading = document.createElement(`h${level}`);
      renderInline(block.children, heading);
      return heading;
    }

    case 'code': {
      const pre = document.createElement('pre');
      const code = document.createElement('code');
      code.textContent = block.text;
      const tag = SAFE_LANG_RE.test(block.lang) ? block.lang.toLowerCase() : '';
      const language = describeCode(tag, block.text);
      // The class still carries the fence's own tag rather than a guess: it is
      // what a highlighter would key off, and naming a language the author
      // never claimed is a stronger statement than a label on a header.
      if (tag) code.className = `language-${tag}`;
      pre.appendChild(code);

      // The wrapper holds the header as well as the <pre>: the <pre> scrolls
      // horizontally, and controls inside it would slide away with the long
      // line the reader scrolled to see.
      const wrap = document.createElement('div');
      wrap.className = 'ai-code';
      wrap.appendChild(codeHead(language, block.text));
      wrap.appendChild(pre);
      return wrap;
    }

    case 'blockquote': {
      const quote = document.createElement('blockquote');
      for (const child of block.children) quote.appendChild(renderBlock(child));
      return quote;
    }

    case 'list': {
      const list = document.createElement(block.ordered ? 'ol' : 'ul');
      if (block.ordered && block.start !== 1) (list as HTMLOListElement).start = block.start;
      for (const item of block.items) {
        const li = document.createElement('li');
        item.forEach((child, position) => {
          // A one-paragraph item renders inline, which is what keeps a short
          // bullet list tight instead of double-spaced.
          if (position === 0 && child.type === 'paragraph' && item.length === 1) {
            renderInline(child.children, li);
            return;
          }
          li.appendChild(renderBlock(child));
        });
        list.appendChild(li);
      }
      return list;
    }

    case 'table': {
      const table = document.createElement('table');
      const thead = document.createElement('thead');
      const headRow = document.createElement('tr');
      block.header.forEach((cell, column) => {
        const th = document.createElement('th');
        const align = block.align[column];
        if (align) th.style.textAlign = align;
        renderInline(cell, th);
        headRow.appendChild(th);
      });
      thead.appendChild(headRow);
      table.appendChild(thead);

      const tbody = document.createElement('tbody');
      for (const row of block.rows) {
        const tr = document.createElement('tr');
        row.forEach((cell, column) => {
          const td = document.createElement('td');
          const align = block.align[column];
          if (align) td.style.textAlign = align;
          renderInline(cell, td);
          tr.appendChild(td);
        });
        tbody.appendChild(tr);
      }
      table.appendChild(tbody);
      return table;
    }

    default: {
      const paragraph = document.createElement('p');
      renderInline(block.children, paragraph);
      return paragraph;
    }
  }
}

export function render(text: string): DocumentFragment {
  const fragment = document.createDocumentFragment();
  for (const block of parse(text)) fragment.appendChild(renderBlock(block));
  return fragment;
}

// Replaces an element's contents. textContent = '' rather than innerHTML = ''
// for the same reason the renderer avoids innerHTML everywhere else.
export function renderInto(element: Element, text: string): Element {
  element.textContent = '';
  drewPlaceholder = false;
  element.appendChild(render(text));
  // Only while the renderer is still in flight. Once it is here nothing draws
  // a placeholder again, so the map empties and stays empty.
  if (drewPlaceholder) awaitingMath.set(element, text);
  else awaitingMath.delete(element);
  return element;
}
