// Where an option list goes, given the trigger's rectangle.
//
// Pure, and its own module, because it is the part with the arithmetic in it
// and jsdom reports every rectangle as zero — a browser is needed to see this
// happen but not to check that it is right.

/** The gap between a control and its list, matching the one .oa-menu leaves. */
export const GAP = 6;
/** Room kept between the list and the edge of the window. */
export const MARGIN = 8;
/** The tallest a list gets before it scrolls, matching .oa-menu. */
export const MAX_HEIGHT = 320;

export interface TriggerBox {
  top: number;
  bottom: number;
  left: number;
  width: number;
}

export interface Placement {
  top: number;
  left: number;
  maxHeight: number;
  origin: 'top left' | 'bottom left';
}

/**
 * The clamp is the point. A list is capped at the room on the side it ends up
 * on, and the flip is then decided against those clamped heights: a static
 * cap would let a 320px list hang off the bottom of a short window, where a
 * body-fixed node scrolls with nothing and the rows below the fold are simply
 * unreachable.
 */
export function placeList(
  box: TriggerBox,
  natural: number,
  width: number,
  view: { width: number; height: number },
): Placement {
  const below = Math.max(0, view.height - box.bottom - GAP - MARGIN);
  const above = Math.max(0, box.top - GAP - MARGIN);
  // Above only when it buys room. Both sides clamp, so the question is which
  // one fits more of the list, not which one fits all of it.
  const flip = natural > below && above > below;
  const room = Math.min(MAX_HEIGHT, flip ? above : below);
  const height = Math.min(natural, room);

  return {
    top: flip ? Math.max(MARGIN, box.top - GAP - height) : box.bottom + GAP,
    left: Math.max(MARGIN, Math.min(box.left, view.width - MARGIN - width)),
    maxHeight: room,
    origin: flip ? 'bottom left' : 'top left',
  };
}
