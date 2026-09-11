<script setup lang="ts">
// Availability, liveness detection, and degradation settings.
//
// Consolidates model health policies, automated probing, service degradation
// thresholds, user-facing uptime visibility, and manual uptime reset into a
// single screen.

import { onMounted, ref } from 'vue';
import { adminApi, type ModelHealth } from '@/admin/api';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaConfirmButton from '@/components/OaConfirmButton.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaNumberField from '@/components/OaNumberField.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import { t } from '@/composables/useI18n';
import AdminFailure from './AdminFailure.vue';
import { useAdminView } from './adminView';

const view = useAdminView();
view.setTitle(t('navAvailability'), t('availabilitySubtitle'));

const error = ref('');
const loaded = ref(false);
const models = ref<ModelHealth[]>([]);
const flash = ref('');
const flashSuccess = ref('');
const saveLabel = ref('');
const busy = ref(false);
const resetting = ref(false);

const form = ref({
  healthProbe: true,
  healthWindow: 30 as number | null,
  healthDisableAfter: 0 as number | null,
  healthDisableBelow: 0 as number | null,
  healthWarnBelow: 90 as number | null,
  healthShowUsers: false,
  healthRetainDays: 14 as number | null,
});

function collect(): Record<string, string> {
  return {
    'health.probe': String(form.value.healthProbe),
    'health.window_minutes': String(form.value.healthWindow ?? 30),
    'health.disable_after': String(form.value.healthDisableAfter ?? 0),
    'health.disable_below': String(form.value.healthDisableBelow ?? 0),
    'health.warn_below': String(form.value.healthWarnBelow ?? 90),
    'health.show_users': String(form.value.healthShowUsers),
    'health.retain_days': String(form.value.healthRetainDays ?? 14),
  };
}

async function save(): Promise<void> {
  busy.value = true;
  saveLabel.value = t('saving');
  flash.value = '';
  flashSuccess.value = '';
  try {
    await adminApi.saveSettings(collect());
    saveLabel.value = t('saved');
    window.setTimeout(() => { saveLabel.value = ''; }, 1500);
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
    saveLabel.value = '';
  } finally {
    busy.value = false;
  }
}

async function resetUptime(): Promise<void> {
  resetting.value = true;
  flash.value = '';
  flashSuccess.value = '';
  try {
    await adminApi.resetHealth();
    flashSuccess.value = t('resetUptimeDone');
    window.setTimeout(() => { flashSuccess.value = ''; }, 3000);
    await load();
  } catch (failure) {
    flash.value = failure instanceof ApiError ? failure.message : String(failure);
  } finally {
    resetting.value = false;
  }
}

async function load(): Promise<void> {
  error.value = '';
  try {
    const [settingsData, healthData] = await Promise.all([
      adminApi.settings(),
      adminApi.health(),
    ]);
    const values = settingsData.settings;
    form.value = {
      healthProbe: values['health.probe'] !== 'false',
      healthWindow: Number(values['health.window_minutes'] ?? 30),
      healthDisableAfter: Number(values['health.disable_after'] ?? 0),
      healthDisableBelow: Number(values['health.disable_below'] ?? 0),
      healthWarnBelow: Number(values['health.warn_below'] ?? 90),
      healthShowUsers: values['health.show_users'] === 'true',
      healthRetainDays: Number(values['health.retain_days'] ?? 14),
    };
    models.value = healthData.models;
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);
</script>

<template>
  <Teleport :to="view.actionsHost">
    <button type="button" class="oa-btn primary" :disabled="busy" @click="save">
      {{ saveLabel || t('save') }}
    </button>
  </Teleport>

  <AdminFailure v-if="error" :message="error" @retry="load" />
  <p v-else-if="!loaded" class="oa-table-empty">{{ t('loading') }}</p>

  <div v-else class="oa-settings-panel">
    <p v-if="flashSuccess" class="oa-drawer-flash visible" style="color: #10b981;">
      {{ flashSuccess }}
    </p>

    <!-- 1. Service Degradation and Auto-disable Thresholds -->
    <OaFormSection id="secDegradationPolicy" :title="t('secDegradationPolicy')" :hint="t('healthWarnBelowHint')" />
    <OaNumberField
      v-model="form.healthWarnBelow"
      :label="t('healthWarnBelow')"
      :min="0"
      :max="100"
      :hint="t('healthWarnBelowHint')"
    />
    <OaNumberField
      v-model="form.healthDisableBelow"
      :label="t('healthDisableBelow')"
      :min="0"
      :max="100"
      :hint="t('healthDisableBelowHint')"
    />
    <OaNumberField
      v-model="form.healthDisableAfter"
      :label="t('healthDisableAfter')"
      :min="0"
      :hint="t('healthDisableAfterHint')"
    />

    <!-- 2. Probing and Window -->
    <OaFormSection id="secProbingWindow" :title="t('secProbingWindow')" :hint="t('livenessHint')" />
    <OaSwitchField v-model="form.healthProbe" :label="t('healthProbe')" :hint="t('healthProbeHint')" />
    <OaNumberField v-model="form.healthWindow" :label="t('healthWindow')" :min="1" :hint="t('healthWindowHint')" />
    <OaNumberField
      v-model="form.healthRetainDays"
      :label="t('healthRetainDays')"
      :min="1"
      :hint="t('healthRetainDaysHint')"
    />

    <!-- 3. User-facing Availability Visibility -->
    <OaFormSection id="secUserVisibility" :title="t('secUserVisibility')" />
    <OaSwitchField
      v-model="form.healthShowUsers"
      :label="t('healthShowUsers')"
      :hint="t('healthShowUsersHint')"
    />

    <!-- 4. Reset Uptime -->
    <OaFormSection id="secResetUptime" :title="t('secResetUptime')" :hint="t('resetUptimeHint')" />
    <div class="oa-field">
      <OaConfirmButton
        class="oa-btn"
        :label="t('resetUptime')"
        :armed-label="t('resetUptimeConfirm')"
        :armed-title="t('resetUptime')"
        :resting-title="t('resetUptime')"
        :disabled="resetting"
        @confirm="resetUptime"
      />
    </div>

    <!-- 5. Current Models Health Overview -->
    <div class="oa-uptime-section-head" style="margin-top: 24px;">
      <OaFormSection id="modelHealthOverview" :title="t('modelHealthOverview')" />
      <a href="/uptime" target="_blank" class="oa-uptime-action-btn">
        {{ t('viewUptimePage') }}
      </a>
    </div>

    <div v-if="!models.length" class="oa-field-hint">
      {{ t('healthNoEvidence') }}
    </div>
    <div v-else class="oa-uptime-list">
      <div v-for="m in models" :key="m.model_id" class="oa-uptime-row">
        <div class="oa-uptime-row-main">
          <span
            class="oa-uptime-status-dot"
            :class="m.status.state === 'up' ? 'up' : m.status.state === 'down' ? 'down' : ''"
          />
          <div class="oa-uptime-row-names">
            <span class="oa-uptime-row-title">{{ m.name }}</span>
            <span class="oa-field-hint">{{ m.provider }}</span>
          </div>
        </div>
        <div class="oa-uptime-row-meta">
          <span v-if="m.status.samples > 0" class="oa-uptime-percentage">
            {{ (m.status.uptime * 100).toFixed(1) }}%
            ({{ m.status.samples }} {{ t('statRequests').toLowerCase() }})
          </span>
          <OaBadge
            :tone="m.auto_disabled ? 'danger' : m.status.state === 'up' ? 'default' : m.status.state === 'down' ? 'danger' : 'muted'"
          >
            {{
              m.auto_disabled
                ? t('uptimeOutage')
                : m.status.state === 'up'
                  ? t('uptimeOperational')
                  : m.status.state === 'down'
                    ? t('uptimeOutage')
                    : t('uptimeNoData')
            }}
          </OaBadge>
        </div>
      </div>
    </div>

    <p class="oa-drawer-flash" :class="{ visible: !!flash }">{{ flash }}</p>
  </div>
</template>
