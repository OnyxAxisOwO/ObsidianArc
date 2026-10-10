<script setup lang="ts">
// Where somebody tells the operator that something is broken, or that it
// could be better — and where they read what came back.
//
// Two screens in one column, the way the keys panel edits a row: the form,
// and the conversation on one report. A second panel beside the first would
// say "you have left the place you were", which is not what opening your own
// report is.
//
// The form is two questions with two or three answers each, a title, and a
// box big enough that a real description does not feel unwelcome. The
// segmented controls are there rather than selects because every option fits
// on screen — a menu that has to be opened to show two choices is a menu that
// exists to hide one of them.
//
// Everything anybody writes here is Markdown, drawn by the transcript's own
// renderer: it builds nodes and never assembles an HTML string, so an answer
// from an operator is safe to draw in a reader's page for the same reason a
// model's answer is.

import { computed, onMounted, ref } from 'vue';
import { ApiError } from '@/api/client';
import {
  fetchThread, listFeedback, replyToFeedback, sendFeedback,
  type Feedback, type FeedbackKind, type FeedbackPriority, type FeedbackThread,
} from '@/api/feedback';
import OaBadge from '@/components/OaBadge.vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaOverlay from '@/components/OaOverlay.vue';
import OaPanel from '@/components/OaPanel.vue';
import OaTextArea from '@/components/OaTextArea.vue';
import OaTextField from '@/components/OaTextField.vue';
import OaTurnstile from '@/components/OaTurnstile.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { IconClose, IconLock } from '@/icons';
import { absoluteTime, relativeTime } from '@/lib/format';
import { refreshFeedbackUnread } from '@/stores/feedback';
import { siteInfo } from '@/stores/session';

const KINDS: Array<{ value: FeedbackKind; label: StringKey }> = [
  { value: 'bug', label: 'feedbackKindBug' },
  { value: 'idea', label: 'feedbackKindIdea' },
];

const PRIORITIES: Array<{ value: FeedbackPriority; label: StringKey }> = [
  { value: 'low', label: 'feedbackPriorityLow' },
  { value: 'medium', label: 'feedbackPriorityMedium' },
  { value: 'high', label: 'feedbackPriorityHigh' },
];

const panels = usePanelExit();

const kind = ref<FeedbackKind>('bug');
const priority = ref<FeedbackPriority>('medium');
const title = ref('');
const body = ref('');

const mine = ref<Feedback[]>([]);
const remaining = ref(0);
const loaded = ref(false);
const busy = ref(false);
const error = ref('');
const sent = ref(false);

/** The report being read, or null while the form is on screen. */
const thread = ref<FeedbackThread | null>(null);
const threadBusy = ref(false);
const answer = ref('');

const titleField = ref<InstanceType<typeof OaTextField> | null>(null);
const guard = ref<InstanceType<typeof OaTurnstile> | null>(null);

/**
 * The challenge, when there is one, is a sheet rather than a row in the form.
 *
 * Cloudflare's widget is a fixed 300px box with its own chrome, and a panel
 * this narrow — resizable down to 320px — squeezed it against the sides. The
 * redemption dialog had already answered this: the check gets a surface of
 * its own, and solving it sends what the reader had already written.
 *
 * It guards both writes, not just the first one. A token is good for one
 * submission, so every report and every reply asks again — which is the point:
 * the cap is ten reports a day but fifty turns a thread, so replying is the
 * cheaper thing to automate and the one a gate on reports alone would miss.
 */
const challengeOpen = ref(false);
const challengeError = ref('');
/** Which write the open sheet is holding. */
const challengeFor = ref<'report' | 'reply'>('report');

/** Whether this instance asks for a check at all. */
const challenged = computed(() => !!siteInfo.value.turnstile_on_feedback);

const full = computed(() => loaded.value && remaining.value <= 0);

async function refresh(): Promise<void> {
  try {
    const result = await listFeedback();
    mine.value = result.feedback;
    remaining.value = result.remaining;
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    loaded.value = true;
  }
}

function send(): void {
  if (busy.value) return;
  const heading = title.value.trim();
  if (!heading) {
    error.value = t('feedbackNeedTitle');
    titleField.value?.focus();
    return;
  }
  if (!body.value.trim()) {
    error.value = t('feedbackNeedBody');
    return;
  }

  error.value = '';
  if (challenged.value) {
    ask('report');
    return;
  }
  void submit('');
}

/** Raises the sheet for one of the two writes. */
function ask(what: 'report' | 'reply'): void {
  challengeFor.value = what;
  challengeError.value = '';
  challengeOpen.value = true;
}

async function submit(token: string): Promise<void> {
  busy.value = true;
  try {
    await sendFeedback({
      kind: kind.value,
      priority: priority.value,
      title: title.value.trim(),
      body: body.value.trim(),
      ...(token ? { turnstile: token } : {}),
    });
    title.value = '';
    body.value = '';
    priority.value = 'medium';
    challengeOpen.value = false;
    sent.value = true;
    // It stays until the next thing is typed, rather than for a couple of
    // seconds: somebody who has just pressed Send and looked away should
    // still find the answer when they look back.
    await refresh();
  } catch (failure) {
    const code = failure instanceof ApiError ? failure.code : '';
    // A refused check is answered inside the sheet, with the widget reset —
    // a token is spent whether or not it passed — so the reader can try again
    // without losing what they wrote.
    if (token && (code === 'challenge_failed' || code === 'challenge_unavailable')) {
      challengeError.value = code === 'challenge_failed'
        ? t('challengeFailed') : t('challengeUnavailable');
      guard.value?.reset();
    } else {
      challengeOpen.value = false;
      error.value = failure instanceof ApiError ? failure.message : t('failed');
    }
  } finally {
    busy.value = false;
  }
}

async function solved(): Promise<void> {
  const token = guard.value?.token() ?? '';
  if (!token || busy.value || threadBusy.value) return;
  challengeError.value = '';
  if (challengeFor.value === 'reply') {
    await submitReply(token);
    return;
  }
  await submit(token);
}

function cancelChallenge(): void {
  challengeOpen.value = false;
  challengeError.value = '';
}

function touched(): void {
  sent.value = false;
}

// --- the conversation on one report ------------------------------------------

/**
 * Opening a report is also what marks its answer read — on the server, in the
 * row behind this panel, and in the dot the account menu draws from, so the
 * three cannot disagree about whether this reader has seen it.
 */
async function open(record: Feedback): Promise<void> {
  error.value = '';
  threadBusy.value = true;
  answer.value = '';
  try {
    thread.value = await fetchThread(record.id);
    const row = mine.value.find((item) => item.id === record.id);
    if (row) row.author_unread = false;
    void refreshFeedbackUnread();
  } catch (failure) {
    error.value = failure instanceof ApiError ? failure.message : t('failed');
  } finally {
    threadBusy.value = false;
  }
}

function back(): void {
  thread.value = null;
  error.value = '';
}

function reply(): void {
  const text = answer.value.trim();
  if (!thread.value || !text || threadBusy.value) return;
  error.value = '';
  if (challenged.value) {
    ask('reply');
    return;
  }
  void submitReply('');
}

async function submitReply(token: string): Promise<void> {
  const current = thread.value;
  const text = answer.value.trim();
  if (!current || !text) return;
  threadBusy.value = true;
  try {
    const record = await replyToFeedback(current.feedback.id, text, token);
    current.replies.push(record);
    current.feedback.replies = current.replies.length;
    answer.value = '';
    challengeOpen.value = false;
    // The list behind the thread carries the count and the status badge.
    await refresh();
  } catch (failure) {
    const code = failure instanceof ApiError ? failure.code : '';
    // Answered inside the sheet, with the widget reset, so the reader can try
    // again without losing what they wrote — the same bargain the form makes.
    if (token && (code === 'challenge_failed' || code === 'challenge_unavailable')) {
      challengeError.value = code === 'challenge_failed'
        ? t('challengeFailed') : t('challengeUnavailable');
      guard.value?.reset();
    } else {
      challengeOpen.value = false;
      error.value = failure instanceof ApiError ? failure.message : t('failed');
    }
  } finally {
    threadBusy.value = false;
  }
}

function kindLabel(record: Feedback): string {
  return t(record.kind === 'bug' ? 'feedbackKindBug' : 'feedbackKindIdea');
}

function priorityLabel(value: FeedbackPriority): string {
  return t(PRIORITIES.find((entry) => entry.value === value)!.label);
}

/**
 * Who said it.
 *
 * The operator's name is shown when the server sent one — whether it does is
 * an instance setting, and when it is off the name never leaves the server,
 * so there is nothing here to decide. An answer signed by a person reads as
 * one; an unsigned answer still reads as staff, which is the part that
 * matters.
 */
function turnWho(turn: { from_staff: boolean; username?: string; nickname?: string }): string {
  if (!turn.from_staff) return t('feedbackYou');
  const name = turn.nickname || turn.username;
  return name ? `${t('feedbackFromStaff')} · ${name}` : t('feedbackFromStaff');
}

function statusLabel(record: Feedback): string {
  return t(record.status === 'resolved' ? 'feedbackStatusResolved' : 'feedbackStatusOpen');
}

onMounted(() => void refresh());
</script>

<template>
  <!-- One column, two screens. The footer belongs to the form; the thread
       saves itself, so it has none. -->
  <OaPanel
    :title="thread ? thread.feedback.title : t('feedback')"
    :back="!!thread"
    :footer="!thread"
    :confirm-label="t('feedbackSend')"
    :confirmable="!full"
    :width="480"
    :busy="busy"
    :error="error"
    @close="panels.close('push')"
    @back="back"
    @confirm="send"
  >
    <template v-if="thread">
      <div class="oa-feedback-detail-badges">
        <OaBadge tone="muted">{{ kindLabel(thread.feedback) }}</OaBadge>
        <OaBadge :tone="thread.feedback.priority === 'high' ? 'warning' : 'muted'">
          {{ priorityLabel(thread.feedback.priority) }}
        </OaBadge>
        <OaBadge :tone="thread.feedback.status === 'resolved' ? 'muted' : 'default'">
          {{ statusLabel(thread.feedback) }}
        </OaBadge>
      </div>

      <div class="oa-thread">
        <!-- The report itself is the first thing said, so it is drawn as the
             first turn rather than as a header above the conversation. -->
        <article class="oa-thread-turn mine">
          <header class="oa-thread-who">
            <span>{{ t('feedbackYou') }}</span>
            <time :title="absoluteTime(thread.feedback.created_at)">
              {{ relativeTime(thread.feedback.created_at) }}
            </time>
          </header>
          <OaMarkdown class="ai-answer oa-thread-body" :text="thread.feedback.body" />
        </article>

        <article
          v-for="turn in thread.replies"
          :key="turn.id"
          class="oa-thread-turn"
          :class="turn.from_staff ? 'staff' : 'mine'"
        >
          <header class="oa-thread-who">
            <span>{{ turnWho(turn) }}</span>
            <time :title="absoluteTime(turn.created_at)">{{ relativeTime(turn.created_at) }}</time>
          </header>
          <OaMarkdown class="ai-answer oa-thread-body" :text="turn.body" />
        </article>
      </div>

      <OaTextArea
        v-model="answer"
        class="oa-feedback-answer"
        :label="t('feedbackReply')"
        :placeholder="t('feedbackReplyPlaceholder')"
        :hint="t('feedbackMarkdownHint')"
        :rows="5"
      />
      <button
        type="button"
        class="oa-btn primary"
        :disabled="threadBusy || !answer.trim()"
        @click="reply"
      >{{ threadBusy ? '…' : t('feedbackReplySend') }}</button>
    </template>

    <template v-else>
      <p class="oa-field-hint">{{ t('feedbackIntro') }}</p>

      <div class="oa-field">
        <span class="oa-field-label">{{ t('feedbackKind') }}</span>
        <div class="oa-segmented">
          <button
            v-for="entry in KINDS"
            :key="entry.value"
            type="button"
            class="oa-segmented-option"
            :class="{ active: kind === entry.value }"
            @click="kind = entry.value; touched()"
          >{{ t(entry.label) }}</button>
        </div>
      </div>

      <div class="oa-field">
        <span class="oa-field-label">{{ t('feedbackPriority') }}</span>
        <div class="oa-segmented">
          <button
            v-for="entry in PRIORITIES"
            :key="entry.value"
            type="button"
            class="oa-segmented-option"
            :class="{ active: priority === entry.value }"
            @click="priority = entry.value; touched()"
          >{{ t(entry.label) }}</button>
        </div>
      </div>

      <OaTextField
        ref="titleField"
        v-model="title"
        :label="t('feedbackTitleLabel')"
        :placeholder="t('feedbackTitlePlaceholder')"
        :max-length="120"
        @update:model-value="touched"
      />

      <!-- Deliberately tall. A three-line box tells somebody to be brief, and
           the thing that makes a report worth having is the part they would
           have left out. -->
      <OaTextArea
        v-model="body"
        class="oa-feedback-body"
        :label="t('feedbackBodyLabel')"
        :placeholder="t('feedbackBodyPlaceholder')"
        :hint="t('feedbackMarkdownHint')"
        :rows="12"
        @update:model-value="touched"
      />

      <p v-if="sent" class="oa-feedback-sent">{{ t('feedbackSent') }}</p>
      <p v-else-if="full" class="oa-feedback-full">{{ t('feedbackNoneLeft') }}</p>
      <p v-else-if="loaded" class="oa-field-hint">{{ t('feedbackRemaining', { count: remaining }) }}</p>

      <section class="oa-feedback-mine">
        <h3 class="oa-panel-section-title">{{ t('feedbackMine') }}</h3>
        <p v-if="loaded && mine.length" class="oa-field-hint">{{ t('feedbackMineHint') }}</p>
        <p v-if="!loaded" class="oa-menu-empty">{{ t('loading') }}</p>
        <p v-else-if="!mine.length" class="oa-menu-empty">{{ t('feedbackMineEmpty') }}</p>
        <ul v-else class="oa-feedback-list">
          <li v-for="record in mine" :key="record.id">
            <!-- A button, because every one of them opens the conversation. -->
            <button type="button" class="oa-feedback-item" @click="open(record)">
              <span class="oa-feedback-item-head">
                <span v-if="record.author_unread" class="oa-menu-unread" :title="t('feedbackHasReply')" />
                <span class="oa-feedback-item-title">{{ record.title }}</span>
                <OaBadge :tone="record.status === 'resolved' ? 'muted' : 'default'">
                  {{ statusLabel(record) }}
                </OaBadge>
              </span>
              <span class="oa-feedback-item-meta">
                <span>{{ kindLabel(record) }}</span>
                <span>{{ priorityLabel(record.priority) }}</span>
                <span v-if="record.replies">{{ t('feedbackReplyCount', { count: record.replies }) }}</span>
                <span>{{ relativeTime(record.created_at) }}</span>
              </span>
            </button>
          </li>
        </ul>
      </section>
    </template>
  </OaPanel>

  <!-- Where the operator asked for one. The daily cap already holds one
       account to ten a day; this is what keeps a script holding somebody's
       cookie from spending those ten without a person present. -->
  <OaOverlay
    v-if="challengeOpen"
    v-slot="{ close }"
    overlay-class="oa-modal-overlay"
    :dismissible="!busy && !threadBusy"
    @close="cancelChallenge"
  >
    <div class="oa-auth-card oa-modal-card">
      <OaIconButton class="oa-icon-btn oa-modal-close" :label="t('close')" @click="close">
        <IconClose :size="16" />
      </OaIconButton>

      <div class="oa-auth-brand">
        <span class="oa-auth-mark"><IconLock :size="15" /></span>
        <span>{{ siteInfo.name }}</span>
      </div>
      <h1 class="oa-auth-title">
        {{ challengeFor === 'reply' ? t('feedbackReplyChallengeTitle') : t('feedbackChallengeTitle') }}
      </h1>
      <p class="oa-auth-sub">
        {{ challengeFor === 'reply' ? t('feedbackReplyChallengeBody') : t('feedbackChallengeBody') }}
      </p>

      <div class="oa-auth-form">
        <OaTurnstile
          ref="guard"
          :site-key="siteInfo.turnstile_site_key ?? ''"
          @solved="solved"
        />
        <p v-if="challengeError" class="oa-auth-error" role="alert">{{ challengeError }}</p>
      </div>
    </div>
  </OaOverlay>
</template>
