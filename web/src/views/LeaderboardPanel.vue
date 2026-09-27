<script setup lang="ts">
// Where the reader stands among everybody else.
//
// A column beside the chat, like Usage and Uptime, and deliberately not the
// backoffice's ranking with a different stylesheet. That one answers "where
// does the money go", so it is ordered by credits and names every account; a
// reader's board answers "how does my use compare", so it carries no prices,
// no account ids, and whatever names the operator decided to publish.
//
// The reader's own standing is pinned above the list rather than left to be
// found in it. Being four hundredth is the common case, and a board that only
// shows the top twenty answers the question for twenty people.

import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { ApiError } from '@/api/client';
import {
  fetchLeaderboard,
  type Leaderboard,
  type LeaderboardEntry,
  type LeaderboardMetric,
  type LeaderboardPeriod,
} from '@/api/leaderboard';
import OaFormSection from '@/components/OaFormSection.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { IconCollapse, IconExpand, IconTrophy } from '@/icons';
import { initials, safeAvatar } from '@/lib/account';
import { compactNumber } from '@/lib/format';

const router = useRouter();

const panel = ref<InstanceType<typeof OaPanel> | null>(null);
const fullscreen = ref(false);

const loading = ref(true);
const error = ref('');
const data = ref<Leaderboard | null>(null);

const period = ref<LeaderboardPeriod>('week');
const metric = ref<LeaderboardMetric>('tokens');

const PERIODS: Array<{ value: LeaderboardPeriod; label: 'boardDay' | 'boardWeek' | 'boardMonth' }> = [
  { value: 'day', label: 'boardDay' },
  { value: 'week', label: 'boardWeek' },
  { value: 'month', label: 'boardMonth' },
];

function toggleFullscreen(): void {
  fullscreen.value = panel.value?.toggleFullscreen() ?? false;
}

/**
 * What to call a row.
 *
 * A row with no name is not missing one: the operator asked for an anonymous
 * board, and a place number is the whole identity. The reader's own row is
 * named whatever the mode, which is why this is asked per row.
 */
function nameFor(entry: LeaderboardEntry): string {
  if (entry.name) return entry.name;
  return entry.self ? t('boardYou') : t('boardAnonymous', { rank: entry.rank });
}

/**
 * What goes in the circle where a picture would be.
 *
 * A name's initial, except on an anonymous row, where there is no name to
 * take one from: running the place label through `initials` produced word
 * salad ("第 1 名" → "第名"), which is what a name-shaped helper does to
 * something that is not a name. The place number is the identity there, so
 * it is also the mark.
 */
function avatarLetter(entry: LeaderboardEntry): string {
  return entry.name ? initials(entry.name) : String(entry.rank);
}

function figure(value: number): string {
  return metric.value === 'requests'
    ? t('boardRequests', { count: compactNumber(value) })
    : t('boardTokens', { count: compactNumber(value) });
}

/** The bar's width, as a share of the leader rather than of the total. */
function share(value: number): string {
  const top = data.value?.accounts[0]?.value ?? 0;
  return top > 0 ? `${Math.max(2, (value / top) * 100)}%` : '0%';
}

function isAbortError(err: unknown): boolean {
  return (
    (err instanceof DOMException && err.name === 'AbortError') ||
    (err instanceof Error && err.name === 'AbortError') ||
    (typeof err === 'object' && err !== null && (err as { name?: string }).name === 'AbortError')
  );
}

function applyMetricSort(board: Leaderboard, currentMetric: LeaderboardMetric): void {
  board.metric = currentMetric;

  if (board.accounts && Array.isArray(board.accounts)) {
    board.accounts.sort((a, b) => {
      const valA = currentMetric === 'requests' ? (a.requests ?? 0) : (a.tokens ?? 0);
      const valB = currentMetric === 'requests' ? (b.requests ?? 0) : (b.tokens ?? 0);
      if (valB !== valA) return valB - valA;
      const tieA = currentMetric === 'requests' ? (a.tokens ?? 0) : (a.requests ?? 0);
      const tieB = currentMetric === 'requests' ? (b.tokens ?? 0) : (b.requests ?? 0);
      if (tieB !== tieA) return tieB - tieA;
      return (a.name ?? '').localeCompare(b.name ?? '');
    });

    for (let i = 0; i < board.accounts.length; i++) {
      const a = board.accounts[i];
      if (!a) continue;
      a.value = currentMetric === 'requests' ? (a.requests ?? 0) : (a.tokens ?? 0);
      const prev = i > 0 ? board.accounts[i - 1] : undefined;
      if (prev && a.value === prev.value) {
        a.rank = prev.rank;
      } else {
        a.rank = i + 1;
      }
    }
  }

  if (board.me && board.accounts && Array.isArray(board.accounts)) {
    const selfEntry = board.accounts.find((a) => a.self);
    if (selfEntry) {
      board.me.rank = selfEntry.rank;
      board.me.value = selfEntry.value;
      let gap = 0;
      const selfIdx = board.accounts.indexOf(selfEntry);
      for (let j = selfIdx - 1; j >= 0; j--) {
        const higher = board.accounts[j];
        if (higher && higher.value > selfEntry.value) {
          gap = higher.value - selfEntry.value;
          break;
        }
      }
      board.me.gap = gap;
    }
  }

  if (board.models && board.models.length > 0) {
    board.models.sort((a, b) => {
      const valA = currentMetric === 'requests' ? (a.requests ?? 0) : (a.tokens ?? 0);
      const valB = currentMetric === 'requests' ? (b.requests ?? 0) : (b.tokens ?? 0);
      if (valB !== valA) return valB - valA;
      if (b.users !== a.users) return b.users - a.users;
      return (a.name ?? '').localeCompare(b.name ?? '');
    });

    for (let i = 0; i < board.models.length; i++) {
      const m = board.models[i];
      if (!m) continue;
      m.value = currentMetric === 'requests' ? (m.requests ?? 0) : (m.tokens ?? 0);
      const prev = i > 0 ? board.models[i - 1] : undefined;
      if (prev && m.value === prev.value && m.users === prev.users) {
        m.rank = prev.rank;
      } else {
        m.rank = i + 1;
      }
    }
  }
}

let abortController: AbortController | null = null;

async function load(): Promise<void> {
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
  const controller = new AbortController();
  abortController = controller;

  loading.value = true;
  error.value = '';
  try {
    const result = await fetchLeaderboard(period.value, metric.value, controller.signal);
    if (controller.signal.aborted) return;
    result.period = period.value;
    applyMetricSort(result, metric.value);
    data.value = result;
  } catch (failure) {
    if (controller.signal.aborted || isAbortError(failure)) return;
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    if (abortController === controller) {
      abortController = null;
      loading.value = false;
    }
  }
}

// Switching the period asks the server for the new window, aborting any
// prior in-flight request. Switching the metric re-sorts the existing
// aggregate locally on the client without a new network round-trip.
watch(period, () => void load());

watch(metric, (nextMetric) => {
  if (data.value && data.value.period === period.value) {
    applyMetricSort(data.value, nextMetric);
    data.value = {
      ...data.value,
      accounts: [...data.value.accounts],
      ...(data.value.models ? { models: [...data.value.models] } : {}),
      me: { ...data.value.me },
    };
  } else {
    void load();
  }
});

onMounted(load);

onBeforeUnmount(() => {
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
});
</script>

<template>
  <OaPanel
    ref="panel"
    :title="t('leaderboardTitle')"
    :footer="false"
    :width="460"
    :busy="loading"
    :error="error"
    body-class="oa-board-body"
    @close="router.replace('/')"
  >
    <template #actions>
      <OaIconButton
        class="oa-icon-btn"
        :label="t(fullscreen ? 'exitFullscreen' : 'fullscreen')"
        @click="toggleFullscreen"
      >
        <IconCollapse v-if="fullscreen" :size="16" />
        <IconExpand v-else :size="16" />
      </OaIconButton>
    </template>

    <div v-if="data" class="oa-board-content">
      <div class="oa-board-controls">
        <div class="oa-segment" role="group" :aria-label="t('boardPeriod')">
          <button
            v-for="entry in PERIODS"
            :key="entry.value"
            type="button"
            :aria-pressed="period === entry.value"
            @click="period = entry.value"
          >{{ t(entry.label) }}</button>
        </div>
        <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
          <button type="button" :aria-pressed="metric === 'tokens'" @click="metric = 'tokens'">
            {{ t('statTokens') }}
          </button>
          <button type="button" :aria-pressed="metric === 'requests'" @click="metric = 'requests'">
            {{ t('statRequests') }}
          </button>
        </div>
      </div>

      <!-- Pinned, because the reader's place is the one fact this screen
           exists to tell them and it is usually not on the visible list. -->
      <div class="oa-board-me">
        <div class="oa-board-me-rank">
          <template v-if="data.me.rank">
            <span class="oa-board-me-hash">#</span>{{ data.me.rank }}
          </template>
          <IconTrophy v-else :size="20" />
        </div>
        <div class="oa-board-me-text">
          <span class="oa-board-me-title">
            {{ data.me.rank
              ? t('boardYourRank', { total: data.me.participants })
              : t('boardNotRanked') }}
          </span>
          <span class="oa-field-hint">
            {{ data.me.rank ? figure(data.me.value) : t('boardNotRankedHint') }}
            <template v-if="data.me.gap > 0">
              · {{ t('boardBehind', { amount: compactNumber(data.me.gap) }) }}
            </template>
          </span>
        </div>
      </div>

      <OaFormSection :title="t('boardAccounts')" />
      <p v-if="!data.accounts.length" class="oa-menu-empty">{{ t('nothingYet') }}</p>
      <ol v-else class="oa-board-list">
        <li
          v-for="(entry, index) in data.accounts"
          :key="index"
          class="oa-board-row"
          :class="{ self: entry.self, podium: entry.rank <= 3 }"
        >
          <span class="oa-board-rank" :data-rank="entry.rank">{{ entry.rank }}</span>
          <span class="oa-avatar oa-board-avatar" aria-hidden="true">
            <img
              v-if="entry.avatar && safeAvatar(entry.avatar)"
              :src="safeAvatar(entry.avatar) ?? ''"
              alt=""
              :draggable="false"
            >
            <template v-else>{{ avatarLetter(entry) }}</template>
          </span>
          <span class="oa-board-main">
            <span class="oa-board-name">
              {{ nameFor(entry) }}
              <span v-if="entry.self" class="oa-badge">{{ t('boardYou') }}</span>
            </span>
            <span v-if="entry.handle" class="oa-field-hint">@{{ entry.handle }}</span>
            <span class="oa-board-track" aria-hidden="true">
              <span :style="{ width: share(entry.value) }" />
            </span>
          </span>
          <span class="oa-board-value">{{ compactNumber(entry.value) }}</span>
        </li>
      </ol>

      <template v-if="data.models?.length">
        <OaFormSection :title="t('boardModels')" :hint="t('boardModelsHint')" />
        <ol class="oa-board-list">
          <li v-for="entry in data.models" :key="entry.name" class="oa-board-row">
            <span class="oa-board-rank" :data-rank="entry.rank">{{ entry.rank }}</span>
            <span class="oa-board-main">
              <span class="oa-board-name">{{ entry.name }}</span>
              <span class="oa-field-hint">{{ figure(entry.value) }}</span>
            </span>
            <span class="oa-board-value">{{ t('boardUsersCount', { count: entry.users }) }}</span>
          </li>
        </ol>
      </template>
    </div>
  </OaPanel>
</template>
