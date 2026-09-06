// The Cloudflare Turnstile widget, where an operator has switched one on.
//
// The one third-party script this interface loads, and it is loaded lazily:
// nothing is fetched on an instance with no challenge configured, which is
// every instance until somebody turns it on. The zero-dependency rule is
// about what ships in the bundle, and this ships in nothing — but it is a
// script from somebody else's server on the sign-in page, which is worth
// knowing and is why it is confined to this file and these two forms.

const SCRIPT = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';

interface TurnstileAPI {
  render(target: HTMLElement, options: Record<string, unknown>): string;
  reset(id?: string): void;
  remove(id: string): void;
}

declare global {
  interface Window {
    turnstile?: TurnstileAPI;
  }
}

let loading: Promise<TurnstileAPI | null> | null = null;

/**
 * Loads the script once per page, however many widgets ask for it.
 *
 * Resolves to null rather than rejecting when it cannot be fetched — an
 * instance behind a firewall that blocks Cloudflare should show a form that
 * says the challenge is unavailable, not a page that failed to render.
 */
function load(): Promise<TurnstileAPI | null> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (loading) return loading;

  loading = new Promise<TurnstileAPI | null>((resolve) => {
    const script = document.createElement('script');
    script.src = SCRIPT;
    script.async = true;
    script.onload = () => resolve(window.turnstile ?? null);
    script.onerror = () => resolve(null);
    document.head.appendChild(script);
  });
  return loading;
}

export interface Challenge {
  /** Place this where the widget should appear. */
  element: HTMLElement;
  /** The token to send, or '' while the reader has not solved it yet. */
  token(): string;
  /**
   * Throws the solved token away and asks for another. A token is good for
   * one submission, so every refusal has to be followed by this or the second
   * attempt fails for a reason that has nothing to do with the first.
   */
  reset(): void;
}

/**
 * Renders a challenge, or returns null when there is nothing to render —
 * which is the ordinary case, and why every caller has to handle it.
 */
export function challenge(siteKey: string, onSolved?: () => void): Challenge | null {
  if (!siteKey) return null;

  const element = document.createElement('div');
  element.className = 'oa-challenge';

  let solved = '';
  let widget = '';

  void load().then((api) => {
    if (!api || !element.isConnected) return;
    widget = api.render(element, {
      sitekey: siteKey,
      callback: (token: string) => {
        solved = token;
        onSolved?.();
      },
      // A token that has gone stale, or a challenge the reader failed. Both
      // mean the one held here is no longer worth sending.
      'expired-callback': () => { solved = ''; },
      'error-callback': () => { solved = ''; },
    });
  });

  return {
    element,
    token: () => solved,
    reset: () => {
      solved = '';
      if (widget) window.turnstile?.reset(widget);
    },
  };
}
