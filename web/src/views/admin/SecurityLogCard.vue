<script setup lang="ts">
// The security log: what the guards on the front door decided, a page at a
// time. Read-only, so it owns its own rows and paging and nothing on the
// settings form beside it depends on them.

import { onMounted, ref } from 'vue';
import { adminApi, type SecurityEvent } from '@/admin/api';
import OaBadge from '@/components/OaBadge.vue';
import OaPagination from '@/components/OaPagination.vue';
import type { PageState } from '@/components/table-types';
import { t } from '@/composables/useI18n';
import { IconFile } from '@/icons';
import { absoluteTime } from '@/lib/format';
import { rememberedPageSize } from '@/lib/page-size';
import { maskLog, maskUser } from '@/admin/safeMode';
import AdminControlCard from './AdminControlCard.vue';
import { eventLabel, formatEventReason, reviewDecision } from './security-text';
import { failureText } from './shared';

// A failed read is reported in the page's own flash line, where it has always
// appeared, rather than in a second place inside the card.
const emit = defineEmits<{ failed: [message: string] }>();

const events = ref<SecurityEvent[]>([]);
const total = ref(0);
const page = ref<PageState>({ page: 1, pageSize: rememberedPageSize() });
const loading = ref(false);
// A page turned while the last one is still on its way: only the newest
// answer may land.
let request = 0;

async function load(): Promise<void> {
  const ticket = ++request;
  loading.value = true;
  try {
    const result = await adminApi.securityEvents(`?limit=${page.value.pageSize}&offset=${(page.value.page - 1) * page.value.pageSize}`);
    if (ticket !== request) return;
    events.value = result.events ?? [];
    total.value = result.total;
  } catch (failure) {
    emit('failed', failureText(failure));
  } finally {
    if (ticket === request) loading.value = false;
  }
}

function change(next: PageState): void { page.value = next; void load(); }

onMounted(load);
</script>

<template>
  <AdminControlCard id="secSecurityLog" :title="t('secSecurityLog')" :icon="IconFile" :hint="t('securityLogHint')" class="oa-control-card-wide">
    <template #actions>
      <button type="button" class="oa-btn" :disabled="loading" @click="load">
        {{ t('refresh') }}
      </button>
    </template>
    <p v-if="loading && !events.length" class="oa-table-empty">{{ t('loading') }}</p>
    <p v-else-if="!events.length" class="oa-table-empty">{{ t('securityLogEmpty') }}</p>
    <!-- Its own rows, not the request log's: those are a six-column grid,
         and one block dropped into it lands in the 64px time column and
         wraps a word to a line. A decision is a sentence, not a table
         row, so it gets a heading, a line of who and where, and room to
         say why. -->
    <ol v-else class="oa-event-list" :class="{ busy: loading }">
      <li v-for="event in events" :key="event.id" class="oa-event" :class="`is-${event.severity}`">
        <div class="oa-event-head">
          <span class="oa-event-title">{{ eventLabel(event.event) }}</span>
          <OaBadge
            v-if="event.decision"
            :tone="event.severity === 'danger'
              ? 'danger' : event.severity === 'warning' ? 'warning' : 'muted'"
          >{{ reviewDecision(event.decision) }}</OaBadge>
          <time class="oa-event-time">{{ absoluteTime(event.at) }}</time>
        </div>
        <div v-if="event.username || event.ip || event.actor_username" class="oa-event-meta">
          <span v-if="event.username">@{{ maskUser(event.username) }}</span>
          <span v-if="event.ip" class="mono">{{ maskLog(event.ip) }}</span>
          <span v-if="event.actor_username">
            {{ t('securityLogActor', { name: `@${maskUser(event.actor_username)}` }) }}
          </span>
        </div>
        <p v-if="event.reason" class="oa-event-reason">{{ formatEventReason(event.reason) }}</p>
      </li>
    </ol>
    <OaPagination v-bind="page" :total="total" :busy="loading" @change="change" />
  </AdminControlCard>
</template>
