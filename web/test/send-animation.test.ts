import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import {
  flyingIDs,
  lastComposerRect,
  isFlying,
  hasFlown,
  markFlying,
  resetFlightState,
  captureComposerRect,
  removeFlying,
  cancelAllFlights,
  playSendAnimation,
} from '../src/chat/useSendAnimation';
import ChatSurface from '../src/chat/ChatSurface.vue';
import ChatMessage from '../src/chat/ChatMessage.vue';
import { providePanelHost } from '../src/composables/usePanelHost';
import { messages, justSentID } from '../src/chat/useChat';

let app: App | null = null;
let host: HTMLElement;

beforeEach(() => {
  document.body.textContent = '';
  host = document.createElement('div');
  document.body.appendChild(host);
  resetFlightState();
  messages.value = [];
  justSentID.value = '';
});

afterEach(() => {
  cancelAllFlights();
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.restoreAllMocks();
});

describe('Telegram-style message send flight animation', () => {
  it('captureComposerRect records DOMRect or HTMLElement bounds', () => {
    expect(lastComposerRect.value).toBeNull();

    // From DOMRect-like object
    captureComposerRect({ top: 100, left: 50, width: 300, height: 40 } as DOMRect);
    expect(lastComposerRect.value).toEqual({ top: 100, left: 50, width: 300, height: 40 });

    // From element
    const el = document.createElement('div');
    vi.spyOn(el, 'getBoundingClientRect').mockReturnValue({
      top: 200,
      left: 80,
      width: 400,
      height: 44,
      right: 480,
      bottom: 244,
    } as DOMRect);

    captureComposerRect(el);
    expect(lastComposerRect.value?.top).toBe(200);
    expect(lastComposerRect.value?.width).toBe(400);

    // Ignores zero-size element
    const zeroEl = document.createElement('div');
    vi.spyOn(zeroEl, 'getBoundingClientRect').mockReturnValue({
      top: 0, left: 0, width: 0, height: 0, right: 0, bottom: 0,
    } as DOMRect);
    captureComposerRect(zeroEl);
    expect(lastComposerRect.value?.top).toBe(200); // untouched
  });

  it('plays flight animation from composer input to transcript target with scale down and hardware-accelerated transforms', async () => {
    const mainEl = document.createElement('div');
    mainEl.className = 'ai-chat-main';
    host.appendChild(mainEl);

    vi.spyOn(mainEl, 'getBoundingClientRect').mockReturnValue({
      top: 50,
      left: 100,
      width: 600,
      height: 800,
      right: 700,
      bottom: 850,
    } as DOMRect);

    const transcriptEl = document.createElement('div');
    transcriptEl.className = 'ai-chat-transcript';
    mainEl.appendChild(transcriptEl);

    // Target message in transcript
    const msgEl = document.createElement('div');
    msgEl.className = 'ai-msg ai-msg-user';
    msgEl.setAttribute('data-message-id', 'msg-1');
    const bubbleEl = document.createElement('div');
    bubbleEl.className = 'ai-bubble';
    bubbleEl.textContent = 'Hello Telegram animation';
    msgEl.appendChild(bubbleEl);
    transcriptEl.appendChild(msgEl);

    vi.spyOn(bubbleEl, 'getBoundingClientRect').mockReturnValue({
      top: 400,
      left: 450,
      width: 180,
      height: 38,
      right: 630,
      bottom: 438,
    } as DOMRect);

    // Composer input element
    const composerInputEl = document.createElement('textarea');
    composerInputEl.className = 'ai-chat-input';
    mainEl.appendChild(composerInputEl);

    vi.spyOn(composerInputEl, 'getBoundingClientRect').mockReturnValue({
      top: 750,
      left: 150,
      width: 480,
      height: 40,
      right: 630,
      bottom: 790,
    } as DOMRect);

    let recordedKeyframes: Keyframe[] | null = null;
    let recordedOptions: any = null;
    let mockAnimationFinish: (() => void) | null = null;

    const mockAnimate = vi.fn().mockImplementation(function (
      this: HTMLElement,
      keyframes: Keyframe[],
      options: any,
    ) {
      recordedKeyframes = keyframes;
      recordedOptions = options;
      return {
        cancel: vi.fn(),
        finish: vi.fn(),
        set onfinish(cb: () => void) {
          mockAnimationFinish = cb;
        },
      } as unknown as Animation;
    });

    Element.prototype.animate = mockAnimate;

    const playPromise = playSendAnimation({
      messageID: 'msg-1',
      mainEl,
      transcriptEl,
      composerInputEl,
    });

    // Message is marked as flying immediately
    expect(isFlying('msg-1')).toBe(true);

    await playPromise;

    // A clone element with .ai-chat-flight-bubble was created and appended
    const flightEl = mainEl.querySelector<HTMLElement>('.ai-chat-flight-bubble');
    expect(flightEl).not.toBeNull();
    expect(flightEl?.textContent).toBe('Hello Telegram animation');
    expect(flightEl?.style.position).toBe('absolute');
    expect(flightEl?.style.pointerEvents).toBe('none');

    // Verify keyframes
    expect(recordedKeyframes).not.toBeNull();
    expect(recordedKeyframes!.length).toBe(3);

    const startFrame = recordedKeyframes![0]!;
    const midFrame = recordedKeyframes![1]!;
    const endFrame = recordedKeyframes![2]!;

    // Starts enlarged (scale 1.18) at composer input position
    expect(startFrame.transform).toContain('scale(1.18)');
    expect(startFrame.transform).toContain('translate3d(');
    expect(startFrame.borderRadius).toBe('22px');
    expect(startFrame.boxShadow).toBeDefined();

    // Mid-flight shrinks to 1.08
    expect(midFrame.transform).toContain('scale(1.08)');
    expect(midFrame.offset).toBe(0.45);

    // Lands at destination with scale(1) and exact bubble border radius
    expect(endFrame.transform).toBe('translate3d(0, 0, 0) scale(1)');
    expect(endFrame.borderRadius).toBe('16px 16px 4px 16px');

    // Animation options: 380ms duration, smooth cubic-bezier curve
    expect(recordedOptions?.duration).toBe(380);
    expect(recordedOptions?.easing).toBe('cubic-bezier(0.2, 0.9, 0.1, 1)');

    // When animation finishes: clone is removed and isFlying is false
    (mockAnimationFinish as (() => void) | null)?.();
    expect(isFlying('msg-1')).toBe(false);
    expect(mainEl.querySelector('.ai-chat-flight-bubble')).toBeNull();
  });

  it('respects prefers-reduced-motion by skipping flight animation and displaying immediately', async () => {
    const originalMatchMedia = window.matchMedia;
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: query.includes('prefers-reduced-motion: reduce'),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })) as any;

    const mainEl = document.createElement('div');
    host.appendChild(mainEl);
    const transcriptEl = document.createElement('div');
    mainEl.appendChild(transcriptEl);

    const mockAnimate = vi.fn();
    Element.prototype.animate = mockAnimate;

    let finished = false;
    await playSendAnimation({
      messageID: 'msg-reduced',
      mainEl,
      transcriptEl,
      onFinish: () => { finished = true; },
    });

    expect(finished).toBe(true);
    expect(isFlying('msg-reduced')).toBe(false);
    expect(mockAnimate).not.toHaveBeenCalled();
    expect(mainEl.querySelector('.ai-chat-flight-bubble')).toBeNull();

    window.matchMedia = originalMatchMedia;
  });

  it('cancels active flights and cleans up elements when cancelAllFlights is called', async () => {
    const mainEl = document.createElement('div');
    host.appendChild(mainEl);
    vi.spyOn(mainEl, 'getBoundingClientRect').mockReturnValue({
      top: 0, left: 0, width: 500, height: 600, right: 500, bottom: 600,
    } as DOMRect);

    const transcriptEl = document.createElement('div');
    mainEl.appendChild(transcriptEl);

    const msgEl = document.createElement('div');
    msgEl.setAttribute('data-message-id', 'msg-cancel');
    const bubbleEl = document.createElement('div');
    bubbleEl.className = 'ai-bubble';
    bubbleEl.textContent = 'Will be cancelled';
    msgEl.appendChild(bubbleEl);
    transcriptEl.appendChild(msgEl);

    vi.spyOn(bubbleEl, 'getBoundingClientRect').mockReturnValue({
      top: 200, left: 300, width: 100, height: 30, right: 400, bottom: 230,
    } as DOMRect);

    const composerEl = document.createElement('div');
    vi.spyOn(composerEl, 'getBoundingClientRect').mockReturnValue({
      top: 500, left: 50, width: 400, height: 40, right: 450, bottom: 540,
    } as DOMRect);

    const cancelMock = vi.fn();
    Element.prototype.animate = vi.fn().mockReturnValue({
      cancel: cancelMock,
      finish: vi.fn(),
      set onfinish(_: any) {},
    });

    await playSendAnimation({
      messageID: 'msg-cancel',
      mainEl,
      transcriptEl,
      composerInputEl: composerEl,
    });

    expect(isFlying('msg-cancel')).toBe(true);
    expect(mainEl.querySelector('.ai-chat-flight-bubble')).not.toBeNull();

    cancelAllFlights();

    expect(cancelMock).toHaveBeenCalled();
    expect(isFlying('msg-cancel')).toBe(false);
    expect(mainEl.querySelector('.ai-chat-flight-bubble')).toBeNull();
  });

  it('ChatMessage binds ai-msg-flying class and data-message-id when in flight', async () => {
    flyingIDs.value = ['msg-test'];

    app = createApp({
      render() {
        return h(ChatMessage, {
          message: {
            id: 'msg-test',
            seq: 1,
            role: 'user',
            content: 'Flying message',
            created_at: Date.now(),
          },
        });
      },
    });
    app.mount(host);
    await nextTick();

    const el = host.querySelector('.ai-msg');
    expect(el).not.toBeNull();
    expect(el?.getAttribute('data-message-id')).toBe('msg-test');
    expect(el?.classList.contains('ai-msg-flying')).toBe(true);

    // Remove from flying
    removeFlying('msg-test');
    await nextTick();

    expect(el?.classList.contains('ai-msg-flying')).toBe(false);
  });

  it('ChatSurface coordinates send flight and handles user message dispatch', async () => {
    const { models, selectedID } = await import('../src/chat/useModels');
    models.value = [{ id: 'test-model', display_name: 'Test Model' } as any];
    selectedID.value = 'test-model';

    app = createApp({
      setup() {
        providePanelHost(ref(host));
        return () => h(ChatSurface);
      },
    });
    app.mount(host);
    await nextTick();

    const main = host.querySelector<HTMLElement>('.ai-chat-main')!;
    expect(main).not.toBeNull();

    // Mock dimensions
    vi.spyOn(main, 'getBoundingClientRect').mockReturnValue({
      top: 50, left: 100, width: 600, height: 800, right: 700, bottom: 850,
    } as DOMRect);

    const input = host.querySelector<HTMLTextAreaElement>('.ai-chat-input')!;
    expect(input).not.toBeNull();
    vi.spyOn(input, 'getBoundingClientRect').mockReturnValue({
      top: 750, left: 150, width: 480, height: 40, right: 630, bottom: 790,
    } as DOMRect);

    const animateMock = vi.fn().mockReturnValue({
      cancel: vi.fn(),
      finish: vi.fn(),
      set onfinish(cb: () => void) {
        setTimeout(cb, 10);
      },
    });
    Element.prototype.animate = animateMock;

    // Type a message and send
    input.value = 'Test hello';
    input.dispatchEvent(new Event('input'));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));

    await nextTick();
    await nextTick();

    // Messages now has the optimistic turn
    expect(messages.value.length).toBe(1);
    expect(messages.value[0]?.content).toBe('Test hello');
  });

  it('ChatMessage suppresses ai-msg-sent after flight completes to prevent double-animation flicker', async () => {
    justSentID.value = 'msg-flicker';
    flyingIDs.value = ['msg-flicker'];

    app = createApp({
      render() {
        return h(ChatMessage, {
          message: {
            id: 'msg-flicker',
            seq: 1,
            role: 'user',
            content: 'No double animation',
            created_at: Date.now(),
          },
        });
      },
    });
    app.mount(host);
    await nextTick();

    const el = host.querySelector('.ai-msg')!;
    expect(el.classList.contains('ai-msg-flying')).toBe(true);
    expect(el.classList.contains('ai-msg-sent')).toBe(false);

    // When flight finishes
    removeFlying('msg-flicker');
    await nextTick();

    // Flight is over, and ai-msg-sent is suppressed by hasFlown
    expect(isFlying('msg-flicker')).toBe(false);
    expect(hasFlown('msg-flicker')).toBe(true);
    expect(el.classList.contains('ai-msg-flying')).toBe(false);
    expect(el.classList.contains('ai-msg-sent')).toBe(false);
  });

  it('aligns flight bubble near composer send button and handles multi-line vertical positioning', async () => {
    const mainEl = document.createElement('div');
    mainEl.className = 'ai-chat-main';
    host.appendChild(mainEl);

    vi.spyOn(mainEl, 'getBoundingClientRect').mockReturnValue({
      top: 0, left: 0, width: 800, height: 900, right: 800, bottom: 900,
    } as DOMRect);

    const transcriptEl = document.createElement('div');
    transcriptEl.className = 'ai-chat-transcript';
    mainEl.appendChild(transcriptEl);

    const msgEl = document.createElement('div');
    msgEl.className = 'ai-msg ai-msg-user';
    msgEl.setAttribute('data-message-id', 'msg-multiline');
    const bubbleEl = document.createElement('div');
    bubbleEl.className = 'ai-bubble';
    bubbleEl.textContent = 'Line 1\nLine 2\nLine 3';
    msgEl.appendChild(bubbleEl);
    transcriptEl.appendChild(msgEl);

    // Target bubble is on the right of the transcript
    vi.spyOn(bubbleEl, 'getBoundingClientRect').mockReturnValue({
      top: 400, left: 520, width: 200, height: 80, right: 720, bottom: 480,
    } as DOMRect);

    // Multi-line composer input
    const composerInputEl = document.createElement('textarea');
    mainEl.appendChild(composerInputEl);
    vi.spyOn(composerInputEl, 'getBoundingClientRect').mockReturnValue({
      top: 800, left: 100, width: 620, height: 80, right: 720, bottom: 880,
    } as DOMRect);

    let recordedKeyframes: Keyframe[] | null = null;
    Element.prototype.animate = vi.fn().mockImplementation((keyframes) => {
      recordedKeyframes = keyframes;
      return { cancel: vi.fn(), finish: vi.fn(), set onfinish(cb: any) { cb(); } };
    });

    await playSendAnimation({
      messageID: 'msg-multiline',
      mainEl,
      transcriptEl,
      composerInputEl,
    });

    expect(recordedKeyframes).not.toBeNull();
    const startTransform = (recordedKeyframes![0] as Keyframe).transform as string;
    const match = /translate3d\(([^p]+)px,\s*([^p]+)px/.exec(startTransform);
    expect(match).not.toBeNull();
    const deltaX = parseFloat(match?.[1] ?? '0');
    const deltaY = parseFloat(match?.[2] ?? '0');

    // targetCenterX is 520 + 100 = 620.
    // In composer: maxCenterX is 720 - 10 - 100 = 610.
    // deltaX = 610 - 620 = -10 (close to destination on the right, not skewed to the far left)
    expect(Math.abs(deltaX)).toBeLessThanOrEqual(20);

    // targetCenterY is 400 + 40 = 440.
    // composer centerY is 800 + 40 = 840.
    // deltaY = 840 - 440 = 400 (starts cleanly centered in composer, not 28px above it)
    expect(deltaY).toBe(400);
  });

  it('supports compound flight animation for messages with both image attachments and text', async () => {
    const mainEl = document.createElement('div');
    mainEl.className = 'ai-chat-main';
    host.appendChild(mainEl);

    vi.spyOn(mainEl, 'getBoundingClientRect').mockReturnValue({
      top: 0, left: 0, width: 800, height: 900, right: 800, bottom: 900,
    } as DOMRect);

    const transcriptEl = document.createElement('div');
    mainEl.appendChild(transcriptEl);

    const msgEl = document.createElement('div');
    msgEl.className = 'ai-msg ai-msg-user';
    msgEl.setAttribute('data-message-id', 'msg-compound');

    const attachEl = document.createElement('div');
    attachEl.className = 'ai-chat-attachments';
    const imgEl = document.createElement('div');
    imgEl.className = 'ai-chat-attachment';
    attachEl.appendChild(imgEl);
    msgEl.appendChild(attachEl);

    const bubbleEl = document.createElement('div');
    bubbleEl.className = 'ai-bubble';
    bubbleEl.textContent = 'Image caption';
    msgEl.appendChild(bubbleEl);
    transcriptEl.appendChild(msgEl);

    vi.spyOn(attachEl, 'getBoundingClientRect').mockReturnValue({
      top: 350, left: 600, width: 120, height: 60, right: 720, bottom: 410,
    } as DOMRect);
    vi.spyOn(bubbleEl, 'getBoundingClientRect').mockReturnValue({
      top: 416, left: 560, width: 160, height: 36, right: 720, bottom: 452,
    } as DOMRect);

    const composerInputEl = document.createElement('textarea');
    mainEl.appendChild(composerInputEl);
    vi.spyOn(composerInputEl, 'getBoundingClientRect').mockReturnValue({
      top: 800, left: 100, width: 620, height: 50, right: 720, bottom: 850,
    } as DOMRect);

    let createdCompound: HTMLElement | null = null;
    Element.prototype.animate = vi.fn().mockImplementation(function (this: HTMLElement) {
      createdCompound = this;
      return { cancel: vi.fn(), finish: vi.fn(), set onfinish(cb: any) { cb(); } };
    });

    await playSendAnimation({
      messageID: 'msg-compound',
      mainEl,
      transcriptEl,
      composerInputEl,
    });

    expect(createdCompound).not.toBeNull();
    expect(createdCompound!.className).toContain('ai-chat-flight-compound');
    expect(createdCompound!.querySelector('.ai-chat-attachments')).not.toBeNull();
    expect(createdCompound!.querySelector('.ai-bubble')).not.toBeNull();
  });

  it('markFlying synchronously flags message as flying before DOM update', () => {
    expect(isFlying('msg-sync-1')).toBe(false);
    markFlying('msg-sync-1');
    expect(isFlying('msg-sync-1')).toBe(true);
    expect(hasFlown('msg-sync-1')).toBe(false);
  });

  it('ChatMessage stationary bubble is marked ai-msg-flying immediately when id === justSentID && !hasFlown(id), before animation starts', async () => {
    // Even if flyingIDs has NOT been set yet, justSentID marks the stationary bubble as flying
    justSentID.value = 'msg-imm-hide';
    expect(flyingIDs.value.includes('msg-imm-hide')).toBe(false);

    app = createApp({
      render() {
        return h(ChatMessage, {
          message: {
            id: 'msg-imm-hide',
            seq: 1,
            role: 'user',
            content: 'Should be hidden immediately',
            created_at: Date.now(),
          },
        });
      },
    });
    app.mount(host);
    await nextTick();

    const el = host.querySelector('.ai-msg')!;
    expect(el.classList.contains('ai-msg-flying')).toBe(true);
    expect(el.classList.contains('ai-msg-sent')).toBe(false);

    // After flight completes, stationary bubble becomes visible and is marked flown
    removeFlying('msg-imm-hide');
    await nextTick();

    expect(isFlying('msg-imm-hide')).toBe(false);
    expect(hasFlown('msg-imm-hide')).toBe(true);
    expect(el.classList.contains('ai-msg-flying')).toBe(false);
    expect(el.classList.contains('ai-msg-sent')).toBe(false);
  });

  it('cancelAllFlights marks in-flight messages as flown so stationary bubble reveals if cancelled', () => {
    markFlying('msg-cancel-flown');
    expect(isFlying('msg-cancel-flown')).toBe(true);
    expect(hasFlown('msg-cancel-flown')).toBe(false);

    cancelAllFlights();

    expect(isFlying('msg-cancel-flown')).toBe(false);
    expect(hasFlown('msg-cancel-flown')).toBe(true);
  });

  it('resetFlightState clears both in-flight and flown tracking', () => {
    markFlying('msg-reset-test');
    removeFlying('msg-reset-test');
    expect(hasFlown('msg-reset-test')).toBe(true);

    resetFlightState();
    expect(hasFlown('msg-reset-test')).toBe(false);
    expect(isFlying('msg-reset-test')).toBe(false);
  });

  it('markFlying marks message as flown immediately when prefers-reduced-motion is active', () => {
    const originalMatchMedia = window.matchMedia;
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: query.includes('prefers-reduced-motion: reduce'),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })) as any;

    markFlying('msg-rm-sync');
    expect(isFlying('msg-rm-sync')).toBe(false);
    expect(hasFlown('msg-rm-sync')).toBe(true);

    window.matchMedia = originalMatchMedia;
  });

  it('verifies .ai-chat-spinner styling avoids sub-pixel wobble', async () => {
    const fs = await import('node:fs');
    const path = await import('node:path');
    const scssPath = path.resolve(__dirname, '../src/styles/_chat.scss');
    const content = fs.readFileSync(scssPath, 'utf8');

    // Matches standalone .ai-chat-spinner block
    const spinnerMatch = content.match(/(?:^|\n)\.ai-chat-spinner\s*\{([^}]+)\}/);
    expect(spinnerMatch).not.toBeNull();
    const spinnerBody = spinnerMatch![1]!;

    expect(spinnerBody).toMatch(/display:\s*inline-block/);
    expect(spinnerBody).toMatch(/box-sizing:\s*border-box/);
    expect(spinnerBody).toMatch(/width:\s*14px/);
    expect(spinnerBody).toMatch(/height:\s*14px/);
    expect(spinnerBody).toMatch(/transform-origin:\s*center center/);

  });

  it('markFlying clears previous flown entry for the same ID so retries remain hidden in flight', () => {
    markFlying('msg-re-fly');
    removeFlying('msg-re-fly');
    expect(hasFlown('msg-re-fly')).toBe(true);

    markFlying('msg-re-fly');
    expect(isFlying('msg-re-fly')).toBe(true);
    expect(hasFlown('msg-re-fly')).toBe(false);
  });

  it('sets initial transform and opacity on clone before insertion to prevent destination flash', async () => {
    const mainEl = document.createElement('div');
    mainEl.className = 'ai-chat-main';
    host.appendChild(mainEl);

    vi.spyOn(mainEl, 'getBoundingClientRect').mockReturnValue({
      top: 0, left: 0, width: 800, height: 800, right: 800, bottom: 800,
    } as DOMRect);

    const transcriptEl = document.createElement('div');
    transcriptEl.className = 'ai-chat-transcript';
    mainEl.appendChild(transcriptEl);

    const msgEl = document.createElement('div');
    msgEl.className = 'ai-msg ai-msg-user';
    msgEl.setAttribute('data-message-id', 'msg-flash-check');
    const bubbleEl = document.createElement('div');
    bubbleEl.className = 'ai-bubble';
    bubbleEl.textContent = 'No flash';
    msgEl.appendChild(bubbleEl);
    transcriptEl.appendChild(msgEl);

    vi.spyOn(bubbleEl, 'getBoundingClientRect').mockReturnValue({
      top: 400, left: 500, width: 100, height: 40, right: 600, bottom: 440,
    } as DOMRect);

    const composerEl = document.createElement('textarea');
    vi.spyOn(composerEl, 'getBoundingClientRect').mockReturnValue({
      top: 700, left: 200, width: 400, height: 40, right: 600, bottom: 740,
    } as DOMRect);

    let observedTransformAtAppend: string | undefined;
    let observedOpacityAtAppend: string | undefined;

    const originalAppendChild = mainEl.appendChild.bind(mainEl);
    vi.spyOn(mainEl, 'appendChild').mockImplementation((node: Node) => {
      if ((node as HTMLElement).classList?.contains('ai-chat-flight-bubble')) {
        observedTransformAtAppend = (node as HTMLElement).style.transform;
        observedOpacityAtAppend = (node as HTMLElement).style.opacity;
      }
      return originalAppendChild(node);
    });

    Element.prototype.animate = vi.fn().mockReturnValue({
      cancel: vi.fn(),
      finish: vi.fn(),
      set onfinish(cb: () => void) { cb(); },
    });

    await playSendAnimation({
      messageID: 'msg-flash-check',
      mainEl,
      transcriptEl,
      composerInputEl: composerEl,
    });

    expect(observedTransformAtAppend).toBeDefined();
    expect(observedTransformAtAppend).toContain('translate3d(');
    expect(observedTransformAtAppend).toContain('scale(1.18)');
    expect(observedOpacityAtAppend).toBe('0.95');
  });
});
