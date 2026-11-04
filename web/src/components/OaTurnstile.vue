<script setup lang="ts">
// A challenge, where the operator asked for one.
//
// The token it produces is good for exactly one submission, so every refusal
// — a taken username as much as a failed challenge — has to be followed by
// `reset()`, or the second attempt fails for a reason that has nothing to do
// with the first. That is what `reset` is exposed for.

import { onBeforeUnmount, onMounted, ref } from 'vue';
import { t } from '@/composables/useI18n';
import { loadTurnstile } from '@/composables/useTurnstile';

const props = defineProps<{ siteKey: string }>();
const emit = defineEmits<{ (event: 'solved'): void }>();

const host = ref<HTMLElement | null>(null);
const token = ref('');
// The script could not be fetched, which is what a firewall or a blocker does
// to this one. The form says so, rather than leaving an empty box where the
// challenge should be and a refusal that gives no reason.
const unavailable = ref(false);
let widget = '';

onMounted(() => {
  if (!props.siteKey) return;
  void loadTurnstile().then((api) => {
    if (!host.value?.isConnected) return;
    if (!api) {
      unavailable.value = true;
      return;
    }
    widget = api.render(host.value, {
      sitekey: props.siteKey,
      callback: (solved: string) => {
        token.value = solved;
        emit('solved');
      },
      // A token that has gone stale, or a challenge the reader failed. Both
      // mean the one held here is no longer worth sending.
      'expired-callback': () => { token.value = ''; },
      'error-callback': () => { token.value = ''; },
    });
  });
});

onBeforeUnmount(() => {
  if (widget) window.turnstile?.remove(widget);
});

defineExpose({
  token: () => token.value,
  reset: () => {
    token.value = '';
    if (widget) window.turnstile?.reset(widget);
  },
});
</script>

<template>
  <div ref="host" class="oa-challenge">
    <p v-if="unavailable" class="oa-auth-error" role="alert">{{ t('challengeUnavailable') }}</p>
  </div>
</template>
