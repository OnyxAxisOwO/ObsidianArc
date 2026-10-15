import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import ChatPending from '../src/chat/ChatPending.vue';
import { currentPreferences, currentUser } from '../src/stores/session';

let app: App | null = null;

function setStats(show: boolean): void {
  currentPreferences.value = { ...currentPreferences.value, show_stats: show };
}

afterEach(() => {
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.restoreAllMocks();
  setStats(false);
  currentUser.value = null;
});

describe('the elapsed clock under a streaming answer', () => {
  it('starts when the reader turns stats on mid-stream, and stops when they turn them off', async () => {
    setStats(false);
    const setSpy = vi.spyOn(window, 'setInterval');
    const clearSpy = vi.spyOn(window, 'clearInterval');
    const host = document.createElement('div');
    document.body.appendChild(host);
    const pending = { answer: '', reasoning: '', startedAt: Date.now() };
    app = createApp({ render: () => h(ChatPending, { pending }) });
    app.mount(host);

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
    const host = document.createElement('div');
    document.body.appendChild(host);
    const pending = { answer: '', reasoning: '', startedAt: Date.now() };
    app = createApp({ render: () => h(ChatPending, { pending }) });
    app.mount(host);

    expect(host.querySelector('.ai-msg-timer')).not.toBeNull();
    app.unmount();
    app = null;
    expect(clearSpy).toHaveBeenCalled();
  });
});
