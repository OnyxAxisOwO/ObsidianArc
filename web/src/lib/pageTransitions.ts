// Page transition logic: which way each navigation slides, if at all.
// Determines whether navigation between routes should trigger a full-page
// horizontal slide (左右滑动) or subtle cross-fade, and in which direction.

export type PageTransitionName = 'page-slide-left' | 'page-slide-right' | 'page-fade';

/**
 * Route hierarchy weights for horizontal directional transitions:
 * Chat / Root drawers: 0
 * Terminal: 1
 * Admin Dashboard: 2
 * Navigating from lower to higher weight slides left (enters from right).
 * Navigating from higher to lower weight slides right (enters from left).
 */
export function getRouteWeight(path: string): number {
  if (path.startsWith('/admin')) return 2;
  if (path === '/terminal') return 1;
  return 0;
}

/**
 * Top-level page keys determining which transitions remount the page frame.
 * Sub-routes within admin (/admin/dashboard, /admin/users, /admin/settings) share
 * the same 'admin' key so AdminPage remains mounted and uses its own internal
 * directional slide transition.
 */
export function getPageKey(path: string): string {
  if (path.startsWith('/admin')) return 'admin';
  if (path === '/terminal') return 'terminal';
  if (
    path === '/login' ||
    path === '/register' ||
    path === '/two-factor' ||
    path === '/verify' ||
    path.startsWith('/oauth')
  ) {
    return 'auth';
  }
  return 'root';
}

export function computePageTransition(fromPath: string, toPath: string): PageTransitionName | null {
  if (!fromPath || toPath === fromPath) return null;

  const fromKey = getPageKey(fromPath);
  const toKey = getPageKey(toPath);

  // Same page realm: no full-page transition needed
  if (fromKey === toKey) return null;

  if (fromKey === 'auth' || toKey === 'auth') {
    return 'page-fade';
  }

  const fromWeight = getRouteWeight(fromPath);
  const toWeight = getRouteWeight(toPath);

  return toWeight > fromWeight ? 'page-slide-left' : 'page-slide-right';
}
