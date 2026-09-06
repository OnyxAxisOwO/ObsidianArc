// The instance's standing notice, above the chat.
//
// Not an announcement, despite the name they share. An announcement is dated,
// has a read state per person, and is done once it has been read. This is a
// property of the instance — a maintenance window, a house rule, a link
// everyone needs — and it stays up until an operator takes it down.
//
// That difference is why it lives in settings rather than in the
// announcements table: there is nothing to record about who has seen it, and
// the front door needs it before anyone has signed in.

import { t } from '../i18n';
import { siteInfo } from '../session';
import { ICONS, el, iconButton } from '../ui/dom';

const DISMISSED_KEY = 'obsidian-arc-home-notice-dismissed';

/**
 * Returns the strip, or null when there is nothing to say — which is the
 * common case, so callers append the result conditionally rather than
 * mounting an empty bar.
 */
export function createHomeNotice(): HTMLElement | null {
  const notice = siteInfo().home_notice;
  const text = notice?.text?.trim() ?? '';
  if (!text) return null;

  // Dismissal is remembered against the wording, not against a flag: an
  // operator who edits the notice is saying something new, and someone who
  // put the old one away has not read it.
  const dismissible = notice?.dismissible ?? true;
  const signature = fingerprint(text);
  if (dismissible && dismissedSignature() === signature) return null;

  const wrap = el('div', 'oa-home-notice');
  // textContent, and the line breaks preserved in CSS: the operator writes
  // this in a plain textarea and nothing here parses markup.
  wrap.appendChild(el('div', 'oa-home-notice-text', text));

  if (dismissible) {
    wrap.appendChild(iconButton('oa-icon-btn oa-home-notice-close', ICONS.close, t('close'), () => {
      remember(signature);
      wrap.remove();
    }, 15));
  }
  return wrap;
}

/**
 * A small non-cryptographic digest of the wording. It only has to change when
 * the text does, and be short enough to sit in localStorage — FNV-1a is both,
 * and costs nothing next to pulling in a hash.
 */
function fingerprint(text: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(36);
}

function dismissedSignature(): string | null {
  try {
    return localStorage.getItem(DISMISSED_KEY);
  } catch {
    // Storage refused: the notice simply stays up, which is the safer way
    // for this particular thing to fail.
    return null;
  }
}

function remember(signature: string): void {
  try {
    localStorage.setItem(DISMISSED_KEY, signature);
  } catch {
    // It closes for this view either way; it will be back on the next load.
  }
}
