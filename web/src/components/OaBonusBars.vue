<script setup lang="ts">
// The bonus bars an account holds, and the switch on the ones it may choose.
//
// A bar is an allowance beside the three windows: spent before them when it is
// on, after them when it is not. Which of those it is right now is what the
// line under each bar says, because the same figure means something different
// in each — credit that is being spent, and credit that is being kept.
//
// The panel that hosts this asks it to read again (`refreshKey`) on its own
// timer and after anything that could have changed a bar, a check-in reward,
// say. That read is quiet, like the panel's other refreshes: a failure leaves
// the bars that are on screen, because the last good figures are a better
// answer than an error where they were. The first read, and the one after a
// switch, say what went wrong.

import { computed, onMounted, ref, watch } from 'vue';
import { ApiError } from '@/api/client';
import { fetchBonus, setBonusChoice, type BonusBar } from '@/api/bonus';
import OaBadge from '@/components/OaBadge.vue';
import OaSwitchField from '@/components/OaSwitchField.vue';
import { t, tn } from '@/composables/useI18n';
import { absoluteTime } from '@/lib/format';

const props = defineProps<{
  /** Changes whenever the host knows something moved. */
  refreshKey?: number;
}>();

const bars = ref<BonusBar[]>([]);
const error = ref('');
const busy = ref('');

async function load(quiet = false): Promise<void> {
  try {
    bars.value = (await fetchBonus()).bars;
    error.value = '';
  } catch (caught) {
    if (quiet) return;
    error.value = caught instanceof ApiError ? caught.message : t('bonusLoadFailed');
  }
}

async function choose(bar: BonusBar, enabled: boolean): Promise<void> {
  busy.value = bar.bar_id;
  try {
    await setBonusChoice(bar.bar_id, enabled);
    bar.enabled = enabled;
    error.value = '';
  } catch (caught) {
    error.value = caught instanceof ApiError ? caught.message : t('bonusChoiceFailed');
  } finally {
    busy.value = '';
  }
}

function credits(amount: number): string {
  return String(Math.round(amount * 100) / 100);
}

function figure(bar: BonusBar): string {
  if (bar.exhausted) return t('bonusExhausted');
  if (bar.show_total && bar.remaining !== null && bar.total !== null) {
    return t('bonusLeft', { left: credits(bar.remaining), total: credits(bar.total) });
  }
  return t('bonusPercentLeft', { percent: bar.percent });
}

/** What is true of the bar right now, in a line. */
function detail(bar: BonusBar): string {
  const parts: string[] = [];
  parts.push(bar.kind === 'reserve' || !bar.enabled ? t('bonusAfterAllowance') : t('bonusBeforeAllowance'));
  parts.push(bar.expires_at > 0 ? t('bonusExpires', { when: absoluteTime(bar.expires_at) }) : t('bonusNoExpiry'));
  if (bar.model_ids.length) parts.push(tn(bar.model_ids.length, 'bonusModelsOne', 'bonusModelsOther'));
  return parts.join(' · ');
}

const visible = computed(() => bars.value);

onMounted(() => load());
watch(() => props.refreshKey, () => load(true));
</script>

<template>
  <div v-if="visible.length || error" class="oa-bonus">
    <h3 class="oa-drawer-subhead">{{ t('secBonus') }}</h3>
    <p v-if="error" class="oa-field-hint">{{ error }}</p>
    <div class="oa-bonus-list">
      <div v-for="bar in visible" :key="bar.bar_id" class="oa-bonus-bar" :class="{ 'is-spent': bar.exhausted }">
        <div class="oa-usage-row">
          <span class="oa-bonus-name">
            {{ bar.name }}
            <OaBadge v-if="bar.kind === 'reserve'" tone="muted">{{ t('bonusKindReserve') }}</OaBadge>
          </span>
          <span class="oa-usage-value">{{ figure(bar) }}</span>
        </div>
        <div class="oa-meter">
          <div class="oa-meter-fill" :style="{ width: `${bar.percent}%` }" />
        </div>
        <div class="oa-usage-reset">{{ detail(bar) }}</div>
        <!-- Operator-written, so text and never markup. -->
        <div v-if="bar.description" class="oa-usage-reset">{{ bar.description }}</div>
        <OaSwitchField
          v-if="bar.choosable"
          :model-value="bar.enabled"
          :label="t('bonusUseFirst')"
          :hint="t('bonusUseFirstHint')"
          :disabled="busy === bar.bar_id"
          @update:model-value="choose(bar, $event)"
        />
      </div>
    </div>
  </div>
</template>
