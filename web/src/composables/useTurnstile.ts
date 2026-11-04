// The Cloudflare Turnstile widget, where an operator has switched one on.
//
// The one third-party script this interface loads, and it is loaded lazily:
// nothing is fetched on an instance with no challenge configured, which is
// every instance until somebody turns it on. The dependency rule is about
// what ships in the bundle, and this ships in nothing — but it is a script
// from somebody else's server on the sign-in page, which is worth knowing and
// is why it is confined to this file and the few challenged surfaces.

const SCRIPT = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
// Long enough for a slow link to fetch the script, short enough that a stalled
// request does not leave the form's challenge box blank for good.
const LOAD_DEADLINE_MS = 15_000;

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
    // Every way of giving up goes through here. Remembered for the page's
    // lifetime, a failure made every later widget draw nothing after one blip
    // on the network, so the next widget tries again. The handlers are
    // detached first: a late event from this script must not clear a retry
    // that has started since.
    const giveUp = (): void => {
      clearTimeout(deadline);
      script.onload = null;
      script.onerror = null;
      loading = null;
      script.remove();
      resolve(null);
    };
    // A request the network accepts and never answers would leave this promise
    // pending for the life of the page, and every widget would wait on it.
    const deadline = setTimeout(giveUp, LOAD_DEADLINE_MS);
    script.onload = () => {
      clearTimeout(deadline);
      const api = window.turnstile ?? null;
      // A script that ran without its API has failed as surely as one that
      // did not load. Its tag goes too, or every retry leaves one more dead
      // script in the document.
      if (!api) {
        giveUp();
        return;
      }
      resolve(api);
    };
    script.onerror = giveUp;
    document.head.appendChild(script);
  });
  return loading;
}

export type { TurnstileAPI };
