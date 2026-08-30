// A small GitHub-flavoured-markdown renderer for the chat transcript.
//
// It exists instead of a library because this project ships no build step and
// no third-party runtime beyond an optional syntax highlighter, and because
// the input is model output — text a hostile page or prompt-injected document
// could have influenced. So the renderer never produces an HTML string:
// parse() turns markdown into a plain token tree and render() walks that tree
// building elements, with every piece of text landing in textContent. There
// is no innerHTML path for a payload to travel down, which is a property a
// regex-based markdown-to-HTML converter cannot offer.
//
// Two deliberate narrowings versus real GFM:
// - Images render as a link, never an <img>. A model that emitted
//   `![](https://tracker/x.png)` would otherwise turn every reader of the
//   transcript into a beacon hit.
// - Only http(s) and mailto links survive; everything else (javascript:,
//   data:, bare relative paths that mean nothing in a chat transcript)
//   renders as its own literal text.

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.ObsidianMarkdown = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const ESCAPABLE = '\\`*_{}[]()#+-.!~>|';
  const FENCE_RE = /^ {0,3}(`{3,}|~{3,})[ \t]*([^`\s]*)[ \t]*$/;
  const HR_RE = /^ {0,3}([-*_])[ \t]*(?:\1[ \t]*){2,}$/;
  const HEADING_RE = /^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$/;
  const QUOTE_RE = /^ {0,3}> ?(.*)$/;
  const ITEM_RE = /^(\s*)([-*+]|\d{1,9}[.)])[ \t]+(.*)$/;
  const TABLE_DIVIDER_RE = /^ {0,3}\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$/;
  const SAFE_LANG_RE = /^[a-z0-9][a-z0-9+#-]{0,19}$/i;

  // --- inline ---------------------------------------------------------------

  function safeHref(value) {
    const href = String(value || '').trim();
    // Anything with a scheme that is not one of these two, and anything
    // without a scheme at all, is not something this UI can usefully open.
    return /^(?:https?:\/\/|mailto:)[^\s]+$/i.test(href) ? href : null;
  }

  function matchLink(text) {
    const match = /^(!?)\[((?:[^[\]\\]|\\.)*)\]\([ \t]*<?([^\s<>)]*)>?(?:[ \t]+"[^"]*")?[ \t]*\)/.exec(text);
    if (!match) return null;
    const label = match[2].replace(/\\(.)/g, '$1');
    const href = safeHref(match[3]);
    const children = parseInline(label) || [{ type: 'text', value: label }];
    // An image keeps its alt text and becomes an ordinary link; a link whose
    // target was rejected keeps its label and becomes plain text.
    return {
      length: match[0].length,
      node: href ? { type: 'link', href, children } : { type: 'span', children }
    };
  }

  function isWordChar(char) {
    return !!char && /[\w一-鿿]/.test(char);
  }

  function parseInline(text) {
    const source = String(text == null ? '' : text);
    const nodes = [];
    let buffer = '';
    let index = 0;

    function flush() {
      if (buffer) {
        nodes.push({ type: 'text', value: buffer });
        buffer = '';
      }
    }
    function push(node) {
      flush();
      nodes.push(node);
    }

    while (index < source.length) {
      const char = source[index];
      const rest = source.slice(index);

      if (char === '\\' && ESCAPABLE.includes(source[index + 1] || '')) {
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
          push({ type: 'codespan', value: code[2].replace(/^ ([\s\S]*) $/, '$1') });
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
          push({ type: 'link', href: auto[1], children: [{ type: 'text', value: auto[1] }] });
          index += auto[0].length;
          continue;
        }
      }

      if (char === '~' && source[index + 1] === '~') {
        const del = /^~~(?=\S)([\s\S]*?\S)~~/.exec(rest);
        if (del) {
          push({ type: 'del', children: parseInline(del[1]) });
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
          push({ type: 'strong', children: parseInline(strong[1]) });
          index += strong[0].length;
          continue;
        }
        const em = new RegExp(`^${marker}(?=\\S)([\\s\\S]*?\\S)${marker}(?!${marker})`).exec(rest);
        if (em && !(char === '_' && isWordChar(source[index + em[0].length]))) {
          push({ type: 'em', children: parseInline(em[1]) });
          index += em[0].length;
          continue;
        }
      }

      buffer += char;
      index += 1;
    }

    flush();
    return nodes;
  }

  // --- blocks ---------------------------------------------------------------

  function splitRow(line) {
    return line
      .trim()
      .replace(/^\|/, '')
      .replace(/\|$/, '')
      .split(/(?<!\\)\|/)
      .map((cell) => cell.trim().replace(/\\\|/g, '|'));
  }

  function columnAlign(cell) {
    const start = cell.startsWith(':');
    const end = cell.endsWith(':');
    if (start && end) return 'center';
    if (end) return 'right';
    if (start) return 'left';
    return '';
  }

  function parse(text) {
    const lines = String(text == null ? '' : text).replace(/\r\n?/g, '\n').split('\n');
    const blocks = [];
    let index = 0;

    while (index < lines.length) {
      const line = lines[index];

      if (!line.trim()) {
        index += 1;
        continue;
      }

      const fence = FENCE_RE.exec(line);
      if (fence) {
        const marker = fence[1][0];
        const body = [];
        index += 1;
        while (index < lines.length && !new RegExp(`^ {0,3}${marker === '`' ? '`' : '~'}{${fence[1].length},}[ \t]*$`).test(lines[index])) {
          body.push(lines[index]);
          index += 1;
        }
        index += 1; // closing fence, or the end of the input
        blocks.push({ type: 'code', lang: fence[2] || '', text: body.join('\n') });
        continue;
      }

      if (HR_RE.test(line)) {
        blocks.push({ type: 'hr' });
        index += 1;
        continue;
      }

      const heading = HEADING_RE.exec(line);
      if (heading) {
        blocks.push({ type: 'heading', level: heading[1].length, children: parseInline(heading[2]) });
        index += 1;
        continue;
      }

      if (QUOTE_RE.test(line)) {
        const body = [];
        while (index < lines.length && (QUOTE_RE.test(lines[index]) || (body.length && lines[index].trim()))) {
          const quoted = QUOTE_RE.exec(lines[index]);
          body.push(quoted ? quoted[1] : lines[index]);
          index += 1;
        }
        blocks.push({ type: 'blockquote', children: parse(body.join('\n')) });
        continue;
      }

      // A table needs its delimiter row to be the very next line; without it
      // the pipes are just pipes in a paragraph.
      if (line.includes('|') && index + 1 < lines.length && TABLE_DIVIDER_RE.test(lines[index + 1]) && lines[index + 1].includes('-')) {
        const header = splitRow(line);
        const align = splitRow(lines[index + 1]).map(columnAlign);
        index += 2;
        const rows = [];
        while (index < lines.length && lines[index].trim() && lines[index].includes('|')) {
          rows.push(splitRow(lines[index]));
          index += 1;
        }
        blocks.push({
          type: 'table',
          align,
          header: header.map(parseInline),
          rows: rows.map((row) => row.map(parseInline))
        });
        continue;
      }

      const item = ITEM_RE.exec(line);
      if (item) {
        const ordered = /\d/.test(item[2]);
        const start = ordered ? parseInt(item[2], 10) : 1;
        const baseIndent = item[1].length;
        const items = [];
        while (index < lines.length) {
          const next = ITEM_RE.exec(lines[index]);
          if (!next || next[1].length > baseIndent + 1 || /\d/.test(next[2]) !== ordered) break;
          const body = [next[3]];
          index += 1;
          // Continuation lines: the item's own wrapped text, plus anything
          // indented under its marker — which is how a nested list stays
          // nested instead of flattening into its parent.
          while (index < lines.length && lines[index].trim() && !FENCE_RE.test(lines[index])) {
            const nested = ITEM_RE.exec(lines[index]);
            if (nested && nested[1].length <= baseIndent + 1) break;
            body.push(lines[index].replace(/^ {1,4}/, ''));
            index += 1;
          }
          items.push(parse(body.join('\n')));
        }
        blocks.push({ type: 'list', ordered, start, items });
        continue;
      }

      const paragraph = [];
      while (
        index < lines.length &&
        lines[index].trim() &&
        !FENCE_RE.test(lines[index]) &&
        !HR_RE.test(lines[index]) &&
        !HEADING_RE.test(lines[index]) &&
        !QUOTE_RE.test(lines[index]) &&
        !ITEM_RE.test(lines[index])
      ) {
        paragraph.push(lines[index].trim());
        index += 1;
      }
      blocks.push({ type: 'paragraph', children: parseInline(paragraph.join('\n')) });
    }

    return blocks;
  }

  // --- rendering ------------------------------------------------------------

  function highlight(code, lang) {
    const Prism = typeof globalThis !== 'undefined' ? globalThis.Prism : null;
    if (!Prism || !lang || !Prism.languages || !Prism.languages[lang]) return;
    try {
      Prism.highlightElement(code);
    } catch (_) {
      // A highlighter failure must never cost the user the message itself.
    }
  }

  function renderInline(nodes, parent, doc) {
    (nodes || []).forEach((node) => {
      if (node.type === 'text') {
        parent.appendChild(doc.createTextNode(node.value));
        return;
      }
      if (node.type === 'br') {
        parent.appendChild(doc.createElement('br'));
        return;
      }
      if (node.type === 'codespan') {
        const code = doc.createElement('code');
        code.textContent = node.value;
        parent.appendChild(code);
        return;
      }
      if (node.type === 'link') {
        const link = doc.createElement('a');
        link.href = node.href;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        renderInline(node.children, link, doc);
        parent.appendChild(link);
        return;
      }
      const tag = { strong: 'strong', em: 'em', del: 'del', span: 'span' }[node.type];
      const element = doc.createElement(tag || 'span');
      renderInline(node.children, element, doc);
      parent.appendChild(element);
    });
  }

  function renderBlock(block, doc) {
    if (block.type === 'hr') return doc.createElement('hr');

    if (block.type === 'heading') {
      const heading = doc.createElement(`h${Math.min(6, Math.max(1, block.level))}`);
      renderInline(block.children, heading, doc);
      return heading;
    }

    if (block.type === 'code') {
      const pre = doc.createElement('pre');
      const code = doc.createElement('code');
      const lang = SAFE_LANG_RE.test(block.lang) ? block.lang.toLowerCase() : '';
      code.textContent = block.text;
      if (lang) code.className = `language-${lang}`;
      pre.appendChild(code);
      highlight(code, lang);
      return pre;
    }

    if (block.type === 'blockquote') {
      const quote = doc.createElement('blockquote');
      block.children.forEach((child) => quote.appendChild(renderBlock(child, doc)));
      return quote;
    }

    if (block.type === 'list') {
      const list = doc.createElement(block.ordered ? 'ol' : 'ul');
      if (block.ordered && block.start !== 1) list.start = block.start;
      block.items.forEach((item) => {
        const li = doc.createElement('li');
        item.forEach((child, position) => {
          // A one-paragraph item renders inline, which is what keeps a short
          // bullet list tight instead of double-spaced.
          if (position === 0 && child.type === 'paragraph' && item.length === 1) {
            renderInline(child.children, li, doc);
            return;
          }
          li.appendChild(renderBlock(child, doc));
        });
        list.appendChild(li);
      });
      return list;
    }

    if (block.type === 'table') {
      const table = doc.createElement('table');
      const thead = doc.createElement('thead');
      const headRow = doc.createElement('tr');
      block.header.forEach((cell, column) => {
        const th = doc.createElement('th');
        if (block.align[column]) th.style.textAlign = block.align[column];
        renderInline(cell, th, doc);
        headRow.appendChild(th);
      });
      thead.appendChild(headRow);
      table.appendChild(thead);

      const tbody = doc.createElement('tbody');
      block.rows.forEach((row) => {
        const tr = doc.createElement('tr');
        row.forEach((cell, column) => {
          const td = doc.createElement('td');
          if (block.align[column]) td.style.textAlign = block.align[column];
          renderInline(cell, td, doc);
          tr.appendChild(td);
        });
        tbody.appendChild(tr);
      });
      table.appendChild(tbody);
      return table;
    }

    const paragraph = doc.createElement('p');
    renderInline(block.children, paragraph, doc);
    return paragraph;
  }

  function render(text, doc) {
    const target = doc || (typeof globalThis !== 'undefined' ? globalThis.document : null);
    if (!target) throw new Error('No document available to render into.');
    const fragment = target.createDocumentFragment();
    parse(text).forEach((block) => fragment.appendChild(renderBlock(block, target)));
    return fragment;
  }

  // Replaces an element's contents. textContent = '' rather than innerHTML = ''
  // for the same reason the renderer avoids innerHTML everywhere else.
  function renderInto(element, text) {
    if (!element) return element;
    element.textContent = '';
    element.appendChild(render(text, element.ownerDocument));
    return element;
  }

  return Object.freeze({ parse, parseInline, safeHref, render, renderInto });
});
