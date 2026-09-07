// The operator's front page, reduced to inert formatting before it reaches
// the document.
//
// An administrator can manage accounts but is not necessarily the machine's
// owner. Raw HTML would let a compromised admin account persist a fake login
// form or a full-page <style> overlay even though CSP blocks script. So the
// markup is parsed inside a detached <template> and rebuilt from a strict
// allowlist: ordinary document structure only, with no style, event, form,
// media, id, class or data-bearing attributes.
//
// This is the one place in the project that assigns innerHTML, and it does it
// to a node that is never attached. If a grep for innerHTML under src/ ever
// returns a second one, that is the thing to look at.

const introTags = new Set([
  'A', 'ABBR', 'ARTICLE', 'ASIDE', 'B', 'BLOCKQUOTE', 'BR', 'CODE', 'DD',
  'DEL', 'DETAILS', 'DIV', 'DL', 'DT', 'EM', 'FIGCAPTION', 'FIGURE', 'FOOTER',
  'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'HEADER', 'HR', 'I', 'INS', 'KBD', 'LI',
  'MAIN', 'MARK', 'NAV', 'OL', 'P', 'PRE', 'Q', 'S', 'SAMP', 'SECTION', 'SMALL',
  'SPAN', 'STRONG', 'SUB', 'SUMMARY', 'SUP', 'TABLE', 'TBODY', 'TD', 'TFOOT',
  'TH', 'THEAD', 'TIME', 'TR', 'U', 'UL', 'VAR',
]);

const droppedIntroTrees = new Set([
  'AUDIO', 'BASE', 'BUTTON', 'CANVAS', 'EMBED', 'FORM', 'IFRAME', 'IMG', 'INPUT',
  'LINK', 'MATH', 'META', 'NOSCRIPT', 'OBJECT', 'OPTION', 'SCRIPT', 'SELECT',
  'SOURCE', 'STYLE', 'SVG', 'TEMPLATE', 'TEXTAREA', 'TRACK', 'VIDEO',
]);

export function safeIntro(html: string): DocumentFragment {
  const source = document.createElement('template');
  source.innerHTML = html;
  const result = document.createDocumentFragment();
  copySafeIntroChildren(source.content, result);
  return result;
}

function copySafeIntroChildren(source: Node, target: Node): void {
  for (const child of source.childNodes) {
    if (child.nodeType === Node.TEXT_NODE) {
      target.appendChild(document.createTextNode(child.textContent ?? ''));
      continue;
    }
    if (!(child instanceof Element)) continue;
    // Foreign elements (SVG, MathML) have lowercase tagNames in the HTML DOM.
    // Uppercasing keeps allowlist lookups namespace-agnostic.
    const tag = child.tagName.toUpperCase();
    if (droppedIntroTrees.has(tag)) continue;
    if (!introTags.has(tag)) {
      copySafeIntroChildren(child, target);
      continue;
    }

    const clean = document.createElement(tag.toLowerCase());
    if (tag === 'A') copySafeLink(child, clean);
    copySafeIntroChildren(child, clean);
    target.appendChild(clean);
  }
}

function copySafeLink(source: Element, target: HTMLElement): void {
  const href = source.getAttribute('href');
  if (href) {
    try {
      const protocol = new URL(href, window.location.href).protocol;
      if (protocol === 'http:' || protocol === 'https:' || protocol === 'mailto:' || protocol === 'tel:') {
        target.setAttribute('href', href);
      }
    } catch {
      // A malformed link remains text.
    }
  }
  const title = source.getAttribute('title');
  if (title) target.setAttribute('title', title);
  target.setAttribute('rel', 'noopener noreferrer');
}
