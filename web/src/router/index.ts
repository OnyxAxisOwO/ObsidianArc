// The routing table.
//
// Nine screens and one nested layout, so the table below is the whole of it.
// The two things worth reading are the nesting and the guards.
//
// The nesting: /settings, /keys, /usage and /about are children of `/`, not
// pages of their own, because that is what they are on screen — a column that
// opens beside the conversation, with the chat still behind it. The
// hand-written router had each of those screens call `renderChatPage` first
// and open a panel over the row it returned; expressing it as nesting is the
// same result with the chat mounted once instead of rebuilt four times.
//
// The guards: which pages need a session is one readable list rather than a
// check at the top of each screen. The server enforces the same rules on
// every endpoint regardless — this only decides what to draw.
//
// Only the backoffice is loaded lazily, and everything else is imported
// statically on purpose. Route-level splitting sounds free and is not: the
// panels below are columns over a chat that is already on screen, so a chunk
// per panel buys a round trip in the middle of a click and saves bytes
// nobody was going to avoid downloading anyway. The backoffice is different —
// it is a quarter of the application's code and most accounts can never
// reach it.

import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { currentUser, isAdmin, siteInfo } from '@/stores/session';
import AboutPanel from '@/views/AboutPanel.vue';
import AuthView from '@/views/AuthView.vue';
import ImageLabPanel from '@/views/ImageLabPanel.vue';
import KeysPanel from '@/views/KeysPanel.vue';
import NotFoundView from '@/views/NotFoundView.vue';
import RootView from '@/views/RootView.vue';
import SettingsPanel from '@/views/SettingsPanel.vue';
import UptimePanel from '@/views/UptimePanel.vue';
import UsagePanel from '@/views/UsagePanel.vue';
import VerifyView from '@/views/VerifyView.vue';

const routes: RouteRecordRaw[] = [
  { path: '/login', component: AuthView, props: { mode: 'login' } },
  { path: '/register', component: AuthView, props: { mode: 'register' } },
  // Public: the link is opened out of a mail client, quite possibly in a
  // browser that has never signed in here.
  { path: '/verify', component: VerifyView },

  {
    path: '/',
    component: RootView,
    children: [
      { path: '', name: 'chat', component: { render: () => null } },
      { path: 'settings', component: SettingsPanel, meta: { auth: true } },
      { path: 'keys', component: KeysPanel, meta: { auth: true } },
      { path: 'usage', component: UsagePanel, meta: { auth: true } },
      { path: 'about', component: AboutPanel, meta: { auth: true } },
      { path: 'image-lab', component: ImageLabPanel, meta: { auth: true } },
      { path: 'uptime', component: UptimePanel, meta: { auth: true } },
    ],
  },

  // Fetched when an administrator first opens the backoffice, rather than by
  // everyone who loads the chat. It is a quarter of the application's code
  // and most people can never reach it, so it is the one screen worth paying
  // a round trip for.
  {
    path: '/admin/:section(.*)*',
    component: () => import('@/views/admin/AdminPage.vue'),
    meta: { auth: true, admin: true },
  },

  { path: '/:path(.*)*', component: NotFoundView },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
  // Panels and transcripts manage their own scroll; restoring the document's
  // would fight them.
  scrollBehavior: () => false,
});

router.beforeEach((to) => {
  const signedIn = !!currentUser.value;
  const needsSession = to.matched.some((record) => record.meta['auth']);

  if (needsSession && !signedIn) return { path: '/login', replace: true };

  // The address itself. Signed in it is the chat; signed out it is whatever
  // the operator has put at the front door, which may be the sign-in card, a
  // page they wrote, or a sample conversation. Only this route consults the
  // setting: a link to /settings is a request for something a visitor cannot
  // have, and goes to sign-in regardless.
  if (to.path === '/' && !signedIn && (siteInfo.value.landing?.mode ?? 'login') === 'login') {
    return { path: '/login', replace: true };
  }

  // Someone signed in who is already where they were being sent.
  if (signedIn && (to.path === '/login' || to.path === '/register')) {
    return { path: '/', replace: true };
  }

  // Uptime is available to admins, and to readers only if published.
  if (to.path === '/uptime' && !isAdmin.value && !siteInfo.value.health_show_users) {
    return { path: '/', replace: true };
  }

  return true;
});

/**
 * Whether the reader may see the backoffice.
 *
 * Not a redirect: an administrator's link opened by somebody else should say
 * so over the product rather than bounce them silently to the chat, so the
 * refusal is drawn by the admin screen itself.
 */
export function mayAdminister(): boolean {
  return isAdmin.value;
}
