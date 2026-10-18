import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import RootView from '../src/views/RootView.vue';
import FrontPage from '../src/views/FrontPage.vue';
import { site, siteInfo, forget } from '../src/stores/session';
import { changeLanguage, t, type StringKey } from '../src/composables/useI18n';
import { zh } from '../src/i18n.zh';

// The public front page.
//
// Two things are worth a test here and the rest is presentation. The first is
// the split: a marketing page that landed on the first paint of everyone
// opening the chat would be exactly the regression `bundle.test.ts` exists to
// catch, and the only thing holding it out of the entry is that RootView
// reaches it through `defineAsyncComponent`. The second is the motion: this
// page moves more than any other, and the reduced-motion path is the one that
// can fail invisibly — every reveal starts at `opacity: 0`, so a broken
// switch leaves a reader with a blank page rather than a still one.

// `vi.hoisted`, because the mock factory below is lifted above every `const`
// in this file — including the spy it needs.
const { push } = vi.hoisted(() => ({ push: vi.fn() }));
// RootView pulls in the chat layout, which reaches the notifications store,
// which imports the real router at module scope — so the mock has to satisfy
// that construction as well as the two components' own `useRouter`.
vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
  useRoute: () => ({ path: '/', fullPath: '/', query: {}, matched: [] }),
  createRouter: () => ({
    beforeEach: () => {},
    afterEach: () => {},
    push,
    replace: vi.fn(),
    isReady: () => Promise.resolve(),
    currentRoute: { value: { path: '/', fullPath: '/', query: {}, matched: [] } },
  }),
  createWebHistory: () => ({}),
}));

let app: App | undefined;
let host: HTMLElement;
let reduced = false;

function stubMatchMedia(): void {
  // usePreferredReducedMotion reads matchMedia, which jsdom does not
  // implement. The listener pair has to be here: vueuse subscribes on mount
  // and a missing addEventListener throws before anything renders.
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: reduced && query.includes('reduce'),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}

/** One IntersectionObserver the page created, as the stub recorded it. */
interface StubObserver {
  callback: IntersectionObserverCallback;
  targets: Element[];
  disconnected: boolean;
}

let observers: StubObserver[] = [];

beforeEach(async () => {
  await changeLanguage('en');
  reduced = false;
  push.mockClear();
  stubMatchMedia();
  // jsdom has no IntersectionObserver. The page makes three: one for the
  // reveals, one that pauses the hero and the flow off screen, and one that
  // stops the terminal demo typing. The stub records each separately, with its
  // callback, which is what lets a test tell them apart and drive any one by
  // hand.
  observers = [];
  vi.stubGlobal('IntersectionObserver', class {
    private readonly record: StubObserver;
    constructor(callback: IntersectionObserverCallback) {
      this.record = { callback, targets: [], disconnected: false };
      observers.push(this.record);
    }
    observe(target: Element): void { this.record.targets.push(target); }
    unobserve(): void {}
    disconnect(): void { this.record.disconnected = true; }
  });
  host = document.createElement('div');
  document.body.append(host);
});

/** Every element any observer was handed, across all of them. */
function observedTargets(): Element[] {
  return observers.flatMap((entry) => entry.targets);
}

/** The observer watching a given element, if there is one. */
function observerOf(target: Element | null): StubObserver | undefined {
  return observers.find((entry) => target !== null && entry.targets.includes(target));
}

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  site.value = null;
  forget();
});

async function mount(component: Parameters<typeof createApp>[0]): Promise<void> {
  app = createApp(component);
  app.mount(host);
  await nextTick();
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe('the front page', () => {
  it('is what the address draws when the operator picked it, and is not in the entry graph', async () => {
    site.value = {
      ...siteInfo.value,
      registration_enabled: true,
      landing: { mode: 'site', intro: '', trial: false, trial_turns: 0 },
    };
    await mount(RootView);

    // RootView reaches the page through defineAsyncComponent, so it is not
    // there on the first tick — which is the whole point, and the reason this
    // waits rather than asserting immediately.
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();

    expect(host.querySelector('.oa-front')).not.toBeNull();
    // The operator's own page and the trial page are the other two front
    // doors; drawing either alongside this one would mean two front doors.
    expect(host.querySelector('.oa-landing-chat')).toBeNull();
    expect(host.querySelector('.oa-landing-intro')).toBeNull();
  });

  it('leaves the other landing modes alone', async () => {
    site.value = {
      ...siteInfo.value,
      landing: { mode: 'intro', intro: '', trial: false, trial_turns: 0 },
    };
    await mount(RootView);

    // The landing page is fetched like the front page, so it is not there on
    // the first tick, and how long the chunk takes depends on which test
    // loaded it first. Waiting on the element rather than on a timer keeps
    // this from passing or failing by order.
    await vi.waitFor(() => expect(host.querySelector('.oa-landing')).not.toBeNull());

    expect(host.querySelector('.oa-front')).toBeNull();
  });

  it('hands every section to the observer rather than revealing them itself', async () => {
    await mount(FrontPage);

    const reveals = [...host.querySelectorAll('.oa-front-reveal')];
    const revealer = observerOf(reveals[0] ?? null);

    expect(reveals.length).toBeGreaterThan(0);
    // One observer holds every reveal. The hero's own observer is a second
    // one, and it must not be handed a reveal — it toggles, and a reveal
    // toggled back off as the reader scrolled past would strobe.
    expect(revealer?.targets).toEqual(reveals);
    // Nothing may arrive already revealed: the class is applied on
    // intersection, and a section that shipped with it would appear before
    // the reader scrolled to it.
    expect(host.querySelectorAll('.oa-front-reveal.shown')).toHaveLength(0);
  });

  it('shows everything at once when the reader asked for less motion', async () => {
    reduced = true;
    stubMatchMedia();
    await mount(FrontPage);

    const page = host.querySelector('.oa-front')!;
    expect(page.classList.contains('still')).toBe(true);

    // The critical half. Every reveal rests at `opacity: 0` and the observer
    // is what takes that away — so switching the observer off without also
    // marking them shown would leave a reader who asked for no motion with a
    // page that has no content on it.
    const reveals = host.querySelectorAll('.oa-front-reveal');
    expect(reveals.length).toBeGreaterThan(0);
    expect(host.querySelectorAll('.oa-front-reveal.shown')).toHaveLength(reveals.length);
    expect(observedTargets()).toHaveLength(0);

    // The counters and the terminal mock animate too, and both have a value
    // worth showing when they do not.
    const figures = [...host.querySelectorAll('.oa-front-fact strong')].map((el) => el.textContent);
    expect(figures).toEqual(['1', '0', '4', '2']);
    expect(host.querySelector('.oa-front-term-line')?.textContent).toContain('model list');
    expect(host.querySelector('.oa-front-caret')).toBeNull();
  });

  /** Moves a pointer over `target`, with the hero given a real box. */
  function pointAt(target: Element, x: number, y: number): HTMLElement {
    const hero = host.querySelector<HTMLElement>('.oa-front-hero')!;
    // jsdom lays nothing out, so every box is 0×0 — which the handler
    // rightly refuses to divide by. Give the hero the size a screen would.
    hero.getBoundingClientRect = () => ({
      left: 0, top: 0, right: 1000, bottom: 800, width: 1000, height: 800, x: 0, y: 0,
      toJSON: () => ({}),
    });
    target.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientX: x, clientY: y }));
    return hero;
  }

  it('follows the pointer across the middle of the hero, not only its margins', async () => {
    await mount(FrontPage);

    // The regression this pins: the handler used to sit on the background
    // layer, and the hero's text covers the whole middle of that layer — so a
    // pointer over the headline or the buttons never reached it, and the
    // parallax only ever answered at the two side margins. A pointer over the
    // headline is exactly the case that was broken.
    const headline = host.querySelector('.oa-front-title')!;
    const hero = pointAt(headline, 750, 200);

    expect(hero.style.getPropertyValue('--oa-front-px')).toBe('0.75');
    expect(hero.style.getPropertyValue('--oa-front-py')).toBe('0.25');
    // The glow needs pixels, not fractions: it sits under the pointer
    // whatever the hero's size.
    expect(hero.style.getPropertyValue('--oa-front-gx')).toBe('750px');
    expect(hero.style.getPropertyValue('--oa-front-gy')).toBe('200px');
  });

  it('does not follow the pointer for a reader who asked for less motion', async () => {
    reduced = true;
    stubMatchMedia();
    await mount(FrontPage);

    const hero = pointAt(host.querySelector('.oa-front-title')!, 750, 200);

    // Following is motion. Nothing is written, so every field stays at the
    // stylesheet's centred default rather than chasing the pointer.
    expect(hero.style.getPropertyValue('--oa-front-px')).toBe('');
    expect(hero.style.getPropertyValue('--oa-front-gx')).toBe('');
  });

  it('pauses the hero background once it is scrolled away, and resumes it on the way back', async () => {
    await mount(FrontPage);

    const hero = host.querySelector('.oa-front-hero')!;
    const watcher = observerOf(hero);
    expect(watcher).toBeDefined();

    const report = (isIntersecting: boolean): void => {
      watcher!.callback(
        [{ isIntersecting, target: hero } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    };

    // A switch, not a one-way door like the reveals: it has to come back on,
    // or a reader who scrolls up finds a frozen background.
    report(false);
    expect(hero.classList.contains('idle')).toBe(true);
    report(true);
    expect(hero.classList.contains('idle')).toBe(false);

    // And it goes when the page does, rather than holding a reference to a
    // hero that no longer exists.
    app?.unmount();
    app = undefined;
    expect(watcher!.disconnected).toBe(true);
  });

  it('pauses the flow diagram\'s wires once it is scrolled away, and resumes them on the way back', async () => {
    await mount(FrontPage);

    // The wires and the core ring animate background position and rotation,
    // and sit below the hero, so the hero's own switch never reaches them. They
    // share the hero's observer, which is why the same watcher answers here.
    const flow = host.querySelector('.oa-front-flow')!;
    const watcher = observerOf(flow);
    expect(watcher).toBeDefined();

    const report = (isIntersecting: boolean): void => {
      watcher!.callback(
        [{ isIntersecting, target: flow } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    };

    report(false);
    expect(flow.classList.contains('idle')).toBe(true);
    report(true);
    expect(flow.classList.contains('idle')).toBe(false);
  });

  it('offers a way in, and only offers sign-up where sign-up is open', async () => {
    site.value = { ...siteInfo.value, registration_enabled: false };
    await mount(FrontPage);

    const labels = () => [...host.querySelectorAll('button')].map((b) => b.textContent?.trim());
    expect(labels()).toContain(t('frontOpenChat'));
    expect(labels()).not.toContain(t('frontCreateAccount'));

    const signIn = [...host.querySelectorAll('button')]
      .find((b) => b.textContent?.trim() === t('frontOpenChat'))!;
    signIn.click();
    expect(push).toHaveBeenCalledWith('/login');
  });

  // The hero's and the closing card's are one definition drawn twice; the
  // header's is a lighter button of its own.
  it('draws the sign-up call to action under the headline and on the closing card', async () => {
    site.value = { ...siteInfo.value, registration_enabled: true };
    await mount(FrontPage);

    const ctas = [...host.querySelectorAll<HTMLButtonElement>('.oa-btn.primary.oa-front-cta')];
    expect(ctas).toHaveLength(2);
    for (const cta of ctas) {
      expect(cta.textContent?.trim()).toBe(t('frontCreateAccount'));
      expect(cta.querySelector('svg')).not.toBeNull();
    }
    expect(ctas[0]!.closest('.oa-front-hero')).not.toBeNull();
    expect(ctas[1]!.closest('.oa-front-close-card')).not.toBeNull();

    ctas[1]!.click();
    expect(push).toHaveBeenCalledWith('/register');
  });

  it('says the same things in Chinese', async () => {
    // The dictionary's own type already forces every key to exist in both.
    // What it cannot see is a key that was translated by being copied, which
    // on a marketing page is the likely mistake — the sentences are long and
    // the temptation is to leave one.
    //
    // Read through `t()` in each language rather than against the English
    // object: `en` is deliberately not exported — it is the source of truth
    // for a type, not a value other modules read — and widening that for a
    // test would be the wrong trade.
    const keys = (Object.keys(zh) as StringKey[]).filter((key) => key.startsWith('front'));
    expect(keys.length).toBeGreaterThan(30);

    await changeLanguage('en');
    const english = new Map(keys.map((key) => [key, t(key)]));
    await changeLanguage('zh');

    const untranslated = keys.filter((key) => t(key) === english.get(key)
      // Protocol and product names stay English on purpose; this is the only
      // front-page string made entirely of them.
      && key !== 'frontFactProtocolsNote');
    expect(untranslated).toEqual([]);

    await mount(FrontPage);
    expect(host.querySelector('.oa-front-title')?.textContent).toContain(zh.frontHeroLineOne);
  });

  it('stops typing the terminal demo while its pane is off screen, and resumes it on the way back', async () => {
    await mount(FrontPage);

    const terminal = host.querySelector('.oa-front-term')!;
    const watcher = observerOf(terminal);
    expect(watcher).toBeDefined();
    const typedText = (): string => terminal.querySelector('.oa-front-term-line')!.children[1]!.textContent ?? '';
    const report = (isIntersecting: boolean): void => {
      watcher!.callback(
        [{ isIntersecting, target: terminal } as unknown as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    };
    const pause = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

    report(false);
    const frozen = typedText();
    await pause(200);
    // Nothing is scheduled, so nothing is typed and nothing redraws.
    expect(typedText()).toBe(frozen);

    report(true);
    await pause(200);
    expect(typedText().length).toBeGreaterThan(frozen.length);
  });

  it('moves the progress bar as the page scrolls, without re-rendering the page for it', async () => {
    await mount(FrontPage);

    const node = host.querySelector<HTMLElement>('.oa-front')!;
    // jsdom has no layout: give the scroller a size so there is somewhere to scroll.
    Object.defineProperty(node, 'scrollHeight', { configurable: true, value: 1000 });
    Object.defineProperty(node, 'clientHeight', { configurable: true, value: 500 });
    node.scrollTop = 250;
    node.dispatchEvent(new Event('scroll'));
    await nextTick();

    const bar = host.querySelector<HTMLElement>('.oa-front-progress')!;
    expect(bar.style.transform).toBe('scaleX(0.5)');

    // No await: the bar moves in the scroll handler itself, not on a render that
    // would have to run first.
    node.scrollTop = 500;
    node.dispatchEvent(new Event('scroll'));
    expect(bar.style.transform).toBe('scaleX(1)');
  });
});
