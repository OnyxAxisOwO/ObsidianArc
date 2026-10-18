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
import { fetchPoWChallenge } from '@/api/auth';
import { ApiError } from '@/api/client';
import OaBadge from '@/components/OaBadge.vue';
import OaImageLightbox from '@/components/OaImageLightbox.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { solvePoW } from '@/lib/pow';
import { pluginRefusal, pluginsSettled, userPanels } from '@/plugins/registry';
import type {
  ChallengeProof, UserEntry, UserEntryView, UserFormControl, UserFormValues, UserRecord,
} from '@/plugins/types';
import PluginUserControls from './PluginUserControls.vue';

const props = defineProps<{ slug: string; entry?: string }>();

const panels = usePanelExit();

// Looked up in a computed, not once at setup, so it follows the plugin registry
// rather than a snapshot of it. Boot waits for the plugins before the first
// paint, so a bookmarked /x/<slug> normally finds its panel on the first run.
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
  // The title is the one line a missing panel shows: "Loading" is for a panel
  // that may still arrive, not for one no loaded plugin offers.
  if (!spec.value) return pluginsSettled() ? t('pluginPanelMissing') : t('loading');
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
    // One empty row to type into: a list with nothing drawn is a button
    // somebody has to find before they can start.
    else if (control.kind === 'list') next[control.key] = [''];
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
    const empty = Array.isArray(value) ? !value.some((item) => item.trim()) : typeof value === 'string' ? !value.trim() : false;
    if ('required' in control && control.required && empty) return control.label();
  }
  return '';
}

/** What the plugin is handed: a list's rows trimmed, without the blank ones left to type into. */
function sendable(): UserFormValues {
  const out: UserFormValues = { ...values.value };
  for (const control of controls.value) {
    const value = out[control.key];
    if (control.kind === 'list' && Array.isArray(value)) {
      out[control.key] = value.map((item) => item.trim()).filter(Boolean);
    }
  }
  return out;
}

// The newest request wins. The component is reused between the list and an
// entry, and between plugins, so an answer can arrive for a list or an entry
// the reader has already left, and must not be drawn over the one shown now.
let ticket = 0;

async function refresh(): Promise<void> {
  const panel = spec.value;
  if (!panel) return;
  const request = ++ticket;
  try {
    if (panel.entries && !opened.value) {
      const next = await panel.entries.load();
      if (request !== ticket) return;
      entries.value = next;
    }
    if (panel.mine && !opened.value) {
      const next = await panel.mine.load();
      if (request !== ticket) return;
      mine.value = next;
    }
  } catch (failure) {
    if (request === ticket) error.value = say(failure);
  } finally {
    if (request === ticket) loaded.value = true;
  }
}

async function open(): Promise<void> {
  const panel = spec.value;
  const entry = opened.value;
  if (!panel?.entries || !entry) return;
  const request = ++ticket;
  try {
    const next = await panel.entries.open(entry);
    if (request !== ticket || opened.value !== entry) return;
    view.value = next;
    reset();
  } catch (failure) {
    if (request === ticket) error.value = say(failure);
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
  // The reader can leave this entry while the proof is solved or the request
  // is in flight. The form is read now, before the first await, because the
  // entry being opened next refills it; the answer and any error then belong
  // to the entry the button was pressed on.
  const entry = opened.value;
  try {
    if (opened.value) {
      const submitter = view.value?.submit;
      if (!submitter) return;
      const payload = sendable();
      const proof = await prove();
      if (!proof) return;
      const result = await submitter.run(payload, proof);
      if (opened.value !== entry) return;
      sent.value = result;
      await open();
    } else if (panel.submit) {
      sent.value = await panel.submit.run(sendable());
      reset();
      await refresh();
    }
  } catch (failure) {
    if (opened.value === entry) error.value = say(failure);
  } finally {
    // Busy, the stage and the widget are the page's, so a send that finishes
    // after the reader has moved on must not touch what the next entry's send
    // is using. The route watch has already released them for the entry left.
    if (opened.value === entry) {
      stage.value = '';
      busy.value = false;
      // Spent whether or not it passed.
      turnstile.value?.reset();
    }
  }
}

// Back from an entry to the list, and from the list out again. Both are a move
// on the same panel's address, so over the backoffice they are the same address
// with the panel's parameter shortened or removed (see usePanelExit).
//
// An entry's X and Escape close its OaPanel before this runs, and a closed
// OaPanel stays closed. The template keys the column by address so that the
// list which follows is a panel of its own, not that hidden one.
function close(): void {
  if (opened.value) panels.open(`/x/${props.slug}`);
  else panels.close('push');
}

// The same component is reused when one plugin panel is opened from another,
// and when an entry is opened from the list or the list from an entry.
watch([spec, opened], ([panel], [before, wasOpen]) => {
  if (panel === before && opened.value === wasOpen) return;
  // Whatever is still in flight was asked for the list or entry being left.
  ticket += 1;
  // A send still out belongs to the entry being left, and its finally no longer
  // clears these once the reader has moved on, so they are released here.
  busy.value = false;
  stage.value = '';
  if (!panel) return;
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
    :key="opened ? `entry:${opened}` : 'list'"
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
    <!-- The title carries the missing-panel message, so the body has nothing to show. -->
    <template v-if="!spec"></template>

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
            <!-- Encoded, so an id with a slash or a question mark names its own entry. -->
            <button type="button" class="oa-feedback-item" @click="panels.open(`/x/${encodeURIComponent(slug)}/${encodeURIComponent(item.id)}`)">
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
