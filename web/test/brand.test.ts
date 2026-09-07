import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import AppShell from '../src/layouts/AppShell.vue';
import LandingView from '../src/views/LandingView.vue';
import { t } from '../src/composables/useI18n';

// The brand is the way back to the conversation from every other screen, and
// it has to stay an ordinary link: a <button> would not open in a new tab,
// would not show its destination on hover, and would not be copyable. These
// assertions exist because it has been a button twice.

let app: App | null = null;
let host: HTMLElement;

function mount(component: Parameters<typeof createApp>[0], props: Record<string, unknown> = {}): void {
  app = createApp({ render: () => h(component, props) });
  app.use(createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:all(.*)*', component: { render: () => null } }],
  }));
  app.mount(host);
}

function brand(): HTMLAnchorElement {
  const node = host.querySelector<HTMLAnchorElement>('a.oa-brand');
  if (!node) throw new Error('no brand rendered');
  return node;
}

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = null;
  host.remove();
});

describe('brand home navigation', () => {
  it('renders the brand as an anchor to / carrying the back-to-chat title', () => {
    mount(AppShell);
    expect(brand().getAttribute('href')).toBe('/');
    expect(brand().title).toBe(t('backToChat'));
  });

  it('renders the landing page brand as an anchor to /', () => {
    mount(LandingView);
    const link = host.querySelector<HTMLAnchorElement>('a.oa-landing-brand');
    expect(link).not.toBeNull();
    expect(link?.getAttribute('href')).toBe('/');
  });

  it('claims a plain left click and hands it to the screen around it', () => {
    const onBrand = vi.fn();
    mount(AppShell, { onBrand });

    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    brand().dispatchEvent(event);

    expect(onBrand).toHaveBeenCalledOnce();
    // Claimed, so the browser does not also follow the href and reload.
    expect(event.defaultPrevented).toBe(true);
  });

  it('leaves a modified click alone, so open-in-new-tab still works', () => {
    const onBrand = vi.fn();
    mount(AppShell, { onBrand });

    const event = new MouseEvent('click', {
      bubbles: true, cancelable: true, button: 0, ctrlKey: true,
    });
    brand().dispatchEvent(event);

    expect(onBrand).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
  });

  it('leaves a middle click alone, which is the other way to open a tab', () => {
    const onBrand = vi.fn();
    mount(AppShell, { onBrand });

    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 1 });
    brand().dispatchEvent(event);

    expect(onBrand).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
  });
});
