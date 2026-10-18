// The trial on the front door, as a visitor meets it when an exchange is refused.
//
// The server counts only the exchanges that went through. A refused question and
// the error it was answered with must not be sent back to the model as if it had
// been said, and the questions that did go through must still be there.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { ApiError } from '@/api/client';
import * as trialApi from '@/api/trial';
import { changeLanguage, t } from '@/composables/useI18n';
import { site, siteInfo } from '@/stores/session';
import LandingView from '@/views/LandingView.vue';
import { settle } from './helpers';

// The notice is not what this checks, and it reads the announcements API.
vi.mock('@/announce/HomeNotice.vue', () => ({ default: { render: () => null } }));

let app: App | null = null;
let host: HTMLElement;

async function mount(): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:p(.*)*', component: { render: () => null } }],
  });
  await router.push('/');
  await router.isReady();
  app = createApp({ render: () => h(LandingView) });
  app.use(router).mount(host);
  await settle();
}

async function ask(text: string): Promise<void> {
  const input = host.querySelector<HTMLInputElement>('.oa-landing-composer input')!;
  input.value = text;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  await nextTick();
  host.querySelector<HTMLFormElement>('.oa-landing-composer')!.dispatchEvent(
    new Event('submit', { bubbles: true, cancelable: true }),
  );
  await settle();
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.append(host);
  site.value = {
    ...siteInfo.value,
    landing: { mode: 'chat', intro: '', trial: true, trial_turns: 3 },
  };
});

afterEach(() => {
  app?.unmount();
  app = null;
  site.value = null;
  host.remove();
  vi.restoreAllMocks();
});

describe('the trial after a refused exchange', () => {
  it('says a refused trial turn in Chinese by its code, not in the server\'s English', async () => {
    await changeLanguage('zh');
    vi.spyOn(trialApi, 'streamTrial').mockRejectedValueOnce(
      new ApiError(429, 'trial_exhausted', 'You have used the trial for now.'));
    await mount();

    await ask('a question');

    expect(host.textContent).toContain(t('trialExhausted'));
    expect(host.textContent).not.toContain('You have used the trial');
    await changeLanguage('en');
  });

  it('says a server fault in Chinese with the trial\'s own line, never the server sentence', async () => {
    await changeLanguage('zh');
    vi.spyOn(trialApi, 'streamTrial').mockRejectedValueOnce(
      new ApiError(500, 'internal', 'Something went wrong on our side.'));
    await mount();

    await ask('a question');

    expect(host.textContent).toContain(t('trialFailed'));
    expect(host.textContent).not.toContain('Something went wrong');
    await changeLanguage('en');
  });

  it('sends the exchanges that went through, and leaves a refused one and its error out', async () => {
    const sent: Array<Array<{ role: string; content: string }>> = [];
    vi.spyOn(trialApi, 'streamTrial')
      .mockImplementationOnce(async (messages, _continuation, onDelta) => {
        sent.push(messages);
        onDelta('the first answer');
        return { continuation: 'c1', turnsLeft: 2 };
      })
      .mockImplementationOnce(async (messages) => {
        sent.push(messages);
        throw new ApiError(502, 'upstream', 'The model did not answer.', {});
      })
      .mockImplementationOnce(async (messages, _continuation, onDelta) => {
        sent.push(messages);
        onDelta('the third answer');
        return { continuation: 'c3', turnsLeft: 1 };
      });
    await mount();

    await ask('first question');
    await ask('second question');
    await ask('third question');

    expect(sent[0]).toEqual([{ role: 'user', content: 'first question' }]);
    expect(sent[1]).toEqual([
      { role: 'user', content: 'first question' },
      { role: 'assistant', content: 'the first answer' },
      { role: 'user', content: 'second question' },
    ]);
    expect(sent[2]).toEqual([
      { role: 'user', content: 'first question' },
      { role: 'assistant', content: 'the first answer' },
      { role: 'user', content: 'third question' },
    ]);
  });
});

describe('the trial when the visitor leaves mid-answer', () => {
  it('ends an answer still arriving when the visitor leaves the page', async () => {
    let signal: AbortSignal | undefined;
    vi.spyOn(trialApi, 'streamTrial').mockImplementationOnce((_messages, _continuation, _onDelta, given) => {
      signal = given;
      return new Promise<never>(() => {});
    });
    await mount();

    await ask('a question');
    expect(signal).toBeDefined();
    expect(signal!.aborted).toBe(false);

    app!.unmount();
    app = null;
    expect(signal!.aborted).toBe(true);
  });
});
