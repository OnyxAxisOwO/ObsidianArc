<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { ArrowUpCircle, ArrowUpRight, ArrowDownLeft, ArrowUpLeft, ChartNoAxesCombined, CircleAlert, Coins, RefreshCw, CalendarDays, Activity } from 'lucide-vue-next';
import { adminApi, type Dashboard, type UsageBreakdown } from '@/admin/api';
import { hasCloudflareNotice, loadUpdateStatus, releaseLink, updateStatus } from '@/admin/update';
import OaChart from '@/components/OaChart.vue';
import OaCellStack from '@/components/OaCellStack.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import { currentLanguage, t, tn } from '@/composables/useI18n';
import { IconSpark, IconUsers, IconServer, IconChevron } from '@/icons';
import { compactNumber, relativeTime, tokenFigure } from '@/lib/format';
import { fold, type ChartShape } from '@/lib/chart';
import { canAdmin, isSuperAdmin } from '@/stores/session';
import { maskUser, maskProvider, maskBilling } from '@/admin/safeMode';
import AdminDashboardTrend from './AdminDashboardTrend.vue';
import AdminFailure from './AdminFailure.vue';
import StatusBadge from './StatusBadge.vue';
import { useAdminView } from './adminView';
import UsageDelta from './usage/UsageDelta.vue';
import UsageHeatmap from './usage/UsageHeatmap.vue';
import { formatDuration, percent } from './usage/scale';

const view = useAdminView();
view.setTitle(t('navDashboard'));
const data = ref<Dashboard | null>(null);
const error = ref('');
const busy = ref(false);
const updatedAt = ref(0);
const ranking = ref<'models' | 'users'>('models');
const shape = ref<ChartShape>(dashboardShape);
const heatMetric = ref<'requests' | 'total_tokens'>(dashboardHeatMetric);
const locale = computed(() => currentLanguage() === 'zh' ? 'zh-CN' : 'en-US');
const dateLabel = computed(() => new Date(updatedAt.value || Date.now()).toLocaleDateString(locale.value, {
  month: 'long', day: 'numeric', weekday: 'long',
}));
const updatedLabel = computed(() => updatedAt.value ? t('dashboardUpdated', {
  time: new Date(updatedAt.value).toLocaleTimeString(locale.value, { hour: '2-digit', minute: '2-digit' }),
}) : '');

const detailsOpen = ref(false);
const detailsAnimating = ref(false);
let detailsTimer = 0;

function toggleDetails(): void {
  window.clearTimeout(detailsTimer);
  if (detailsOpen.value) {
    detailsOpen.value = false;
    detailsAnimating.value = true;
    detailsTimer = window.setTimeout(() => {
      detailsAnimating.value = false;
    }, 280);
  } else {
    detailsOpen.value = true;
    detailsAnimating.value = true;
    detailsTimer = window.setTimeout(() => {
      detailsAnimating.value = false;
    }, 280);
  }
}

onBeforeUnmount(() => {
  window.clearTimeout(detailsTimer);
});

// The day before, for the arrows. A server older than this page sends none,
// and a missing comparison is drawn as no arrow rather than as a fall to zero.
const before = computed(() => data.value?.prev_24h ?? null);
const successRate = computed(() => {
  const totals = data.value?.last_24h;
  return totals?.requests ? (totals.requests - totals.errors) / totals.requests : null;
});
const failureRate = computed(() => {
  const totals = data.value?.last_24h;
  return totals?.requests ? percent(totals.errors / totals.requests) : '—';
});
const latency = computed(() => {
  const totals = data.value?.last_24h;
  return totals?.requests ? (totals.duration_ms ?? 0) / totals.requests : 0;
});
const latencyBefore = computed(() => {
  const totals = before.value;
  return totals?.requests ? (totals.duration_ms ?? 0) / totals.requests : null;
});

const resources = computed(() => {
  const counts = data.value?.counts;
  if (!counts) return [];
  return [
    { key: 'users', icon: IconUsers, label: t('statUsers'), value: counts.users, note: t('nActive', { count: counts.active_users }) },
    { key: 'providers', icon: IconServer, label: t('statProviders'), value: counts.providers, note: t('nEnabled', { count: counts.enabled_providers }) },
    { key: 'models', icon: IconSpark, label: t('statModels'), value: counts.models, note: t('nEnabled', { count: counts.enabled_models }) },
  ];
});
const rankedRows = computed(() => ranking.value === 'models' ? data.value?.top_models ?? [] : data.value?.top_users ?? []);
// Ranked by tokens, not credits. Credits are tokens times a price the
// operator set per model, so a free model — weight 0 — carrying half the
// traffic vanished from this chart entirely, and a pricey one looked busier
// than it was. "Where the load goes" is a question about tokens; what it
// cost is still in the breakdown table underneath.
const rankedSlices = computed(() => fold(rankedRows.value.map((row) => ({
  key: row.key,
  label: ranking.value === 'users' ? maskUser(row.label || row.key || '—') : (row.label || row.key || '—'),
  value: row.total_tokens,
})), 5, t('chartOther')));
const rankedTotal = computed(() => rankedSlices.value.reduce((sum, row) => sum + row.value, 0));
function share(value: number): string {
  return rankedTotal.value ? `${(value / rankedTotal.value * 100).toFixed(1)}%` : '0%';
}
function rowOf(key: string): UsageBreakdown | undefined {
  return rankedRows.value.find((entry) => entry.key === key);
}
function rowNote(key: string): string {
  const row = rowOf(key);
  return row ? t('dashboardRankNote', { requests: compactNumber(row.requests), credits: maskBilling(compactNumber(row.credits)) }) : '';
}
/** Under a model, how many people use it; under an account, how many models. */
function rowReach(key: string): string {
  const row = rowOf(key);
  if (!row) return '';
  if (ranking.value === 'models') return row.users ? tn(row.users, 'boardUsersOne', 'boardUsersOther', { count: row.users }) : '';
  return row.models ? tn(row.models, 'boardModelsOne', 'boardModelsOther', { count: row.models }) : '';
}
function exactCredits(row: UsageBreakdown): string { return row.credits.toLocaleString(locale.value, { maximumFractionDigits: 2 }); }
function onShape(next: ChartShape): void { shape.value = next; dashboardShape = next; }
function initial(name: string): string { return Array.from(name)[0]?.toLocaleUpperCase() ?? '·'; }

async function load(): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    // The server orders the breakdown table; asking for the same metric the
    // chart draws keeps the two in the same order.
    data.value = await adminApi.dashboard('tokens');
    updatedAt.value = Date.now();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}
// Both notices are for the super administrator alone; the store only ever
// holds an answer for one, and the check repeats that rule where it is read.
const availableUpdate = computed(() => (isSuperAdmin.value && updateStatus.value?.update_available ? updateStatus.value : null));
const updateLink = computed(() => (availableUpdate.value ? releaseLink(availableUpdate.value) : null));
const cloudflareNotice = computed(() => isSuperAdmin.value && hasCloudflareNotice(updateStatus.value));

onMounted(() => {
  void load();
  if (isSuperAdmin.value) void loadUpdateStatus();
});

function onHeatMetric(next: 'requests' | 'total_tokens'): void { heatMetric.value = next; dashboardHeatMetric = next; }
</script>

<script lang="ts">
// Preserve the existing chart preference when the administrator returns.
let dashboardShape: ChartShape = 'bar';
let dashboardHeatMetric: 'requests' | 'total_tokens' = 'requests';
</script>

<template>
  <AdminFailure v-if="error" :message="error" @retry="load" />
  <div v-if="!data && busy" class="oa-dashboard-loading" role="status" :aria-label="t('loading')">
    <span>{{ t('loading') }}</span><div /><div /><div /><div />
  </div>

  <div v-if="data" class="oa-dashboard" :aria-busy="busy">
    <header class="oa-dashboard-intro">
      <div>
        <h1>{{ t('dashboardHeading') }}</h1>
        <p v-if="t('dashboardIntro')">{{ t('dashboardIntro') }}</p>
      </div>
      <div class="oa-dashboard-intro-meta">
        <span class="oa-dashboard-date"><CalendarDays :size="14" aria-hidden="true" />{{ dateLabel }}</span>
        <div><span class="oa-dashboard-updated" role="status">{{ updatedLabel }}</span>
          <OaIconButton class="oa-icon-btn oa-dashboard-refresh" :label="t('refresh')" :disabled="busy" @click="load">
            <RefreshCw :size="14" :class="{ 'is-refreshing': busy }" aria-hidden="true" />
          </OaIconButton>
        </div>
      </div>
    </header>

    <section v-if="availableUpdate" id="secUpdateNotice" class="oa-dashboard-card oa-dashboard-notice" role="status">
      <ArrowUpCircle :size="18" aria-hidden="true" />
      <div>
        <strong>{{ t('updateNoticeLine', { version: availableUpdate.latest }) }}</strong>
        <p>{{ t('updateCurrentVersion', { version: availableUpdate.current }) }}</p>
      </div>
      <a v-if="updateLink" class="oa-dashboard-link" :href="updateLink" target="_blank" rel="noopener noreferrer">{{ t('updateOpenRelease') }}<ArrowUpRight :size="14" aria-hidden="true" /></a>
    </section>
    <section v-if="cloudflareNotice" id="secCloudflareNotice" class="oa-dashboard-card oa-dashboard-notice is-warning" role="status">
      <CircleAlert :size="18" aria-hidden="true" />
      <div>
        <strong>{{ t('cloudflareNoticeTitle') }}</strong>
        <p>{{ t('cloudflareNoticeBody') }}</p>
      </div>
    </section>

    <section class="oa-dashboard-metrics" :aria-label="t('secLast24h')">
      <article class="oa-dashboard-metric oa-dashboard-metric-featured">
        <div class="oa-dashboard-metric-top"><span>{{ t('statRequests') }}</span><ChartNoAxesCombined :size="18" aria-hidden="true" /></div>
        <strong :title="data.last_24h.requests.toLocaleString(locale)">{{ compactNumber(data.last_24h.requests) }}</strong>
        <div class="oa-dashboard-metric-note">
          <UsageDelta :current="data.last_24h.requests" :previous="before?.requests" />
          <span>{{ t('dashboardVsDayBefore') }}</span>
        </div>
        <svg class="oa-dashboard-metric-orbit" viewBox="0 0 160 160" aria-hidden="true"><circle cx="132" cy="132" r="38" /><circle cx="132" cy="132" r="66" /><circle cx="132" cy="132" r="94" /></svg>
      </article>
      <article class="oa-dashboard-metric">
        <div class="oa-dashboard-metric-top"><span>{{ t('statTokens') }}</span><IconSpark :size="18" /></div>
        <strong :title="maskBilling(data.last_24h.total_tokens.toLocaleString(locale))">{{ maskBilling(compactNumber(data.last_24h.total_tokens)) }}</strong>
        <div class="oa-dashboard-metric-note oa-dashboard-token-split">
          <UsageDelta :current="data.last_24h.total_tokens" :previous="before?.total_tokens" />
          <span><ArrowDownLeft :size="12" aria-hidden="true" />{{ maskBilling(compactNumber(data.last_24h.input_tokens)) }}</span>
          <span><ArrowUpLeft :size="12" aria-hidden="true" />{{ maskBilling(compactNumber(data.last_24h.output_tokens)) }}</span>
        </div>
      </article>
      <article class="oa-dashboard-metric">
        <div class="oa-dashboard-metric-top"><span>{{ t('statCredits') }}</span><Coins :size="18" aria-hidden="true" /></div>
        <strong :title="maskBilling(data.last_24h.credits.toLocaleString(locale))">{{ maskBilling(compactNumber(Math.round(data.last_24h.credits * 100) / 100)) }}</strong>
        <div class="oa-dashboard-metric-note">
          <UsageDelta :current="data.last_24h.credits" :previous="before?.credits" />
          <span>{{ t('dashboardWeeklyCredits', { value: maskBilling(compactNumber(Math.round(data.last_7d.credits))) }) }}</span>
        </div>
      </article>
      <article class="oa-dashboard-metric">
        <div class="oa-dashboard-metric-top"><span>{{ t('kpiActiveUsers') }}</span><IconUsers :size="18" /></div>
        <strong>{{ compactNumber(data.last_24h.users ?? 0) }}</strong>
        <div class="oa-dashboard-metric-note">
          <UsageDelta :current="data.last_24h.users ?? 0" :previous="before?.users" />
          <span v-if="data.last_24h.models">{{ t('kpiModelsUsed', { count: data.last_24h.models }) }}</span>
        </div>
      </article>
    </section>

    <section id="secInstance" class="oa-dashboard-resources" :aria-label="t('secInstance')">
      <div class="oa-dashboard-resources-label"><span class="oa-dashboard-dot" />{{ t('dashboardWorkspace') }}</div>
      <component :is="canAdmin(resource.key) ? 'RouterLink' : 'div'" v-for="resource in resources" :key="resource.key"
        :to="canAdmin(resource.key) ? `/admin/${resource.key}` : undefined" class="oa-dashboard-resource">
        <span class="oa-dashboard-resource-icon"><component :is="resource.icon" :size="17" /></span>
        <span class="oa-dashboard-resource-copy"><span>{{ resource.label }} <strong>{{ resource.value.toLocaleString(locale) }}</strong></span><small>{{ resource.note }}</small></span>
        <ArrowUpRight v-if="canAdmin(resource.key)" :size="14" aria-hidden="true" />
      </component>
    </section>

    <div class="oa-dashboard-middle">
      <AdminDashboardTrend :series="data.series" :totals="data.last_7d" :bucket-ms="data.bucket_ms" />

      <section id="secBusiestModels" class="oa-dashboard-card oa-dashboard-ranking">
        <div class="oa-dashboard-section-head">
          <div><span class="oa-dashboard-kicker">{{ t('secLast7d') }}</span><h2>{{ t('dashboardRanking') }}</h2></div>
          <div class="oa-segment" :aria-label="t('secRanking')" role="group">
            <button type="button" :aria-pressed="ranking === 'models'" @click="ranking = 'models'">{{ t('statModels') }}</button>
            <button type="button" :aria-pressed="ranking === 'users'" @click="ranking = 'users'">{{ t('statUsers') }}</button>
          </div>
        </div>
        <div class="oa-dashboard-rank-caption">
          <span>{{ t('dashboardRankHint') }}</span>
          <div class="oa-dashboard-shapes" :aria-label="t('chartShape')" role="group">
            <button type="button" :aria-pressed="shape === 'bar'" @click="onShape('bar')">{{ t('chartBar') }}</button>
            <button type="button" :aria-pressed="shape === 'pie'" @click="onShape('pie')">{{ t('chartPie') }}</button>
          </div>
        </div>
        <p v-if="!rankedSlices.length" class="oa-dashboard-empty"><IconSpark :size="26" />{{ t('nothingYet') }}</p>
        <ol v-else-if="shape === 'bar'" class="oa-dashboard-rank-list">
          <li v-for="(row, index) in rankedSlices" :key="row.key || 'other'" :title="`${row.label} · ${rowNote(row.key)}`">
            <span class="oa-dashboard-rank-number">{{ String(index + 1).padStart(2, '0') }}</span>
            <div class="oa-dashboard-rank-main">
              <div class="oa-dashboard-rank-label"><span>{{ row.label }}</span><strong :title="maskBilling(row.value.toLocaleString(locale))">{{ maskBilling(compactNumber(row.value)) }}</strong></div>
              <div class="oa-dashboard-rank-bottom">
                <span class="oa-dashboard-rank-track"><span :style="{ width: share(row.value) }" /></span>
                <small>{{ share(row.value) }}</small>
              </div>
              <small v-if="rowReach(row.key)" class="oa-dashboard-rank-reach">{{ rowReach(row.key) }} · {{ rowNote(row.key) }}</small>
            </div>
          </li>
        </ol>
        <OaChart v-else class="oa-dashboard-pie" shape="pie" :data="rankedSlices" :format="(value) => maskBilling(compactNumber(value))" :empty-text="t('nothingYet')" />
        <details
          v-if="rankedRows.length"
          class="oa-dashboard-ranking-details"
          :open="detailsOpen || detailsAnimating"
        >
          <summary
            class="oa-dashboard-details-summary"
            @click.prevent="toggleDetails"
          >
            <span>{{ t('dashboardRankDetails') }}</span>
            <IconChevron :size="12" class="oa-dashboard-details-chevron" :class="{ open: detailsOpen }" />
          </summary>
          <div
            class="oa-dashboard-details-collapse"
            :class="{ open: detailsOpen }"
            :aria-hidden="detailsOpen ? undefined : 'true'"
          >
            <div class="oa-dashboard-details-body">
              <div class="oa-dashboard-table-wrap" tabindex="0" :aria-label="t('dashboardRankDetails')">
                <table class="oa-dashboard-table">
                  <thead><tr><th>{{ ranking === 'models' ? t('colModel') : t('colUser') }}</th><th>{{ t('colRequests') }}</th><th>{{ t('colTokens') }}</th><th>{{ t('colCredits') }}</th></tr></thead>
                  <tbody><tr v-for="row in rankedRows" :key="row.key"><td>{{ ranking === 'users' ? maskUser(row.label || row.key || '—') : (row.label || row.key || '—') }}</td><td>{{ compactNumber(row.requests) }}</td><td>{{ maskBilling(compactNumber(row.total_tokens)) }}</td><td>{{ maskBilling(exactCredits(row)) }}</td></tr></tbody>
                </table>
              </div>
            </div>
          </div>
        </details>
      </section>
    </div>

    <div class="oa-dashboard-middle">
      <section id="secWhen" class="oa-dashboard-card">
        <div class="oa-dashboard-section-head">
          <div><span class="oa-dashboard-kicker">{{ t('dashboardHeatmapHint') }}</span><h2>{{ t('heatmapTitle') }}</h2></div>
          <div class="oa-segment" role="group" :aria-label="t('rankMetric')">
            <button type="button" :aria-pressed="heatMetric === 'requests'" @click="onHeatMetric('requests')">{{ t('statRequests') }}</button>
            <button type="button" :aria-pressed="heatMetric === 'total_tokens'" @click="onHeatMetric('total_tokens')">{{ t('statTokens') }}</button>
          </div>
        </div>
        <UsageHeatmap class="oa-dashboard-heatmap" :slots="data.heatmap ?? []" :metric="heatMetric" :format="(value) => heatMetric === 'requests' ? t('boardRequests', { count: compactNumber(value) }) : t('boardTokens', { count: maskBilling(compactNumber(value)) })" />
      </section>

      <section id="secHealth" class="oa-dashboard-card oa-dashboard-health">
        <div class="oa-dashboard-section-head">
          <div><span class="oa-dashboard-kicker">{{ t('secLast24h') }}</span><h2>{{ t('dashboardHealth') }}</h2></div>
          <Activity :size="18" aria-hidden="true" class="oa-dashboard-health-icon" />
        </div>
        <div class="oa-dashboard-health-rate">
          <strong>{{ successRate === null ? '—' : percent(successRate) }}</strong>
          <span>{{ t('dashboardSuccessRate') }}</span>
        </div>
        <div class="oa-dashboard-health-meter" aria-hidden="true"><span :style="{ width: `${(successRate ?? 0) * 100}%` }" /></div>
        <dl class="oa-dashboard-health-list">
          <div>
            <dt>{{ t('kpiLatency') }}</dt>
            <dd><UsageDelta :current="latency" :previous="latencyBefore" inverse />{{ formatDuration(latency) }}</dd>
          </div>
          <div :class="{ 'has-errors': data.last_24h.errors > 0 }">
            <dt><CircleAlert :size="13" aria-hidden="true" />{{ t('dashboardFailedRequests') }}</dt>
            <dd><UsageDelta :current="data.last_24h.errors" :previous="before?.errors" inverse />{{ compactNumber(data.last_24h.errors) }}</dd>
          </div>
          <div>
            <dt>{{ t('dashboardFailureShare') }}</dt>
            <dd>{{ data.last_24h.requests ? failureRate : t('noRequestsYet') }}</dd>
          </div>
        </dl>
      </section>
    </div>

    <section id="secRecentRequests" class="oa-dashboard-card oa-dashboard-recent">
      <div class="oa-dashboard-section-head">
        <div><h2>{{ t('secRecentRequests') }}</h2><p>{{ t('dashboardRecentHint') }}</p></div>
        <RouterLink v-if="canAdmin('usage')" to="/admin/usage" class="oa-dashboard-link">{{ t('dashboardViewUsage') }}<ArrowUpRight :size="14" aria-hidden="true" /></RouterLink>
      </div>
      <p v-if="!data.recent.length" class="oa-dashboard-empty"><ChartNoAxesCombined :size="26" aria-hidden="true" />{{ t('noRequestsYet') }}</p>
      <div v-else class="oa-dashboard-table-wrap" tabindex="0" :aria-label="t('secRecentRequests')">
        <table class="oa-dashboard-table oa-dashboard-recent-table">
          <thead><tr><th>{{ t('colUser') }}</th><th>{{ t('colModel') }}</th><th class="oa-dashboard-numeric">{{ t('colTokens') }}</th><th class="oa-dashboard-numeric">{{ t('colTook') }}</th><th>{{ t('colStatus') }}</th><th class="oa-dashboard-when">{{ t('colWhen') }}</th></tr></thead>
          <tbody>
            <tr v-for="row in data.recent" :key="row.id">
              <td>
                <span class="oa-dashboard-user">
                  <span class="oa-dashboard-avatar" aria-hidden="true">{{ maskUser(initial(row.nickname || row.username || row.user_id), '*') }}</span>
                  <OaCellStack :title="maskUser(row.nickname || row.username || row.user_id)" :sub="row.nickname && row.username ? `@${maskUser(row.username)}` : ''" />
                </span>
              </td>
              <td><OaCellStack :title="row.model_name || '—'" :sub="maskProvider(row.provider_name)" /></td>
              <td class="oa-dashboard-numeric" :title="row.estimated ? t('tokensEstimatedHint') : maskBilling(row.total_tokens.toLocaleString(locale))">{{ maskBilling(tokenFigure(row.total_tokens, row.estimated)) }}</td>
              <td class="oa-dashboard-numeric">{{ formatDuration(row.duration_ms) }}</td>
              <td><StatusBadge :status="row.status" :error-code="row.error_code" /></td>
              <td class="oa-dashboard-when" :title="new Date(row.started_at).toLocaleString(locale)">{{ relativeTime(row.started_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
    <footer class="oa-dashboard-foot"><span class="oa-dashboard-dot" />{{ t('dashboardFootnote') }}</footer>
  </div>
</template>
