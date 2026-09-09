<script setup lang="ts">
// The Uptime panel.
//
// A side panel presented beside the chat showing real-time availability of
// models and the server instance. Configurable by administrators whether
// regular users may view it.

import { computed, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { api } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { formatUptime } from '@/lib/format';

interface ModelUptimeItem {
  id: string;
  display_name: string;
  provider_name?: string;
  enabled: boolean;
  uptime?: number;
  state: 'up' | 'degraded' | 'down' | 'unknown';
  total?: number;
}

interface UptimeResponse {
  uptime_sec: number;
  models: ModelUptimeItem[];
}

const router = useRouter();

const loading = ref(true);
const error = ref('');
const data = ref<UptimeResponse | null>(null);

const systemUptime = computed(() => (data.value ? formatUptime(data.value.uptime_sec) : '—'));

const hasOutage = computed(() =>
  data.value?.models.some((m) => m.state === 'down') ?? false,
);

const hasDegraded = computed(() =>
  data.value?.models.some((m) => m.state === 'degraded') ?? false,
);

async function load(): Promise<void> {
  loading.value = true;
  error.value = '';
  try {
    data.value = await api.get<UptimeResponse>('/api/uptime');
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('failed');
  } finally {
    loading.value = false;
  }
}

onMounted(load);
</script>

<template>
  <OaPanel
    :title="t('uptimeTitle')"
    :footer="false"
    :width="460"
    :busy="loading"
    :error="error"
    @close="router.replace('/')"
  >
    <div v-if="data" class="oa-uptime-content">
      <div
        class="oa-uptime-banner"
        :class="{ outage: hasOutage, degraded: !hasOutage && hasDegraded }"
      >
        <span class="oa-uptime-banner-dot" />
        <div class="oa-uptime-banner-text">
          <span class="oa-uptime-banner-title">
            {{
              hasOutage
                ? t('someSystemsOutage')
                : hasDegraded
                  ? t('someSystemsDegraded')
                  : t('allSystemsOperational')
            }}
          </span>
          <span class="oa-uptime-banner-sub">
            {{ t('uptimeSystem') }}: {{ systemUptime }}
          </span>
        </div>
      </div>

      <OaFormSection :title="t('uptimeModels')" />
      <div v-if="!data.models.length" class="oa-field-hint">
        {{ t('healthNoEvidence') }}
      </div>
      <div v-else class="oa-uptime-list">
        <div
          v-for="model in data.models"
          :key="model.id"
          class="oa-uptime-row"
        >
          <div class="oa-uptime-row-main">
            <span class="oa-uptime-status-dot" :class="model.state" />
            <div class="oa-uptime-row-names">
              <span class="oa-uptime-row-title">{{ model.display_name }}</span>
              <span v-if="model.provider_name" class="oa-field-hint">{{ model.provider_name }}</span>
            </div>
          </div>
          <div class="oa-uptime-row-meta">
            <span v-if="model.uptime !== undefined" class="oa-uptime-percentage">
              {{ (model.uptime * 100).toFixed(model.uptime >= 0.995 ? 0 : 1) }}%
            </span>
            <OaBadge
              :tone="model.state === 'up' ? 'default' : model.state === 'degraded' ? 'warning' : model.state === 'down' ? 'danger' : 'muted'"
            >
              {{
                model.state === 'up'
                  ? t('uptimeOperational')
                  : model.state === 'degraded'
                    ? t('uptimeDegraded')
                    : model.state === 'down'
                      ? t('uptimeOutage')
                      : t('uptimeNoData')
              }}
            </OaBadge>
          </div>
        </div>
      </div>
    </div>
  </OaPanel>
</template>
