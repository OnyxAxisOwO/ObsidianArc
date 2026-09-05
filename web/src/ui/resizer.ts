// Draggable column edges.
//
// The rail and the side panel are fixed widths that suit a 1280px window and
// nothing else: someone on a wide monitor wants a wider conversation list,
// someone editing a long model form wants a wider panel. This makes the edge
// between two columns a handle.
//
// The width is written to a CSS custom property rather than to `style.width`,
// because both columns already derive other things from it — the rail's
// collapsed margin, the panel's slide-out — and those have to move with it.
//
// It is a `separator` with arrow-key support, not only a drag target: a
// column you can resize with a mouse and not with a keyboard is a column half
// the people cannot resize.

export interface ResizerOptions {
  /** The column being resized. The handle is positioned against its edge. */
  target: HTMLElement;
  /** Which of the target's edges the handle sits on. */
  edge: 'left' | 'right';
  /** The custom property the width is written to. */
  cssVariable: string;
  /** Where that property is set — often the target, sometimes its row. */
  styleTarget: HTMLElement;
  storageKey: string;
  min: number;
  max: number;
  /** Used when nothing is stored, and restored on a double-click. */
  fallback: number;
  label: string;
}

export function attachResizer(options: ResizerOptions): void {
  const { target, styleTarget, cssVariable } = options;

  const clamp = (value: number) => Math.min(options.max, Math.max(options.min, Math.round(value)));
  const apply = (value: number) => styleTarget.style.setProperty(cssVariable, `${clamp(value)}px`);

  const persist = (value: number) => {
    try {
      localStorage.setItem(options.storageKey, String(clamp(value)));
    } catch {
      // Best effort; the width still applies for this page.
    }
  };

  apply(storedWidth(options.storageKey, options.fallback));

  const handle = document.createElement('div');
  handle.className = `oa-resizer on-${options.edge}`;
  handle.setAttribute('role', 'separator');
  handle.setAttribute('aria-orientation', 'vertical');
  handle.setAttribute('aria-label', options.label);
  handle.tabIndex = 0;

  const currentWidth = () => {
    const raw = getComputedStyle(styleTarget).getPropertyValue(cssVariable);
    const parsed = parseFloat(raw);
    return Number.isFinite(parsed) ? parsed : options.fallback;
  };

  let startX = 0;
  let startWidth = 0;

  handle.addEventListener('pointerdown', (event) => {
    // Only the primary button; a right-click here is a context menu.
    if (event.button !== 0) return;
    event.preventDefault();
    startX = event.clientX;
    startWidth = currentWidth();
    handle.setPointerCapture(event.pointerId);
    handle.classList.add('active');
    // On the body, so the col-resize cursor and the text-selection block
    // apply everywhere the pointer travels — not only over the handle.
    document.body.classList.add('oa-resizing');
  });

  handle.addEventListener('pointermove', (event) => {
    if (!handle.hasPointerCapture(event.pointerId)) return;
    const delta = event.clientX - startX;
    // Dragging the left edge of a right-hand column makes it wider when the
    // pointer moves left, so the sign flips.
    apply(startWidth + (options.edge === 'right' ? delta : -delta));
  });

  const release = (event: PointerEvent) => {
    if (!handle.hasPointerCapture(event.pointerId)) return;
    handle.releasePointerCapture(event.pointerId);
    handle.classList.remove('active');
    document.body.classList.remove('oa-resizing');
    persist(currentWidth());
  };
  handle.addEventListener('pointerup', release);
  handle.addEventListener('pointercancel', release);

  handle.addEventListener('keydown', (event) => {
    const step = event.shiftKey ? 64 : 16;
    let next: number | null = null;
    if (event.key === 'ArrowLeft') next = currentWidth() + (options.edge === 'right' ? -step : step);
    else if (event.key === 'ArrowRight') next = currentWidth() + (options.edge === 'right' ? step : -step);
    else if (event.key === 'Home') next = options.min;
    else if (event.key === 'End') next = options.max;
    if (next === null) return;
    event.preventDefault();
    apply(next);
    persist(currentWidth());
  });

  // Back to the width it shipped with, for anyone who has dragged themselves
  // somewhere they do not want to be.
  handle.addEventListener('dblclick', () => {
    apply(options.fallback);
    persist(options.fallback);
  });

  target.appendChild(handle);
}

export function storedWidth(key: string, fallback: number): number {
  try {
    const raw = localStorage.getItem(key);
    const parsed = raw === null ? NaN : Number(raw);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  } catch {
    return fallback;
  }
}
