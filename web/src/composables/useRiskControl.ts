// The self-hosted risk control service's SDK, where the operator has
// switched one on.
//
// The shape follows useTurnstile: one third-party script, loaded lazily, so
// nothing is fetched on an instance with no check configured. The SDK is
// built to be called before it finishes loading — boot.js installs a
// placeholder that collects init/execute calls and replays them once the
// real implementation arrives — so the loader below only has to know when
// the placeholder exists, and everything else is the SDK's business.

export interface RiskControlAPI {
  init(options: { base?: string; site?: string }): void;
  /**
   * Resolves to the token to submit with the form, or rejects when the
   * service refuses this browser outright — its design is fail-closed.
   * May take a while: the interactive check, when the service asks for
   * one, runs inside this call.
   */
  execute(action: string, form?: HTMLFormElement): Promise<string>;
}

declare global {
  interface Window {
    RiskControl?: RiskControlAPI;
  }
}

let loading: Promise<RiskControlAPI | null> | null = null;
let loadedBase = '';

/**
 * Loads the script once per page, however many surfaces ask for it.
 *
 * Resolves to null rather than rejecting when it cannot be fetched — a
 * service that is down should end as a form saying the check did not pass,
 * not a page that failed to render. The same rule the Turnstile loader
 * keeps, for the same reason.
 */
export function loadRiskControl(base: string): Promise<RiskControlAPI | null> {
  if (window.RiskControl) return Promise.resolve(window.RiskControl);
  if (loading && loadedBase === base) return loading;

  loading = new Promise<RiskControlAPI | null>((resolve) => {
    const script = document.createElement('script');
    script.src = base.replace(/\/+$/, '') + '/boot.js';
    script.async = true;
    script.onload = () => resolve(window.RiskControl ?? null);
    script.onerror = () => resolve(null);
    document.head.appendChild(script);
  });
  loadedBase = base;
  return loading;
}

// Kept by identity rather than as a flag: a service whose boot.js was
// reloaded into the page hands back a different object, and that one wants
// its init() call however many times the page has been loaded before.
let initialisedFor: RiskControlAPI | null = null;

/**
 * Loads the SDK if needed and points it at the instance's service. Called
 * as soon as a challenged screen opens rather than on submit: the service
 * scores behaviour, and telemetry that starts at submit time has nothing
 * to score.
 */
export async function beginRiskControl(base: string, site: string): Promise<RiskControlAPI | null> {
  const api = await loadRiskControl(base);
  if (api && initialisedFor !== api) {
    api.init({ base, site });
    initialisedFor = api;
  }
  return api;
}
