<script setup lang="ts">
// The bell in the header and the announcement it sometimes puts in front of
// you.
//
// The server decides which announcement wants attention; this decides whether
// it has already had it during this visit. That split is deliberate: "has
// this reader ever seen it" is durable and belongs in the database, while
// "have we already shown it since the tab was opened" is a property of the
// tab and would be pointless to store.

import { computed, onMounted, ref } from 'vue';
import {
  fetchAnnouncements,
  markAllRead,
  markRead,
  type Announcement,
  type AnnouncementFeed,
} from '@/api/announcements';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import { t } from '@/composables/useI18n';
import { IconBell } from '@/icons';
import { relativeTime } from '@/lib/format';
import AnnouncementSheet from './AnnouncementSheet.vue';

// Announcements shown during this page load, so an "every visit" one does not
// reappear the moment it is dismissed.
const shownThisVisit = new Set<string>();

const feed = ref<AnnouncementFeed>({ announcements: [], unread: 0, popup: null });
const showing = ref<Announcement | null>(null);
const menu = ref<InstanceType<typeof OaMenu> | null>(null);

const label = computed(() => (feed.value.unread
  ? `${t('announcements')} · ${t('announcementsUnread', { count: feed.value.unread })}`
  : t('announcements')));

/**
 * `popup: false` for the refresh that happens because the reader opened the
 * bell: they are already looking at the list, and throwing a sheet over it at
 * that moment would cover the thing they asked to see.
 */
async function refresh(options: { popup?: boolean } = {}): Promise<void> {
  try {
    const next = await fetchAnnouncements();
    feed.value = next;
    if (options.popup === false) return;
    // Only on the first read of a given announcement per visit. An "every
    // visit" one that has just been dismissed must not come straight back
    // when something else refreshes the feed.
    if (next.popup && !shownThisVisit.has(next.popup.id)) show(next.popup);
  } catch {
    // A bell nobody can ring is not worth an error state.
  }
}

function show(record: Announcement): void {
  shownThisVisit.add(record.id);
  showing.value = record;
}

function dismiss(): void {
  const record = showing.value;
  showing.value = null;
  if (!record) return;
  void markRead(record.id).then(() => refresh({ popup: false })).catch(() => refresh({ popup: false }));
}

/**
 * The feed is fetched once when the header is built. A tab left open since
 * before an announcement was written would otherwise say there are none for
 * as long as it stays open — which is exactly when somebody opens the bell to
 * check. Opening it is the moment the answer is wanted, so that is when it is
 * asked for.
 */
function onTrigger(wasOpen: boolean): void {
  if (!wasOpen) void refresh({ popup: false });
}

onMounted(() => void refresh());
</script>

<template>
  <OaMenu ref="menu" menu-class="oa-menu-announce">
    <template #trigger="{ open, toggle }">
      <OaIconButton
        class="oa-icon-btn oa-bell"
        :label="label"
        aria-haspopup="menu"
        :aria-expanded="open ? 'true' : 'false'"
        @click="onTrigger(open); toggle()"
      >
        <IconBell :size="17" />
        <span v-if="feed.unread > 0" class="oa-bell-dot" />
      </OaIconButton>
    </template>

    <template #default="{ close }">
      <div class="oa-menu-head">
        <span class="oa-menu-head-name">{{ t('announcements') }}</span>
        <button
          v-if="feed.unread > 0"
          type="button"
          class="oa-menu-head-action"
          @click="close(); markAllRead().then(() => refresh({ popup: false }))"
        >
          {{ t('markAllRead') }}
        </button>
      </div>

      <p v-if="!feed.announcements.length" class="oa-menu-empty">{{ t('announcementsEmpty') }}</p>
      <OaMenuItem
        v-for="record in feed.announcements"
        :key="record.id"
        :title="record.title"
        :sub="relativeTime(record.updated_at)"
        @click="close(); show(record)"
      >
        <template v-if="!record.read" #leading>
          <span class="oa-menu-unread" />
        </template>
      </OaMenuItem>
    </template>
  </OaMenu>

  <AnnouncementSheet v-if="showing" :announcement="showing" @dismiss="dismiss" />
</template>
