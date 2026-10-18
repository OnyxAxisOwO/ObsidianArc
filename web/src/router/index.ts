// The routing table.
//
// Eleven screens and one nested layout, so the table below is the whole of it.
// The two things worth reading are the nesting and the guards.
//
// The nesting: /settings, /keys, /usage, /feedback and /about are children of
// `/`, not pages of their own, because that is what they are on screen — a
// column that opens beside the conversation, with the chat still behind it.
// Nesting keeps the chat mounted once behind all of them. A screen that drew
// the chat itself would rebuild it on every move between these panels.
//
// The guards: which pages need a session is one readable list rather than a
// check at the top of each screen. The server enforces the same rules on
// every endpoint regardless — this only decides what to draw.
//
// Only the backoffice and the terminal are loaded lazily, and everything else
// is imported statically on purpose. Route-level splitting sounds free and is
// not: the panels below are columns over a chat that is already on screen, so
// a chunk per panel buys a round trip in the middle of a click and saves bytes
// nobody was going to avoid downloading anyway. The backoffice is different —
// it is a quarter of the application's code and most accounts can never
// reach it — and so is the terminal: every account may open it, but few ever
// will, and its emulator, history and ANSI decoder would otherwise be on the
// first paint of everyone who only came to chat.

import {
  createRouter, createWebHistory, type RouteLocationNormalized, type RouteLocationRaw, type RouteRecordRaw,
} from 'vue-router';
import { safeNext, serverOwned } from '@/lib/next';
import { BACKOFFICE_PANEL_PARAM } from '@/composables/usePanelExit';
import { SESSION_LOST } from '@/api/session-ended';
import { currentUser, pendingSecondFactor, siteInfo } from '@/stores/session';
import AboutPanel from '@/views/AboutPanel.vue';
import ArchivePanel from '@/views/ArchivePanel.vue';
import AuthView from '@/views/AuthView.vue';
import BindOIDCView from '@/views/BindOIDCView.vue';
import CompleteSignupView from '@/views/CompleteSignupView.vue';
import ConsentView from '@/views/ConsentView.vue';
import FeedbackPanel from '@/views/FeedbackPanel.vue';
import ImageLabPanel from '@/views/ImageLabPanel.vue';
import KeysPanel from '@/views/KeysPanel.vue';
import NotFoundView from '@/views/NotFoundView.vue';
import RootView from '@/views/RootView.vue';
import SettingsPanel from '@/views/SettingsPanel.vue';
import TwoFactorEnrolView from '@/views/TwoFactorEnrolView.vue';
import UptimePanel from '@/views/UptimePanel.vue';
import LeaderboardPanel from '@/views/LeaderboardPanel.vue';
import UsagePanel from '@/views/UsagePanel.vue';
import PluginUserPanel from '@/views/PluginUserPanel.vue';
import VerifyView from '@/views/VerifyView.vue';
import { panelAllowed, resolveSidePanel } from '@/views/panels';

const routes: RouteRecordRaw[] = [
  { path: '/login', component: AuthView, props: { mode: 'login' } },
  { path: '/register', component: AuthView, props: { mode: 'register' } },
  // Public: the link is opened out of a mail client, quite possibly in a
  // browser that has never signed in here.
  { path: '/verify', component: VerifyView },
  // "Another site wants to sign you in with your account here." A page rather
  // than a panel, for the reason the sign-in card is one: there is no chat
  // behind it, and it is a decision rather than a task.
  { path: '/oauth/consent', component: ConsentView, meta: { auth: true } },
  // The step a provider sign-in stops at when this server wants something the
  // provider could not supply. Public by necessity: the person filling it in
  // has no account yet, which is the whole point of the form. What stands in
  // for a session is the signed cookie the callback left behind.
  { path: '/oauth/complete', component: CompleteSignupView },
  // Where the two-step policy holds an account until it enrols. A page for
  // the reason the consent screen is one: nothing behind it would work.
  { path: '/two-factor', component: TwoFactorEnrolView, meta: { auth: true } },
  // Where the operator's OIDC policy holds an account until it links an
  // identity. Same reason as above.
  { path: '/bind-oidc', component: BindOIDCView, meta: { auth: true } },

  {
    path: '/',
    component: RootView,
    children: [
      { path: '', name: 'chat', component: { render: () => null } },
      {
        path: 'settings',
        component: SettingsPanel,
        // The tab is read here rather than by the panel, because the panel is
        // also drawn over the backoffice, whose own `tab` parameter belongs to
        // its plugin pages and must not select a settings tab.
        props: (to) => {
          const tab = to.query['tab'];
          return { tab: typeof tab === 'string' ? tab : undefined };
        },
        meta: { auth: true },
      },
      { path: 'archive', component: ArchivePanel, meta: { auth: true } },
      { path: 'keys', component: KeysPanel, meta: { auth: true } },
      { path: 'usage', component: UsagePanel, meta: { auth: true } },
      { path: 'feedback', component: FeedbackPanel, meta: { auth: true } },
      { path: 'about', component: AboutPanel, meta: { auth: true } },
      { path: 'image-lab', component: ImageLabPanel, meta: { auth: true } },
      { path: 'uptime', component: UptimePanel, meta: { auth: true } },
      { path: 'leaderboard', component: LeaderboardPanel, meta: { auth: true } },
      // A panel a plugin brings (UserPanelSpec). The slug is resolved when the
      // panel is drawn, not here: the plugins load after the router is made.
      { path: 'x/:slug/:entry?', component: PluginUserPanel, props: true, meta: { auth: true } },
      { path: 'terminal', component: () => import('@/views/TerminalPanel.vue'), meta: { auth: true } },
    ],
  },

  // Where the terminal used to live, for a bookmark made before it moved.
  { path: '/admin/terminal', redirect: '/terminal' },

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

// The session store signs a tab out in memory when its session ends, and that
// leaves the route alone: the guard above runs on navigation, not on sign-out.
// Sending the reader to sign-in here, with where they were as `next`, is the
// same move the account menu makes after an explicit sign-out.
if (typeof window !== 'undefined') {
  window.addEventListener(SESSION_LOST, () => {
    if (router.currentRoute.value.path === '/login') return;
    void router.replace({ path: '/login', query: { next: router.currentRoute.value.fullPath } });
  });
}

// The destination a signed-in visitor is sent to. As a string location the
// query and hash of a deep link survive (/settings?tab=keys keeps its tab); an
// object with a path is re-parsed with the query dropped.
function arrive(next: string): RouteLocationRaw {
  const target = router.resolve(next || '/');
  return { path: target.path, query: target.query, hash: target.hash, replace: true };
}

// Where a gate the visitor has just cleared lets them go on to: the `next`
// they were stopped on the way to. One copy for every gate, because the
// server's own pages need a real navigation and a gate that forgot that would
// draw "no such page" in place of the request another site is waiting on.
function goOn(to: RouteLocationNormalized): RouteLocationRaw | false {
  const next = safeNext(to.query['next']);
  if (next && serverOwned(next)) {
    window.location.assign(next);
    return false;
  }
  return arrive(next);
}

// Holds a signed-in account at the page a policy wants it on, remembering
// where it was going. The chat itself is not worth carrying: it is where
// goOn lands without a `next` anyway.
function holdAt(path: string, to: RouteLocationNormalized): RouteLocationRaw {
  const next = to.path === '/' ? undefined : to.fullPath;
  return { path, query: next ? { next } : {}, replace: true };
}

// A page an operator's policy holds a signed-in account at until it is done.
// Holding and releasing are separate tests rather than one and its negation:
// the OIDC gate stands aside for an account still owing a factor, but that
// account has not cleared it, so it is not sent on from /bind-oidc either.
interface PolicyGate {
  path: string;
  hold: (to: RouteLocationNormalized) => boolean;
  release: () => boolean;
}

// Checked in this order, each gate's hold before its release: the first gate
// an account still owes is the one it is held at.
const POLICY_GATES: readonly PolicyGate[] = [
  // The operator's policy wants a second step on this account before it does
  // anything else. The server refuses the rest regardless; this only saves
  // drawing a screen made of refusals.
  {
    path: '/two-factor',
    hold: (to) => !!currentUser.value?.two_factor_enrol && to.path !== '/two-factor',
    release: () => !currentUser.value?.two_factor_enrol,
  },
  // The operator's OIDC policy wants every account to link an identity
  // before it does anything else. An account still owing a factor is left to
  // the two-step redirect above: this one must not claim it, or the two would
  // send each other back and forth. See oauth.Service.BindingGate for why the
  // server holds the same order.
  {
    path: '/bind-oidc',
    hold: (to) => !!currentUser.value?.oidc_binding_required && !currentUser.value?.two_factor_enrol && to.path !== '/bind-oidc',
    release: () => !currentUser.value?.oidc_binding_required,
  },
];

router.beforeEach((to) => {
  const signedIn = !!currentUser.value;
  const needsSession = to.matched.some((record) => record.meta['auth']);

  // Where they were going is carried along, so that signing in lands on the
  // page that asked rather than on the chat. It matters most for the consent
  // screen, where the thing being lost is not a page but a request another
  // site is waiting on.
  if (needsSession && !signedIn) {
    return { path: '/login', query: { next: to.fullPath }, replace: true };
  }

  // Halfway through signing in: the password was right and the code is
  // still wanted, whatever the front door is set to show.
  if (!signedIn && pendingSecondFactor.value && to.path === '/') {
    return { path: '/login', replace: true };
  }

  for (const gate of POLICY_GATES) {
    if (signedIn && gate.hold(to)) return holdAt(gate.path, to);
    if (to.path === gate.path && signedIn && gate.release()) return goOn(to);
  }

  // The address itself. Signed in it is the chat; signed out it is whatever
  // the operator has put at the front door, which may be the sign-in card, a
  // page they wrote, or a sample conversation. Only this route consults the
  // setting: a link to /settings is a request for something a visitor cannot
  // have, and goes to sign-in regardless.
  if (to.path === '/' && !signedIn && (siteInfo.value.landing?.mode ?? 'login') === 'login') {
    return { path: '/login', replace: true };
  }

  // A partner or friend's link, opened by somebody who already has an
  // account: there is no sign-up for it to fill in, but the code itself is
  // still worth something, as a claim. Checked ahead of the plain
  // already-signed-in bounce below, which would otherwise send this same
  // visitor to the chat with the code dropped on the floor.
  if (signedIn && to.path === '/register') {
    const invite = to.query['invite'];
    if (typeof invite === 'string' && invite) {
      return { path: '/settings', query: { tab: 'invites', claim: invite }, replace: true };
    }
  }

  // Someone signed in who is already where they were being sent — unless the
  // sign-in page was carrying somewhere to go, which is a request somebody
  // made rather than a page they wandered onto.
  if (signedIn && (to.path === '/login' || to.path === '/register')) {
    return goOn(to);
  }

  // The terminal is offered by group. The server refuses the account either
  // way; this only stops the panel opening onto a refusal.
  if (to.path === '/terminal' && currentUser.value?.allow_terminal === false) {
    return { path: '/', replace: true };
  }

  // Uptime is available to admins, and to readers only if published.
  if (to.path === '/uptime' && !panelAllowed('uptime')) {
    return { path: '/', replace: true };
  }

  // The leaderboard likewise: its curators may look before it is published.
  if (to.path === '/leaderboard' && !panelAllowed('leaderboard')) {
    return { path: '/', replace: true };
  }

  // A panel drawn over the backoffice is held to the same rule as its chat
  // address. Only the panel is taken out of the address: the page underneath
  // is what the reader asked for.
  if (to.meta['admin']) {
    const panel = resolveSidePanel(to.query[BACKOFFICE_PANEL_PARAM]);
    if (panel && !panelAllowed(panel.name)) {
      const query = { ...to.query };
      delete query[BACKOFFICE_PANEL_PARAM];
      return { path: to.path, query, hash: to.hash, replace: true };
    }
  }

  return true;
});
