<script setup lang="ts">
// The macOS-style stack in the top-right corner: up to three cards, one per
// notification that arrived since this tab opened.
//
// Mounted once, in the signed-in shell. Its own onMounted/onUnmounted starts
// and stops the polling store — the same shape AnnounceBell uses for its own
// refresh — so there is nothing to wire from AppShell beyond `v-if`.

import { onBeforeUnmount, onMounted, watch } from 'vue';
import {
  activeToasts, dismissToast, openNotification, startNotifications, stopNotifications, type Toast,
} from '@/stores/notifications';
import { describeNotification } from '@/lib/notification-text';
import { relativeTime } from '@/lib/format';
import { t } from '@/composables/useI18n';
import { IconClose } from '@/icons';
import OaIconButton from './OaIconButton.vue';

const DISMISS_MS = 6000;

/**
 * One entry per toast currently on screen. A plain Map rather than reactive
 * state: nothing here is drawn from it directly, it only remembers enough to
 * pause and resume a timeout — reactivity would just be overhead on every
 * mouse move.
 */
const timers = new Map<string, { handle: number | null; remaining: number; startedAt: number }>();

function clearHandle(id: string): void {
  const entry = timers.get(id);
  if (entry?.handle !== null && entry?.handle !== undefined) window.clearTimeout(entry.handle);
}

function schedule(id: string, ms: number): void {
  clearHandle(id);
  timers.set(id, {
    handle: window.setTimeout(() => dismissToast(id), ms),
    remaining: ms,
    startedAt: Date.now(),
  });
}

function pause(id: string): void {
  const entry = timers.get(id);
  if (!entry || entry.handle === null) return;
  window.clearTimeout(entry.handle);
  entry.remaining = Math.max(entry.remaining - (Date.now() - entry.startedAt), 0);
  entry.handle = null;
}

function resume(id: string): void {
  const entry = timers.get(id);
  if (!entry) return;
  entry.startedAt = Date.now();
  entry.handle = window.setTimeout(() => dismissToast(id), entry.remaining);
}

// Keeps the timer set in step with the store's own queue: a toast that
// arrives gets six seconds, one that is dismissed — by its own timer, by a
// click, or by the store trimming the queue — has its timer forgotten rather
// than firing into nothing.
//
// immediate: true so a toast already queued the moment this component mounts
// — the store starts polling before this ever runs — gets its timer here
// rather than sitting on screen forever.
watch(activeToasts, (list) => {
  const present = new Set(list.map((toast) => toast.id));
  for (const id of Array.from(timers.keys())) {
    if (!present.has(id)) {
      clearHandle(id);
      timers.delete(id);
    }
  }
  for (const toast of list) {
    if (!timers.has(toast.id)) schedule(toast.id, DISMISS_MS);
  }
}, { immediate: true });

onMounted(startNotifications);
onBeforeUnmount(() => {
  stopNotifications();
  for (const id of timers.keys()) clearHandle(id);
  timers.clear();
});

function onClick(toast: Toast): void {
  void openNotification(toast.notification);
}
</script>

<template>
  <div class="oa-toast-stack" aria-live="polite">
    <TransitionGroup name="oa-toast" tag="div" class="oa-toast-list">
      <div
        v-for="toast in activeToasts"
        :key="toast.id"
        class="oa-toast"
        role="status"
        tabindex="0"
        @click="onClick(toast)"
        @keydown.enter="onClick(toast)"
        @mouseenter="pause(toast.id)"
        @mouseleave="resume(toast.id)"
      >
        <component :is="describeNotification(toast.notification).icon" :size="18" class="oa-toast-icon" />
        <span class="oa-toast-body">
          <span class="oa-toast-title">{{ describeNotification(toast.notification).title }}</span>
          <span class="oa-toast-text">{{ describeNotification(toast.notification).body }}</span>
          <span class="oa-toast-time">{{ relativeTime(toast.notification.created_at) }}</span>
        </span>
        <OaIconButton
          class="oa-toast-close"
          :label="t('close')"
          @click.stop="dismissToast(toast.id)"
        >
          <IconClose :size="12" />
        </OaIconButton>
      </div>
    </TransitionGroup>
  </div>
</template>
