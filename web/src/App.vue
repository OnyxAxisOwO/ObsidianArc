<script setup lang="ts">
// The root. Everything is decided by the route, so there is nothing here but
// the outlet — and the one piece of global, always-on state that has nowhere
// else to live: the browser tab itself is not part of any route's template.

import { computed, ref, watch, watchEffect } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { siteInfo } from './stores/session';
import { useTheme } from './composables/useTheme';
import OaBackdrop from './components/OaBackdrop.vue';
import { computePageTransition, getPageKey, type PageTransitionName } from './lib/pageTransitions';

// /api/site already resolves browser_title against the site's own name
// (settings.Service.BrowserTitle), so there is no second fallback to keep in
// sync here. Runs again whenever siteInfo changes — on the initial load, and
// after an operator saves the settings page while this tab is the one open —
// so the tab updates without a reload.
watchEffect(() => {
  document.title = siteInfo.value.browser_title || siteInfo.value.name;
});

const DEFAULT_FAVICON = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'%3E%3Cpath fill='%23a1a1aa' d='M12 2 3 7v10l9 5 9-5V7z'/%3E%3C/svg%3E";

watchEffect(() => {
  if (typeof document === 'undefined') return;
  const icon = siteInfo.value.logo_url || DEFAULT_FAVICON;
  let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']");
  if (!link) {
    link = document.createElement('link');
    link.rel = 'icon';
    document.head.appendChild(link);
  }
  link.href = icon;
});

let router: ReturnType<typeof useRouter> | undefined;
let route: ReturnType<typeof useRoute> | undefined;
try {
  router = useRouter();
} catch {
  // outside router context in tests
}
try {
  route = useRoute();
} catch {
  // outside router context in tests
}

// The instance's signed-in background when it is a page. A picture needs
// nothing here — theme.ts paints it as a CSS layer — but a page needs an
// element, and the root is the one place that outlives every route.
const theme = useTheme();
const backdrop = computed(() => {
  const paper = theme.paper();
  return paper?.html ? paper.url : '';
});

const transitionName = ref<PageTransitionName>('page-slide-left');

// Sync transition name before the route change begins to ensure leave-active classes use the right direction
if (router) {
  router.beforeEach((to, from) => {
    if (!to.path || !from.path) return;
    const nextTransition = computePageTransition(from.path, to.path);
    if (nextTransition) {
      transitionName.value = nextTransition;
    }
  });
}

// Fallback watcher for non-router test setups
if (route) {
  watch(
    () => route?.path,
    (toPath, fromPath) => {
      if (!toPath || !fromPath) return;
      const nextTransition = computePageTransition(fromPath, toPath);
      if (nextTransition) {
        transitionName.value = nextTransition;
      }
    },
  );
}
</script>

<template>
  <OaBackdrop v-if="backdrop" :key="backdrop" class="is-wallpaper" :url="backdrop" />
  <div class="oa-app-viewport">
    <RouterView v-slot="{ Component, route: currentRoute }">
      <Transition :name="transitionName">
        <div :key="getPageKey(currentRoute?.path || '/')" class="oa-app-page">
          <component :is="Component" v-if="Component" />
        </div>
      </Transition>
    </RouterView>
  </div>
</template>
