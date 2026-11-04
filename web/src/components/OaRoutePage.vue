<script setup lang="ts">
import { computed, provide, shallowReactive, watch } from 'vue';
import { routeLocationKey, routerViewLocationKey, type RouteLocationNormalizedLoaded } from 'vue-router';

const props = defineProps<{ route: RouteLocationNormalizedLoaded }>();

// A leaving page still renders during the slide. Keep its route and nested
// outlets on that page until unmount, rather than redrawing them for the
// destination underneath the animation. Same-page navigation stays reactive.
const route = shallowReactive({ ...props.route });
watch(() => props.route, (next) => Object.assign(route, next), { flush: 'sync' });
provide(routeLocationKey, route);
provide(routerViewLocationKey, computed(() => props.route));
</script>

<template>
  <div class="oa-app-page"><slot /></div>
</template>
