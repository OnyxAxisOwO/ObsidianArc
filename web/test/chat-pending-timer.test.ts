import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import ChatPending from '../src/chat/ChatPending.vue';
import { pending } from '../src/chat/useChat';
import { currentPreferences, currentUser } from '../src/stores/session';

let app: App | null = null;

function setStats(show: boolean): void {
  currentPreferences.value = { ...currentPreferences.value, show_stats: show };
}

// Not mountComponent: each test hands in its own render, slots and all, rather than one component and its props.
function mount(render: () => ReturnType<typeof h>): HTMLElement {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({ render });
  app.mount(host);
  return host;
}

afterEach(() => {
  app?.unmount();
  app = null;
  vi.restoreAllMocks();
  setStats(false);
  currentUser.value = null;
  pending.value = null;
});

describe('the elapsed clock under a streaming answer', () => {
  it('starts when the reader turns stats on mid-stream, and stops when they turn them off', async () => {
    setStats(false);
    const setSpy = vi.spyOn(window, 'setInterval');
    const clearSpy = vi.spyOn(window, 'clearInterval');
    pending.value = { answer: '', reasoning: '', startedAt: Date.now() };
    const host = mount(() => h(ChatPending));

    expect(host.querySelector('.ai-msg-timer')).toBeNull();
    expect(setSpy).not.toHaveBeenCalled();

    setStats(true);
    await nextTick();
    expect(host.querySelector('.ai-msg-timer')?.textContent).toMatch(/^\d+\.\d+s$/);
    expect(setSpy).toHaveBeenCalledTimes(1);

    setStats(false);
    await nextTick();
    expect(host.querySelector('.ai-msg-timer')).toBeNull();
    expect(clearSpy).toHaveBeenCalled();
  });

  it('runs from the start when stats are already on, and stops on unmount', () => {
    setStats(true);
    const clearSpy = vi.spyOn(window, 'clearInterval');
    pending.value = { answer: '', reasoning: '', startedAt: Date.now() };
    mount(() => h(ChatPending));

    expect(document.querySelector('.ai-msg-timer')).not.toBeNull();
    app!.unmount();
    app = null;
    expect(clearSpy).toHaveBeenCalled();
  });
});

describe('the streaming answer and the transcript around it', () => {
  it('redraws itself on each frame without the parent rendering again', async () => {
    // The parent stands in for the transcript: it decides whether the pending
    // block is there and reads nothing of what is in it.
    let parentRenders = 0;
    pending.value = { answer: 'a', reasoning: '', startedAt: Date.now() };
    const host = mount(() => {
      parentRenders += 1;
      return h('div', [h(ChatPending)]);
    });
    expect(parentRenders).toBe(1);

    for (const answer of ['ab', 'abc', 'abcd']) {
      pending.value = { answer, reasoning: '', startedAt: pending.value!.startedAt };
      await nextTick();
    }

    expect(host.querySelector('.ai-answer-streaming')?.textContent).toContain('abcd');
    expect(parentRenders).toBe(1);
  });

  it('survives the pending turn being cleared before it is taken away', async () => {
    setStats(true);
    pending.value = { answer: 'done', reasoning: '', startedAt: Date.now() };
    const host = mount(() => h(ChatPending));
    // The turn's end clears pending a moment before busy hides this block.
    pending.value = null;
    await nextTick();
    expect(host.querySelector('.ai-chat-pending')).not.toBeNull();
  });
});
