// A short burst of paper.
//
// For the two moments in this interface that are unambiguously good news: an
// administrator putting everybody's usage back, and somebody spending a reset
// card on their own. Both are the end of "you have run out", and both used to
// be a sentence in small grey text.
//
// Hand-rolled rather than a package, for the same reason as before: this is
// forty lines, and a confetti library is several kilobytes to draw a thing
// that falls down. It stays imperative because it is not part of any
// component's state — nothing re-renders because paper is falling.

const PIECES = 44;
const FALL_MS = 2200;

/**
 * Colours are read from the palette rather than written down, so a burst
 * belongs to whatever accent the instance is set to instead of being the one
 * party trick in the interface with hues of its own.
 */
function palette(): string[] {
  const style = getComputedStyle(document.documentElement);
  const accent = style.getPropertyValue('--ai-primary').trim() || '#6c4cd6';
  const danger = style.getPropertyValue('--ai-danger').trim() || '#e5484d';
  const card = style.getPropertyValue('--ai-surface-card').trim() || '#ffffff';
  return [accent, accent, danger, card];
}

export function celebrate(): void {
  // Somebody who has asked for less motion has asked for exactly this.
  if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return;

  const layer = document.createElement('div');
  layer.className = 'oa-confetti';
  // Decoration with nothing to read: a screen reader should walk straight
  // past it, and a pointer should never land on it.
  layer.setAttribute('aria-hidden', 'true');

  const colours = palette();
  for (let i = 0; i < PIECES; i += 1) {
    const piece = document.createElement('span');
    piece.className = 'oa-confetti-piece';
    piece.style.left = `${Math.random() * 100}%`;
    piece.style.background = colours[i % colours.length]!;
    // Spread the start so it falls as a shower rather than a single line,
    // and vary the duration so the shower does not land all at once.
    piece.style.animationDelay = `${Math.random() * 400}ms`;
    piece.style.animationDuration = `${FALL_MS - Math.random() * 700}ms`;
    piece.style.setProperty('--drift', `${(Math.random() - 0.5) * 260}px`);
    piece.style.setProperty('--spin', `${Math.random() * 1080 - 540}deg`);
    if (i % 3 === 0) piece.style.borderRadius = '50%';
    layer.appendChild(piece);
  }

  document.body.appendChild(layer);
  window.setTimeout(() => layer.remove(), FALL_MS + 500);
}
