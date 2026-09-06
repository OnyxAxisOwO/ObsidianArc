// What someone with no account sees at the address.
//
// Three shapes, chosen by the operator: the sign-in card (what the instance
// always did), a page they wrote, or the chat itself — optionally live, so a
// visitor can ask a couple of questions before deciding whether to sign up.
//
// The trial is the only part of this project that spends the operator's
// provider credit for someone who has not identified themselves, so it holds
// nothing back on the client. The turn count is the server's, signed, and
// simply carried back and forth by this page; the per-address and
// instance-wide ceilings behind it are what hold when a client throws the
// token away. Losing this file entirely would cost the operator nothing.

import { ApiError } from '../api/client';
import { streamTrial } from '../api/trial';
import { t } from '../i18n';
import { navigate } from '../router';
import { siteInfo } from '../session';
import { renderInto } from '../chat/markdown';
import { nextThemeMode, themeMode } from '../theme/theme';
import { persistTheme } from '../session';
import { createHomeNotice } from '../announce/home-notice';
import { ICONS, button, clear, el, icon, iconButton } from '../ui/dom';

interface Turn {
  role: 'user' | 'assistant';
  content: string;
}

export function renderLandingPage(root: HTMLElement): void {
  clear(root);

  const site = siteInfo();
  const landing = site.landing ?? { mode: 'login' as const, intro: '', trial: false, trial_turns: 0 };

  const page = el('div', 'oa-landing');
  const head = el('div', 'oa-landing-head');

  const brand = el('div', 'oa-landing-brand');
  const mark = el('span', 'oa-auth-mark');
  mark.appendChild(icon(ICONS.spark, 15));
  brand.appendChild(mark);
  brand.appendChild(el('span', null, site.name));
  head.appendChild(brand);
  head.appendChild(el('span', 'oa-header-spacer'));

  // The same toggle the sign-in card carries, for the same reason: this may
  // be the first thing anyone sees, and being stuck in the wrong scheme until
  // you have an account is an odd first impression.
  const toggle = iconButton('oa-icon-btn', themeIcon(), t('theme'), () => {
    persistTheme(nextThemeMode());
    clear(toggle);
    toggle.appendChild(icon(themeIcon(), 17));
  }, 17);
  head.appendChild(toggle);

  head.appendChild(button('oa-btn', t('landingSignIn'), () => navigate('/login')));
  if (site.registration_enabled) {
    head.appendChild(button('oa-btn primary', t('landingRegister'), () => navigate('/register')));
  }
  page.appendChild(head);

  // The same standing notice the chat carries. "Above the home page" has to
  // mean the page a visitor actually lands on, and for an instance with a
  // public front door that is this one rather than the chat behind it.
  const notice = createHomeNotice();
  if (notice) page.appendChild(notice);

  const body = el('div', 'oa-landing-body');
  if (landing.mode === 'intro') body.appendChild(intro(landing.intro, site.description));
  else body.appendChild(trial(landing.trial, landing.trial_turns, site.registration_enabled));
  page.appendChild(body);

  root.appendChild(page);
}

function themeIcon(): readonly string[] {
  const mode = themeMode();
  return mode === 'dark' ? ICONS.moon : mode === 'light' ? ICONS.sun : ICONS.auto;
}

/** The operator's page, reduced to inert formatting before it reaches DOM. */
function intro(html: string, description: string): HTMLElement {
  const card = el('div', 'oa-landing-intro');
  if (html.trim()) {
    card.appendChild(safeIntro(html));
    return card;
  }
  // Nothing written yet: say what the instance is, from the setting the
  // sign-in card already uses, rather than showing an empty page.
  card.appendChild(el('h1', 'oa-landing-title', t('welcomeBack')));
  if (description) card.appendChild(el('p', 'oa-landing-sub', description));
  return card;
}

// An administrator can manage accounts but is not necessarily the machine's
// owner. Raw HTML would let a compromised admin account persist a fake login
// form or full-page <style> overlay even though CSP blocks script. Parse it in
// a detached template and rebuild only ordinary document structure, with no
// style, event, form, media, id, class, or data-bearing attributes.
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

function trial(enabled: boolean, allowance: number, canRegister: boolean): HTMLElement {
  const wrap = el('div', 'oa-landing-chat');
  const site = siteInfo();

  wrap.appendChild(el('h1', 'oa-landing-title', site.name));
  if (site.description) wrap.appendChild(el('p', 'oa-landing-sub', site.description));

  const thread = el('div', 'oa-landing-thread');
  wrap.appendChild(thread);

  const notice = el('p', 'oa-landing-notice');
  wrap.appendChild(notice);

  if (!enabled) {
    // The shop window: the interface is visible, nothing can be sent. Saying
    // so beats a composer that silently refuses.
    notice.textContent = t('setupBodyUser');
    wrap.appendChild(entry(canRegister));
    return wrap;
  }

  const turns: Turn[] = [];
  let busy = false;
  // The server's signed count of turns used. Opaque, and the only thing
  // that makes the limit mean anything: the exchange itself is written
  // here, so a page that simply forgot the earlier turns would otherwise
  // look like a first-time visitor forever.
  let continuation = '';

  const form = el('form', 'oa-landing-composer');
  const input = el('input');
  input.type = 'text';
  input.placeholder = t('trialPlaceholder');
  input.maxLength = 4000;
  const send = iconButton('ai-chat-send', ICONS.send, t('send'), undefined, 16);
  send.type = 'submit';
  form.appendChild(input);
  form.appendChild(send);
  wrap.appendChild(form);

  const finished = el('div', 'oa-landing-finished');
  finished.hidden = true;
  wrap.appendChild(finished);

  paintNotice();

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (!text || busy) return;

    input.value = '';
    push('user', text);
    const answer = push('assistant', '');
    busy = true;
    send.disabled = true;

    // Everything but the empty assistant turn just pushed to render into.
    // The question itself is already the last entry.
    const sending = turns.slice(0, -1).map((entry) => ({ role: entry.role, content: entry.content }));

    void streamTrial(sending, continuation, (delta) => {
      answer.content += delta;
      renderInto(answer.node, answer.content);
      thread.scrollTop = thread.scrollHeight;
    })
      .then((result) => {
        turns[turns.length - 1]!.content = answer.content;
        continuation = result.continuation;
        left = result.turnsLeft;
      })
      .catch((error: unknown) => {
        answer.content = error instanceof ApiError ? error.message : t('trialFailed');
        answer.node.textContent = answer.content;
        answer.node.classList.add('oa-landing-error');
      })
      .finally(() => {
        busy = false;
        send.disabled = false;
        paintNotice();
      });
  });

  function push(role: Turn['role'], content: string): Turn & { node: HTMLElement } {
    const turn: Turn = { role, content };
    turns.push(turn);

    const row = el('div', `oa-landing-turn ${role}`);
    const bubble = el('div', role === 'user' ? 'ai-user-bubble' : 'ai-answer');
    if (content) bubble.textContent = content;
    row.appendChild(bubble);
    thread.appendChild(row);
    thread.scrollTop = thread.scrollHeight;

    return Object.assign(turn, { node: bubble });
  }

  // What the server said is left after the last answer. Before the first,
  // the configured allowance is the honest guess.
  let left = allowance;

  function paintNotice(): void {
    if (left === 0) {
      notice.textContent = t('trialFinished');
      form.hidden = true;
      finished.hidden = false;
      clear(finished);
      finished.appendChild(entry(canRegister));
      return;
    }
    notice.textContent = left === 1 ? t('trialLastTurn') : t('trialTurnsLeft', { count: left });
  }

  return wrap;
}

/** The way in, once the trial is over or was never on offer. */
function entry(canRegister: boolean): HTMLElement {
  const row = el('div', 'oa-landing-entry');
  if (canRegister) {
    row.appendChild(button('oa-btn primary', t('trialSignUp'), () => navigate('/register')));
  }
  row.appendChild(button('oa-btn', t('trialSignIn'), () => navigate('/login')));
  return row;
}
