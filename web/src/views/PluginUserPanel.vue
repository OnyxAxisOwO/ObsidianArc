<script setup lang="ts">
// A panel a plugin brings for everybody signed in (UserPanelSpec), drawn by
// the core in the shape of the feedback panel: what it is for, a form, and
// the reader's own earlier submissions under it. A plugin whose forms an
// operator writes — a survey, a prize draw — lists them instead, and each
// opens at /x/<slug>/<id> with its own form, so a notification can link to
// the one it is about.
//
// The plugin declares the controls and does the requests; every pixel is
// drawn here with the same components the core's own panels use, so a plugin
// cannot bring markup or a style of its own into the chat. The human check
// is drawn and solved here too: the plugin only carries the proof.

import { computed, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { fetchPoWChallenge } from '@/api/auth';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t } from '@/composables/useI18n';
import { solvePoW } from '@/lib/pow';
import { pluginRefusal, userPanels } from '@/plugins/registry';
import type {
  ChallengeProof, UserEntry, UserEntryView, UserFormControl, UserFormValues, UserRecord,
} from '@/plugins/types';
import PluginUserControls from './PluginUserControls.vue';

const props = defineProps<{ slug: string; entry?: string }>();

const router = useRouter();

// Looked up rather than fixed at setup: the plugins load after the first
// paint, and a reader who opened /x/<slug> from a bookmark gets here before
// the plugin that owns it has said anything.
const spec = computed(() => userPanels().find((panel) => panel.slug === props.slug) ?? null);

const values = ref<UserFormValues>({});
const busy = ref(false);
const stage = ref('');
const error = ref('');
const sent = ref('');
const mine = ref<UserRecord[]>([]);
const loaded = ref(false);
const viewing = ref('');

const entries = ref<UserEntry[]>([]);
const view = ref<UserEntryView | null>(null);
const turnstile = ref<InstanceType<typeof OaTurnstile> | null>(null);

/** The entry open now: the route says which, the plugin says what it is. */
const opened = computed(() => (spec.value?.entries && props.entry ? props.entry : ''));
const controls = computed<readonly UserFormControl[]>(() =>
  (opened.value ? view.value?.controls : spec.value?.controls) ?? []);

const title = computed(() => {
  if (!spec.value) return t('loading');
  if (opened.value) return view.value?.title ?? t('loading');
  return spec.value.title();
});
const confirmLabel = computed(() =>
  (opened.value ? view.value?.submit?.label : spec.value?.submit?.label()) ?? '');

function reset(): void {
  const next: UserFormValues = {};
  const defaults = (opened.value ? view.value?.defaults : spec.value?.defaults) ?? {};
  for (const control of controls.value) {
    const fallback = defaults[control.key];
    if (control.kind === 'switch') next[control.key] = fallback === true;
    else if (control.kind === 'images' || control.kind === 'checks') next[control.key] = [];
    else if (control.kind === 'choice') {
      next[control.key] = typeof fallback === 'string' ? fallback : control.required ? '' : control.options[0]?.value ?? '';
    } else next[control.key] = typeof fallback === 'string' ? fallback : '';
  }
  values.value = next;
}

function set(key: string, value: string | boolean | string[]): void {
  values.value = { ...values.value, [key]: value };
  sent.value = '';
  error.value = '';
}

function say(failure: unknown): string {
  if (!(failure instanceof ApiError)) return t('failed');
  if (failure.code === 'challenge_failed' || failure.code.startsWith('pow_')) return t('challengeFailed');
  if (failure.code === 'challenge_unavailable') return t('challengeUnavailable');
  return pluginRefusal(failure.code) ?? failure.message;
}

/** The first required control left empty, by its label; empty when none is. */
function missing(): string {
  for (const control of controls.value) {
    const value = values.value[control.key];
    const empty = Array.isArray(value) ? value.length === 0 : typeof value === 'string' ? !value.trim() : false;
    if ('required' in control && control.required && empty) return control.label();
  }
  return '';
}

async function refresh(): Promise<void> {
  const panel = spec.value;
  if (!panel) return;
  try {
    if (panel.entries && !opened.value) entries.value = await panel.entries.load();
    if (panel.mine && !opened.value) mine.value = await panel.mine.load();
  } catch (failure) {
    error.value = say(failure);
  } finally {
    loaded.value = true;
  }
}

async function open(): Promise<void> {
  const panel = spec.value;
  if (!panel?.entries || !opened.value) return;
  try {
    view.value = await panel.entries.open(opened.value);
    reset();
  } catch (failure) {
    error.value = say(failure);
  }
}

/** The proof of whatever check the entry asks for, solved here. */
async function prove(): Promise<ChallengeProof | null> {
  const challenge = view.value?.challenge;
  const proof: ChallengeProof = {};
  if (!challenge) return proof;
  if (challenge.turnstile_site_key) {
    proof.turnstile = turnstile.value?.token() ?? '';
    if (!proof.turnstile) {
      error.value = t('challengeRequired');
      return null;
    }
  }
  if (challenge.pow) {
    stage.value = t('powSolving');
    proof.pow = await solvePoW(await fetchPoWChallenge()).promise;
  }
  return proof;
}

async function submit(): Promise<void> {
  const panel = spec.value;
  if (!panel || busy.value) return;
  const field = missing();
  if (field) {
    error.value = t('pluginFieldRequired', { field });
    return;
  }
  busy.value = true;
  error.value = '';
  try {
    if (opened.value) {
      const submitter = view.value?.submit;
      if (!submitter) return;
      const proof = await prove();
      if (!proof) return;
      sent.value = await submitter.run(values.value, proof);
      await open();
    } else if (panel.submit) {
      sent.value = await panel.submit.run(values.value);
      reset();
      await refresh();
    }
  } catch (failure) {
    error.value = say(failure);
  } finally {
    stage.value = '';
    busy.value = false;
    // Spent whether or not it passed.
    turnstile.value?.reset();
  }
}

function close(): void {
  void router.push(opened.value ? `/x/${props.slug}` : '/');
}

// The same component is reused when one plugin panel is opened from another,
// and when an entry is opened from the list or the list from an entry.
watch([spec, opened], ([panel], [before, wasOpen]) => {
  if (!panel || (panel === before && opened.value === wasOpen)) return;
  error.value = '';
  sent.value = '';
  view.value = null;
  if (opened.value) {
    void open();
    return;
  }
  reset();
  entries.value = [];
  mine.value = [];
  loaded.value = false;
  void refresh();
});

onMounted(() => {
  reset();
  if (opened.value) void open();
  else void refresh();
});
</script>

<template>
  <OaPanel
    :title="title"
    :confirm-label="confirmLabel"
    :confirmable="!!confirmLabel"
    :footer="!!confirmLabel"
    :back="!!opened"
    :width="480"
    :busy="busy"
    :error="error"
    @close="close"
    @back="close"
    @confirm="submit"
  >
    <p v-if="!spec" class="oa-menu-empty">{{ t('pluginPanelMissing') }}</p>

    <template v-else-if="opened">
      <p v-if="!view" class="oa-menu-empty">{{ t('loading') }}</p>
      <template v-else>
        <div v-if="view.badges?.length" class="oa-feedback-detail-badges">
          <OaBadge v-for="(badge, index) in view.badges" :key="index" :tone="badge.tone">{{ badge.label }}</OaBadge>
        </div>
        <p v-if="view.body" class="oa-plugin-entry-body">{{ view.body }}</p>
        <dl v-if="view.facts?.length" class="oa-plugin-record">
          <template v-for="(fact, index) in view.facts" :key="index">
            <dt>{{ fact.label }}</dt>
            <dd class="multiline">{{ fact.value }}</dd>
          </template>
        </dl>
        <p v-if="view.outcome" class="oa-plugin-outcome" :class="`tone-${view.outcome.tone}`" role="status">
          {{ view.outcome.text }}
        </p>
        <template v-if="view.submit">
          <PluginUserControls
            :controls="controls"
            :values="values"
            @set="set"
            @error="error = $event"
            @view="viewing = $event"
          />
          <OaTurnstile
            v-if="view.challenge?.turnstile_site_key"
            ref="turnstile"
            :site-key="view.challenge.turnstile_site_key"
          />
          <p v-if="stage" class="oa-field-hint" role="status">{{ stage }}</p>
        </template>
        <!-- Opened again after a submission, an entry usually says what came
             of it itself; the plugin's sentence is for when it does not. -->
        <p v-if="sent && !view.outcome" class="oa-feedback-sent" role="status">{{ sent }}</p>
      </template>
    </template>

    <template v-else>
      <p v-if="spec.intro" class="oa-field-hint">{{ spec.intro() }}</p>

      <PluginUserControls
        :controls="controls"
        :values="values"
        @set="set"
        @error="error = $event"
        @view="viewing = $event"
      />

      <p v-if="sent" class="oa-feedback-sent" role="status">{{ sent }}</p>

      <template v-if="spec.entries">
        <p v-if="!loaded" class="oa-menu-empty">{{ t('loading') }}</p>
        <p v-else-if="!entries.length" class="oa-menu-empty">{{ spec.entries.empty() }}</p>
        <ul v-else class="oa-feedback-list">
          <li v-for="item in entries" :key="item.id">
            <button type="button" class="oa-feedback-item" @click="router.push(`/x/${slug}/${item.id}`)">
              <span class="oa-feedback-item-head">
                <span class="oa-feedback-item-title">{{ item.title }}</span>
                <OaBadge v-if="item.badge" :tone="item.badge.tone">{{ item.badge.label }}</OaBadge>
              </span>
              <span v-if="item.meta?.length" class="oa-feedback-item-meta">
                <span v-for="(fact, index) in item.meta" :key="index">{{ fact }}</span>
              </span>
              <span v-if="item.note" class="oa-field-hint">{{ item.note }}</span>
            </button>
          </li>
        </ul>
      </template>

      <section v-if="spec.mine" class="oa-feedback-mine">
        <h3 class="oa-panel-section-title">{{ spec.mine.title() }}</h3>
        <p v-if="!loaded" class="oa-menu-empty">{{ t('loading') }}</p>
        <p v-else-if="!mine.length" class="oa-menu-empty">{{ spec.mine.empty() }}</p>
        <ul v-else class="oa-feedback-list">
          <li v-for="record in mine" :key="record.id">
            <div class="oa-feedback-item">
              <span class="oa-feedback-item-head">
                <span class="oa-feedback-item-title">{{ record.title }}</span>
                <OaBadge v-if="record.badge" :tone="record.badge.tone">{{ record.badge.label }}</OaBadge>
              </span>
              <span v-if="record.meta?.length" class="oa-feedback-item-meta">
                <span v-for="(fact, index) in record.meta" :key="index">{{ fact }}</span>
              </span>
              <span v-if="record.note" class="oa-field-hint">{{ record.note }}</span>
            </div>
          </li>
        </ul>
      </section>
    </template>
  </OaPanel>

  <OaImageLightbox v-if="viewing" :src="viewing" @close="viewing = ''" />
</template>
