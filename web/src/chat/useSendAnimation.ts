// Telegram-style send flight animation for chat messages.
//
// When a user dispatches a message, a bubble emerges from the composer's
// input field, initially scaled up (1.18x), and smoothly scales down into its
// target position in the message stream with hardware-accelerated transforms
// (60/120fps).
//
// Respects prefers-reduced-motion, handles multi-line/attachments gracefully,
// and ensures seamless handoff to the stationary transcript message.

import { nextTick, ref } from 'vue';

export const flyingIDs = ref<string[]>([]);
const flownIDs = ref<string[]>([]);
export const lastComposerRect = ref<DOMRect | null>(null);

const activeAnimations = new Map<string, { animation: Animation; clone: HTMLElement }>();

export function isFlying(id: string): boolean {
  return flyingIDs.value.includes(id);
}

export function hasFlown(id: string): boolean {
  return flownIDs.value.includes(id);
}

// Read at each call rather than once at load: the setting can change while the
// page is open.
function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true;
}

export function markFlying(id: string): void {
  if (!id) return;
  if (prefersReducedMotion()) {
    if (!flownIDs.value.includes(id)) {
      flownIDs.value = [...flownIDs.value, id];
    }
    return;
  }
  flownIDs.value = flownIDs.value.filter((item) => item !== id);
  if (!flyingIDs.value.includes(id)) {
    flyingIDs.value = [...flyingIDs.value, id];
  }
}

export function captureComposerRect(target?: HTMLElement | DOMRect | null): void {
  if (!target) return;
  if ('top' in target && 'width' in target && typeof target.top === 'number') {
    if (target.width > 0 && target.height > 0) {
      lastComposerRect.value = target as DOMRect;
    }
    return;
  }
  if (typeof (target as HTMLElement).getBoundingClientRect === 'function') {
    const rect = (target as HTMLElement).getBoundingClientRect();
    if (rect.width > 0 && rect.height > 0) {
      lastComposerRect.value = rect;
    }
  }
}

export function removeFlying(id: string): void {
  flyingIDs.value = flyingIDs.value.filter((item) => item !== id);
  if (!flownIDs.value.includes(id)) {
    flownIDs.value = [...flownIDs.value, id];
  }
  const active = activeAnimations.get(id);
  if (active) {
    try {
      active.clone.remove();
    } catch {
      // already removed
    }
    activeAnimations.delete(id);
  }
}

export function cancelAllFlights(): void {
  for (const [id, active] of activeAnimations.entries()) {
    try {
      active.animation.cancel();
      active.clone.remove();
    } catch {
      // ignore
    }
    if (!flownIDs.value.includes(id)) {
      flownIDs.value = [...flownIDs.value, id];
    }
  }
  for (const id of flyingIDs.value) {
    if (!flownIDs.value.includes(id)) {
      flownIDs.value = [...flownIDs.value, id];
    }
  }
  activeAnimations.clear();
  flyingIDs.value = [];
  lastComposerRect.value = null;
}

export function resetFlightState(): void {
  cancelAllFlights();
  flownIDs.value = [];
}

interface SendAnimationOptions {
  messageID: string;
  mainEl: HTMLElement;
  transcriptEl: HTMLElement;
  composerInputEl?: HTMLElement | null;
  scrollToBottom?: () => void;
  onFinish?: () => void;
}

/** How much larger the bubble is as it leaves the composer than where it lands. */
const EMERGE_SCALE = 1.18;

export async function playSendAnimation(options: SendAnimationOptions): Promise<void> {
  const { messageID, mainEl, transcriptEl, composerInputEl, scrollToBottom, onFinish } = options;

  // Every way out lands the bubble in place and tells the caller, whether it
  // flew or not; one helper, so no early exit can forget half of that.
  const done = () => {
    removeFlying(messageID);
    onFinish?.();
  };

  // 1. Accessibility: respect prefers-reduced-motion
  if (prefersReducedMotion()) {
    // Consumed here as step 5 would have: left behind, a stale rect from this
    // send would be where the next one flies from.
    lastComposerRect.value = null;
    done();
    return;
  }

  // 2. Mark this message as in-flight
  markFlying(messageID);

  // 3. Wait for DOM patch
  await nextTick();
  scrollToBottom?.();

  // 4. Locate target element
  const messageRow = transcriptEl.querySelector<HTMLElement>(`[data-message-id="${messageID}"]`);
  if (!messageRow) {
    done();
    return;
  }

  const bubbleEl = messageRow.querySelector<HTMLElement>('.ai-bubble');
  const attachmentsEl = messageRow.querySelector<HTMLElement>('.ai-chat-attachments');
  const isCompound = !!(bubbleEl && attachmentsEl);
  const targetEl = isCompound ? messageRow : (bubbleEl || attachmentsEl || messageRow);

  if (typeof targetEl.animate !== 'function' || typeof mainEl.getBoundingClientRect !== 'function') {
    done();
    return;
  }

  const mainRect = mainEl.getBoundingClientRect();
  let targetRect: DOMRect;
  if (isCompound && bubbleEl && attachmentsEl) {
    const aRect = attachmentsEl.getBoundingClientRect();
    const bRect = bubbleEl.getBoundingClientRect();
    const unionLeft = Math.min(aRect.left, bRect.left);
    const unionTop = Math.min(aRect.top, bRect.top);
    const unionRight = Math.max(aRect.right, bRect.right);
    const unionBottom = Math.max(aRect.bottom, bRect.bottom);
    targetRect = {
      left: unionLeft,
      top: unionTop,
      right: unionRight,
      bottom: unionBottom,
      width: unionRight - unionLeft,
      height: unionBottom - unionTop,
      x: unionLeft,
      y: unionTop,
      toJSON: () => {},
    };
  } else {
    targetRect = targetEl.getBoundingClientRect();
  }

  // 5. Read source rect (from composer input)
  const sourceRect = lastComposerRect.value || (composerInputEl && typeof composerInputEl.getBoundingClientRect === 'function' ? composerInputEl.getBoundingClientRect() : null);
  lastComposerRect.value = null; // consume

  // Guard against zero-size or unrendered elements (e.g. headless tests)
  if (!sourceRect || targetRect.width <= 0 || targetRect.height <= 0 || sourceRect.width <= 0) {
    done();
    return;
  }

  // Guard against targets completely outside visible area
  if (targetRect.bottom < mainRect.top || targetRect.top > mainRect.bottom + 100) {
    done();
    return;
  }

  // 6. Coordinates relative to mainEl
  const targetLeft = targetRect.left - mainRect.left - (mainEl.clientLeft || 0);
  const targetTop = targetRect.top - mainRect.top - (mainEl.clientTop || 0);
  const targetWidth = targetRect.width;
  const targetHeight = targetRect.height;

  const sourceLeft = sourceRect.left - mainRect.left - (mainEl.clientLeft || 0);
  const sourceRight = sourceRect.right - mainRect.left - (mainEl.clientLeft || 0);
  const sourceTop = sourceRect.top - mainRect.top - (mainEl.clientTop || 0);
  const sourceHeight = sourceRect.height;

  // Origin center of the bubble at the composer input box
  const halfTargetW = targetWidth / 2;
  const halfTargetH = targetHeight / 2;
  const targetCenterX = targetLeft + halfTargetW;
  const targetCenterY = targetTop + halfTargetH;

  const minCenterX = sourceLeft + 10 + Math.min(halfTargetW, sourceRect.width / 2);
  const maxCenterX = Math.max(minCenterX, sourceRight - 10 - Math.min(halfTargetW, sourceRect.width / 2));
  // Natural alignment with the target bubble's horizontal position, clamped within input box
  const sourceCenterX = Math.max(minCenterX, Math.min(maxCenterX, targetCenterX));
  // Vertical center inside the composer input box, adapting to single or multi-line
  const sourceCenterY = sourceTop + Math.max(halfTargetH, Math.min(sourceHeight - halfTargetH, sourceHeight / 2));

  const deltaX = sourceCenterX - targetCenterX;
  const deltaY = sourceCenterY - targetCenterY;

  // If already at destination, finish immediately
  if (Math.hypot(deltaX, deltaY) < 4) {
    done();
    return;
  }

  // 7. Clone target element for flight
  let clone: HTMLElement;
  if (isCompound && attachmentsEl && bubbleEl) {
    clone = document.createElement('div');
    clone.className = 'ai-chat-flight-compound';
    const aClone = attachmentsEl.cloneNode(true) as HTMLElement;
    const bClone = bubbleEl.cloneNode(true) as HTMLElement;
    clone.appendChild(aClone);
    clone.appendChild(bClone);
  } else {
    clone = targetEl.cloneNode(true) as HTMLElement;
    clone.classList.remove('ai-msg-flying');
    clone.classList.add('ai-chat-flight-bubble');
  }
  clone.removeAttribute('id');
  clone.querySelectorAll('[id]').forEach((node) => node.removeAttribute('id'));

  clone.style.position = 'absolute';
  clone.style.left = `${targetLeft}px`;
  clone.style.top = `${targetTop}px`;
  clone.style.width = `${targetWidth}px`;
  clone.style.height = `${targetHeight}px`;
  clone.style.margin = '0';
  clone.style.pointerEvents = 'none';
  clone.style.zIndex = '90';
  clone.style.boxSizing = 'border-box';
  clone.style.transformOrigin = 'center center';
  clone.style.willChange = 'transform, opacity, border-radius, box-shadow';

  // 8. Keyframes: emerge enlarged from composer and shrink down into target bubble (1.0x)
  // The radii, shadows and easing here are literals, not the chat's tokens: the
  // flight is an element animated through the Web Animations API, which takes
  // plain values, and the stylesheet has no radius or easing tokens to read.
  const emerge = {
    transform: `translate3d(${deltaX}px, ${deltaY}px, 0) scale(${EMERGE_SCALE})`,
    borderRadius: '22px',
    boxShadow: '0 12px 30px rgba(0, 0, 0, 0.22), 0 2px 6px rgba(0, 0, 0, 0.12)',
    opacity: 0.95,
  };

  // Apply initial keyframe 0 values before DOM insertion so the clone emerges
  // directly from the composer, preventing any single-frame flash at destination.
  // Read from the keyframe itself, so the two cannot disagree by a frame.
  clone.style.transform = emerge.transform;
  clone.style.borderRadius = emerge.borderRadius;
  clone.style.boxShadow = emerge.boxShadow;
  clone.style.opacity = String(emerge.opacity);

  mainEl.appendChild(clone);

  const keyframes: Keyframe[] = [
    emerge,
    {
      transform: `translate3d(${deltaX * 0.35}px, ${deltaY * 0.28}px, 0) scale(1.08)`,
      borderRadius: '18px 18px 10px 18px',
      boxShadow: '0 6px 18px rgba(0, 0, 0, 0.14)',
      opacity: 1,
      offset: 0.45,
    },
    {
      transform: 'translate3d(0, 0, 0) scale(1)',
      borderRadius: '16px 16px 4px 16px',
      boxShadow: '0 1px 2px rgba(0, 0, 0, 0)',
      opacity: 1,
    },
  ];

  let anim: Animation;
  try {
    anim = clone.animate(keyframes, {
      duration: 380,
      // A literal, not an --ai-ease-* token: the Web Animations API parses its
      // easing as a bare timing function and rejects var().
      easing: 'cubic-bezier(0.2, 0.9, 0.1, 1)',
      fill: 'forwards',
    });
  } catch {
    clone.remove();
    done();
    return;
  }

  activeAnimations.set(messageID, { animation: anim, clone });

  anim.onfinish = done;
  anim.oncancel = done;
}
