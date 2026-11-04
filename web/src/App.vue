<script setup lang="ts">
// The root. Everything is decided by the route, so there is nothing here but
// the outlet — and the one piece of global, always-on state that has nowhere
// else to live: the browser tab itself is not part of any route's template.

import { computed, onBeforeUnmount, ref, watchEffect } from 'vue';
import { useRouter } from 'vue-router';
import { useMediaQuery } from '@vueuse/core';
import { siteInfo } from './stores/session';
import { useTheme } from './composables/useTheme';
import OaBackdrop from './components/OaBackdrop.vue';
import OaRoutePage from './components/OaRoutePage.vue';
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
  const icon = siteInfo.value.logo_url || DEFAULT_FAVICON;
  let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']");
  if (!link) {
    link = document.createElement('link');
    link.rel = 'icon';
    document.head.appendChild(link);
  }
  link.href = icon;
});

const router = useRouter();

// The instance's signed-in background when it is a page. A picture needs
// nothing here — theme.ts paints it as a CSS layer — but a page needs an
// element, and the root is the one place that outlives every route.
const theme = useTheme();
const backdrop = computed(() => {
  const paper = theme.paper();
  return paper?.html ? paper.url : '';
});

const transitionName = ref<PageTransitionName>('page-slide-left');
const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)');
// Vue only detects transitions on the outer element; workspace slides run
// on its body instead. The shared duration keeps that body alive to the end.
const transitionDuration = computed(() => reducedMotion.value ? 80 : transitionName.value === 'page-fade' ? 220 : 380);

// This guard is the only place the direction is worked out. It runs before the
// new page is committed, so the leave transition already has the right name;
// a watcher on the path would compute the same value again after the commit.
const stopTransitionGuard = router.beforeEach((to, from) => {
  if (!to.path || !from.path) return;
  const nextTransition = computePageTransition(from.path, to.path);
  if (nextTransition) {
    transitionName.value = nextTransition;
  }
});

// The router outlives this root: a guard left behind would keep running after
// the page is gone, and every later mount (tests, dev hot reload) adds another.
onBeforeUnmount(() => stopTransitionGuard());
</script>

<template>
  <OaBackdrop v-if="backdrop" :key="backdrop" class="is-wallpaper" :url="backdrop" />
  <div class="oa-app-viewport" :style="{ '--oa-page-duration': `${transitionDuration}ms` }">
    <RouterView v-slot="{ Component, route: currentRoute }">
      <Transition :name="transitionName" :duration="transitionDuration">
        <OaRoutePage :key="getPageKey(currentRoute.path)" :route="currentRoute">
          <component :is="Component" v-if="Component" />
        </OaRoutePage>
      </Transition>
    </RouterView>
  </div>
</template>
