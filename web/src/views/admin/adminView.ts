// What an administration page is handed.
//
// The shell owns the heading, the actions strip and the scrolling body; a
// page owns what goes in them. This is the seam between the two, and it is
// deliberately three things: name yourself, put your buttons somewhere, and
// ask to be drawn again after a save.

import { inject, provide, type InjectionKey } from 'vue';

export interface AdminView {
  /** Left of the actions strip. The subtitle is optional and often absent. */
  setTitle(title: string, subtitle?: string): void;
  /** Re-runs the current page, after a save. */
  reload(): void;
  /** Everything after /admin/<slug>/, for a nested selection. */
  params: string[];
  /**
   * Where a page teleports its own buttons — "Add a provider", a refresh.
   *
   * A plain element rather than a template ref, and created before any page
   * mounts. A ref would be nulled while the tree is being torn down, which
   * flips the `v-if` on every page's teleport and schedules a render on a
   * component that is already unmounting — where its own setup state has
   * gone and every expression in its template reads undefined.
   */
  actionsHost: HTMLElement;
}

const ADMIN_VIEW: InjectionKey<AdminView> = Symbol('oa-admin-view');

export function provideAdminView(view: AdminView): void {
  provide(ADMIN_VIEW, view);
}

export function useAdminView(): AdminView {
  const view = inject(ADMIN_VIEW, null);
  if (!view) throw new Error('admin page rendered outside the administration shell');
  return view;
}
