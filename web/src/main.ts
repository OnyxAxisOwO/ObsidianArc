import { createApp } from 'vue';
import App from './App.vue';
import { router } from './router';
import { primeLanguage } from './composables/useI18n';
import { startSession } from './stores/session';
import { startTheme } from './theme/theme';
import './styles/index.scss';

startTheme();

const root = document.getElementById('app');
if (!root) throw new Error('missing #app');

void boot();

async function boot(): Promise<void> {
  // The dictionary before the session: a slow or refused /api/auth/me must not
  // decide whether the sign-in card is in the reader's language. Both are one
  // round trip and they run together.
  //
  // Both also have to settle before the router does anything, because the
  // first navigation guard asks who is signed in and what the front door is
  // set to — and a guard that runs against an empty session sends everybody
  // to /login for one frame.
  await Promise.all([primeLanguage(), startSession()]);

  const app = createApp(App);
  app.use(router);
  await router.isReady();
  app.mount(root!);
}
