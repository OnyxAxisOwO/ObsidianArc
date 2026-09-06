import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderShell } from '../src/app/shell';
import { renderLandingPage } from '../src/landing/landing-page';
import { t } from '../src/i18n';

describe('brand home navigation', () => {
  let root: HTMLElement;

  beforeEach(() => {
    root = document.createElement('div');
    document.body.appendChild(root);
  });

  afterEach(() => {
    root.remove();
  });

  it('renders brand as an anchor link to / with backToChat title', () => {
    const shell = renderShell(root);
    expect(shell.brand.tagName).toBe('A');
    expect(shell.brand.getAttribute('href')).toBe('/');
    expect(shell.brand.title).toBe(t('backToChat'));
    expect(shell.brand.classList.contains('oa-brand')).toBe(true);
  });

  it('renders landing page brand as an anchor link to /', () => {
    renderLandingPage(root);
    const brand = root.querySelector<HTMLAnchorElement>('a.oa-landing-brand');
    expect(brand).not.toBeNull();
    expect(brand?.getAttribute('href')).toBe('/');
  });

  it('triggers new conversation when clicked while already on /', () => {
    const shell = renderShell(root);
    const newConversation = vi.fn();

    // Replicate the listener attached in chat-page.ts
    shell.brand.addEventListener('click', (event) => {
      if (event.defaultPrevented || event.button !== 0) return;
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      if (window.location.pathname === '/' && !window.location.search) {
        event.preventDefault();
        newConversation();
      }
    });

    window.history.pushState(null, '', '/');
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    shell.brand.dispatchEvent(event);

    expect(newConversation).toHaveBeenCalledOnce();
    expect(event.defaultPrevented).toBe(true);
  });

  it('allows default navigation when clicked while on another path', () => {
    const shell = renderShell(root);
    const newConversation = vi.fn();

    shell.brand.addEventListener('click', (event) => {
      if (event.defaultPrevented || event.button !== 0) return;
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      if (window.location.pathname === '/' && !window.location.search) {
        event.preventDefault();
        newConversation();
      }
    });

    window.history.pushState(null, '', '/settings');
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    shell.brand.dispatchEvent(event);

    expect(newConversation).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
  });

  it('allows default navigation when clicked with modifier key', () => {
    const shell = renderShell(root);
    const newConversation = vi.fn();

    shell.brand.addEventListener('click', (event) => {
      if (event.defaultPrevented || event.button !== 0) return;
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      if (window.location.pathname === '/' && !window.location.search) {
        event.preventDefault();
        newConversation();
      }
    });

    window.history.pushState(null, '', '/');
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, ctrlKey: true });
    shell.brand.dispatchEvent(event);

    expect(newConversation).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
  });
});
