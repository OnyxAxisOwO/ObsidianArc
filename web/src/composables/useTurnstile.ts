// The Cloudflare Turnstile widget, where an operator has switched one on.
//
// The one third-party script this interface loads, and it is loaded lazily:
// nothing is fetched on an instance with no challenge configured, which is
// every instance until somebody turns it on. The dependency rule is about
// what ships in the bundle, and this ships in nothing — but it is a script
// from somebody else's server on the sign-in page, which is worth knowing and
// is why it is confined to this file and the few challenged surfaces.

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
export function loadTurnstile(): Promise<TurnstileAPI | null> {
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

export type { TurnstileAPI };
