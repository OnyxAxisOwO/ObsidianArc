<script setup lang="ts">
// A background that is a page rather than a picture: an operator's HTML,
// drawn in a frame behind everything.
//
// Sandboxed twice — here, and by the Content-Security-Policy the server
// sends with the page — so it runs its scripts in an origin of its own and
// can reach nothing of this one, even opened directly. It takes no input
// either: it is scenery, and a click meant for the interface must not land
// in it.

defineProps<{ url: string }>();
</script>

<template>
  <iframe
    class="oa-backdrop-frame"
    :src="url"
    sandbox="allow-scripts"
    referrerpolicy="no-referrer"
    tabindex="-1"
    aria-hidden="true"
    title=""
  />
</template>
