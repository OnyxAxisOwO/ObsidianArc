// A history router in about a hundred lines.
//
// The application has nine screens and no nested layouts, so a routing
// library would be more code than the thing it routes. This matches a path
// against a small table, hands the winner its parameters, and lets it draw
// into the root element.

export interface RouteContext {
  path: string;
  params: Record<string, string>;
  query: URLSearchParams;
}

export interface Route {
  // `/admin/users/:id` — a `:name` segment captures, everything else is
  // literal. A trailing `/*` matches any remaining segments.
  pattern: string;
  render: (root: HTMLElement, ctx: RouteContext) => void | Promise<void>;
}

interface CompiledRoute extends Route {
  segments: string[];
}

let routes: CompiledRoute[] = [];
let fallback: Route['render'] | null = null;
let rootElement: HTMLElement | null = null;
// Incremented on every navigation so a slow render that has been superseded
// can notice and stop before it paints over the newer screen.
let generation = 0;

export function startRouter(root: HTMLElement, table: Route[], notFound: Route['render']): void {
  rootElement = root;
  routes = table.map((route) => ({ ...route, segments: split(route.pattern) }));
  fallback = notFound;

  window.addEventListener('popstate', () => {
    void render();
  });

  // Any in-app link is intercepted here rather than each screen wiring up its
  // own click handler.
  document.addEventListener('click', (event) => {
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const anchor = (event.target as Element | null)?.closest('a');
    if (!anchor) return;
    const href = anchor.getAttribute('href');
    if (!href || anchor.target === '_blank' || anchor.hasAttribute('download')) return;
    if (!href.startsWith('/') || href.startsWith('//')) return;
    event.preventDefault();
    navigate(href);
  });

  void render();
}

export function navigate(path: string, options: { replace?: boolean } = {}): void {
  if (path === currentPath() + location.search) return;
  if (options.replace) history.replaceState(null, '', path);
  else history.pushState(null, '', path);
  void render();
}

export function currentPath(): string {
  return location.pathname || '/';
}

async function render(): Promise<void> {
  if (!rootElement) return;
  const mine = ++generation;

  const path = currentPath();
  const query = new URLSearchParams(location.search);
  const parts = split(path);

  for (const route of routes) {
    const params = match(route.segments, parts);
    if (!params) continue;
    await route.render(rootElement, { path, params, query });
    // A newer navigation started while this render was awaiting; its output
    // is the one that should be on screen.
    if (mine !== generation) return;
    return;
  }

  if (fallback) await fallback(rootElement, { path, params: {}, query });
}

function split(path: string): string[] {
  return path.split('/').filter(Boolean);
}

function match(pattern: string[], actual: string[]): Record<string, string> | null {
  const params: Record<string, string> = {};

  for (let i = 0; i < pattern.length; i++) {
    const expected = pattern[i]!;
    if (expected === '*') return params;

    const got = actual[i];
    if (got === undefined) return null;
    if (expected.startsWith(':')) {
      params[expected.slice(1)] = decodeURIComponent(got);
      continue;
    }
    if (expected !== got) return null;
  }

  return pattern.length === actual.length ? params : null;
}
