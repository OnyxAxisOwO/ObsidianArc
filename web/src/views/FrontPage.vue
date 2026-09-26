<script setup lang="ts">
// The product's own front page.
//
// The other two front doors are the operator's: `intro` is HTML they wrote,
// `chat` is the product itself with a trial bolted on. This one is ours — it
// exists so that an instance can have a front page without anybody composing
// one, and so what it says is about the software rather than about any
// particular deployment.
//
// It is the one screen in this project that is allowed to be loud, and the
// loudness is still made of the same parts as everything else: every colour
// comes from an `--ai-*` token, so the page recolours with the accent the
// reader picked, and every card is the same 18px surface the chat is built
// from. What it adds is depth and motion, which is what a front page is for.
//
// The motion is deliberately cheap. Reveals are one IntersectionObserver
// over the whole page rather than one per element; the aurora, the grid, the
// motes and the connectors are CSS keyframes on `transform` and `opacity`
// only, so they composite rather than repaint; the only per-frame JavaScript
// is the counters, and they stop when they land. The hero's background, which
// never stops, is paused by a second observer once it is scrolled away.
// Everything here is off under `prefers-reduced-motion: reduce` — this page
// moves more than any other, so it has more to switch off.
//
// What it cannot do is make the default accent colourful. The default is a
// near-black, so the aurora is smoke on a near-white page until a reader picks
// a colour — and that is kept deliberately: a hue hardcoded here would be the
// one surface in the product that ignored the accent (AGENTS.md).

import { onMounted, onBeforeUnmount, ref, computed } from 'vue';
import { useRouter } from 'vue-router';
import { useEventListener, usePreferredReducedMotion } from '@vueuse/core';
import HomeNotice from '@/announce/HomeNotice.vue';
import OaThemeToggle from '@/components/OaThemeToggle.vue';
import { t } from '@/composables/useI18n';
import {
  IconArrowUpRight, IconChart, IconChevronRight, IconCpu, IconGithub, IconKey,
  IconLayers, IconMessage, IconServer, IconShield, IconSpark, IconTerminal,
} from '@/icons';
import { siteInfo } from '@/stores/session';

const DOCS_URL = 'https://obsidianarc.pages.dev';
const SOURCE_URL = 'https://github.com/OnyxAxisOwO/ObsidianArc';

const router = useRouter();
const site = computed(() => siteInfo.value);
const reducedMotion = usePreferredReducedMotion();
const still = computed(() => reducedMotion.value === 'reduce');

// The page scrolls inside itself rather than in the document: it is mounted
// in `.oa-app-viewport`, which is `overflow: hidden` so that the chat's own
// columns can own their scrolling. Everything below that needs a scroll
// position — the progress bar, the reveals, the anchors — therefore reads
// this element and not the window.
const page = ref<HTMLElement | null>(null);
const progress = ref(0);
const scrolled = ref(false);

/** Protocol and product names, which stay English on purpose (AGENTS.md). */
const SPEAKS = [
  'OpenAI Chat Completions', 'Anthropic Messages', 'OpenAI Responses',
  'Images', 'Server-Sent Events', 'SQLite', 'PostgreSQL', 'Docker',
] as const;

const FEATURES = [
  { icon: IconMessage, title: 'frontFeatureChat', body: 'frontFeatureChatBody' },
  { icon: IconKey, title: 'frontFeatureGateway', body: 'frontFeatureGatewayBody' },
  { icon: IconLayers, title: 'frontFeatureRouting', body: 'frontFeatureRoutingBody' },
  { icon: IconChart, title: 'frontFeatureQuota', body: 'frontFeatureQuotaBody' },
  { icon: IconTerminal, title: 'frontFeatureConsole', body: 'frontFeatureConsoleBody' },
  { icon: IconShield, title: 'frontFeatureSecurity', body: 'frontFeatureSecurityBody' },
] as const;

// Counted rather than written out, because a number that animates to itself
// is the one piece of decoration on this page that is also the message: the
// whole claim is that these four numbers are small.
const FACTS = [
  { value: 1, label: 'frontFactBinary', note: 'frontFactBinaryNote' },
  { value: 0, label: 'frontFactServices', note: 'frontFactServicesNote' },
  { value: 4, label: 'frontFactProtocols', note: 'frontFactProtocolsNote' },
  { value: 2, label: 'frontFactDatabases', note: 'frontFactDatabasesNote' },
] as const;

const counted = ref<number[]>(FACTS.map(() => 0));

// A real request against a real endpoint, with the placeholder names the API
// reference uses (`your-model`, `$OBSIDIAN_API_KEY`) so that copying this and
// copying the docs land in the same place.
const CURL = `curl https://arc.example.com/v1/chat/completions \\
  -H "Authorization: Bearer $OBSIDIAN_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "your-model",
    "stream": true,
    "messages": [{"role": "user", "content": "Hello"}]
  }'`;

// Real commands, rendered the way the terminal actually renders them:
// `model list` is a table with lowercase headers (`rt.Table`), and the other
// two are aligned `key: value` field lists (`rt.Fields`), which is why they
// do not look like tables. A mock that invented a command, a column or a
// format would be the first thing an operator discovers is a lie — so these
// were read off `internal/console` rather than imagined.
const SESSION = [
  {
    command: 'model list',
    output: [
      'id     display_name  model_id           provider   enabled',
      '01JAX… Sonnet        claude-sonnet-4-5  Anthropic  yes',
      '01JB2… GPT           gpt-5              OpenAI     yes',
      '01JB7… Local         qwen3:8b           Ollama     yes',
    ],
  },
  {
    command: 'usage summary',
    output: [
      'requests:     1904',
      'total_tokens: 6220115',
      'credits:      58.40',
      'current_rpm:  12',
    ],
  },
  {
    command: 'key create --name deploy-bot',
    output: [
      'id:        01JBQ…',
      'prefix:    sk-oa-7f3a',
      'name:      deploy-bot',
      '',
      'token (save it now — this is the only time it is shown in full): sk-…',
    ],
  },
] as const;

const step = ref(0);
const typed = ref('');
const typing = ref(true);
const current = computed(() => SESSION[step.value] ?? SESSION[0]!);
// The output belongs to the command that finished typing, so it appears only
// once the line is whole. Typing `model list` above a usage table for half a
// second reads as the terminal answering the wrong question.
const output = computed(() => (typing.value ? [] : current.value.output));

let timer = 0;
let frame = 0;
// Checked by every callback that can outlive the component. A timeout or a
// frame scheduled just before teardown otherwise writes to refs whose setup
// state has gone — the teardown-ordering trap AGENTS.md describes.
let alive = true;

/** Types the current command, holds the answer, then moves to the next. */
function tick(): void {
  if (!alive) return;
  const command = current.value.command;
  if (typed.value.length < command.length) {
    typed.value = command.slice(0, typed.value.length + 1);
    timer = window.setTimeout(tick, 55);
    return;
  }
  if (typing.value) {
    typing.value = false;
    timer = window.setTimeout(tick, 2600);
    return;
  }
  step.value = (step.value + 1) % SESSION.length;
  typed.value = '';
  typing.value = true;
  timer = window.setTimeout(tick, 420);
}

/** Runs the counters up once, when the facts come into view. */
function countUp(): void {
  if (still.value) {
    counted.value = FACTS.map((fact) => fact.value);
    return;
  }
  const started = performance.now();
  const run = (now: number): void => {
    if (!alive) return;
    // 1 - (1-x)^3: fast first, settling rather than stopping. The same shape
    // as the 0.16/1/0.3/1 curve the rest of the interface eases with.
    const progressed = Math.min(1, (now - started) / 900);
    const eased = 1 - Math.pow(1 - progressed, 3);
    counted.value = FACTS.map((fact) => Math.round(fact.value * eased));
    if (progressed < 1) frame = requestAnimationFrame(run);
  };
  frame = requestAnimationFrame(run);
}

function onScroll(): void {
  const node = page.value;
  if (!node) return;
  const travel = node.scrollHeight - node.clientHeight;
  progress.value = travel > 0 ? Math.min(1, node.scrollTop / travel) : 0;
  scrolled.value = node.scrollTop > 24;
}

function jump(id: string): void {
  // `block: 'start'` against the scroller's `scroll-padding-top`, which is
  // what keeps the section's own heading clear of the sticky header.
  const node = page.value?.querySelector(`#${id}`);
  node?.scrollIntoView({ behavior: still.value ? 'auto' : 'smooth', block: 'start' });
}

/**
 * The pointer's position on the element it moved over, as custom properties.
 *
 * Written to the element rather than held in a ref on purpose: a ref would
 * re-render the component on every pointer move, and nothing in the template
 * depends on where the pointer is — only the gradients and the parallax do,
 * and CSS can read those without Vue's help.
 *
 * Two forms, because two kinds of thing read them. The fractions drive
 * parallax and the cards' spotlight, which are proportional to the box. The
 * pixel pair positions the hero's glow, which has to sit exactly under the
 * pointer whatever the hero's size — a fraction cannot say that without the
 * box's width, and a CSS `translate` cannot read one.
 */
function onPointer(event: PointerEvent): void {
  if (still.value) return;
  const node = event.currentTarget as HTMLElement | null;
  if (!node) return;
  const box = node.getBoundingClientRect();
  // A box with no size — collapsed, or mid-layout — would write `NaN` and
  // `Infinity` into the fractions, and a `calc()` holding either is invalid,
  // which drops the whole declaration and snaps every field to its origin.
  if (!box.width || !box.height) return;
  const x = event.clientX - box.left;
  const y = event.clientY - box.top;
  node.style.setProperty('--oa-front-px', String(x / box.width));
  node.style.setProperty('--oa-front-py', String(y / box.height));
  node.style.setProperty('--oa-front-gx', `${x}px`);
  node.style.setProperty('--oa-front-gy', `${y}px`);
}

let observer: IntersectionObserver | null = null;
let heroObserver: IntersectionObserver | null = null;

onMounted(() => {
  const node = page.value;
  if (!node) return;
  onScroll();

  // Everything on this page that is hidden at rest is hidden by a class an
  // observer removes, so "no observer" and "no motion wanted" have to land in
  // the same place: shown. They are different questions — one is the reader's
  // setting, the other is the browser's capability — and only the answer is
  // shared. Getting this wrong does not degrade the page, it empties it.
  if (still.value || typeof IntersectionObserver === 'undefined') {
    node.querySelectorAll('.oa-front-reveal').forEach((el) => el.classList.add('shown'));
    counted.value = FACTS.map((fact) => fact.value);
    typed.value = SESSION[0]!.command;
    typing.value = false;
    return;
  }

  // One observer for the whole page, rooted on the scroller rather than the
  // viewport — the document never scrolls here, so a viewport-rooted observer
  // would report everything as visible at once and reveal the page in a
  // single frame.
  observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      entry.target.classList.add('shown');
      if (entry.target.classList.contains('oa-front-facts')) countUp();
      // Revealing is a one-way door: a section that faded back out as the
      // reader scrolled past would turn the page into a strobe.
      observer?.unobserve(entry.target);
    }
  }, { root: node, threshold: 0.15, rootMargin: '0px 0px -8% 0px' });

  node.querySelectorAll('.oa-front-reveal').forEach((el) => observer?.observe(el));

  // A second observer, because it answers a different question in the
  // opposite direction. The reveals are a one-way door; the hero's background
  // is a switch that has to turn back on when the reader scrolls up to it.
  // Folding both into one callback would mean the reveal observer could never
  // unobserve what it had revealed.
  const hero = node.querySelector('.oa-front-hero');
  if (hero) {
    heroObserver = new IntersectionObserver(([entry]) => {
      hero.classList.toggle('idle', !entry?.isIntersecting);
    }, { root: node });
    heroObserver.observe(hero);
  }
  tick();
});

onBeforeUnmount(() => {
  alive = false;
  window.clearTimeout(timer);
  cancelAnimationFrame(frame);
  observer?.disconnect();
  observer = null;
  heroObserver?.disconnect();
  heroObserver = null;
});

useEventListener(page, 'scroll', onScroll, { passive: true });
</script>

<template>
  <div ref="page" class="oa-front" :class="{ still }">
    <div class="oa-front-progress" :style="{ transform: `scaleX(${progress})` }" />

    <header class="oa-front-head" :class="{ lifted: scrolled }">
      <a class="oa-landing-brand" href="/" @click.prevent="jump('front-top')">
        <span class="oa-auth-mark"><IconSpark :size="15" /></span>
        <span>{{ site.name }}</span>
      </a>
      <nav class="oa-front-nav">
        <button type="button" @click="jump('front-features')">{{ t('frontFeaturesKicker') }}</button>
        <button type="button" @click="jump('front-flow')">{{ t('frontFlowKicker') }}</button>
        <button type="button" @click="jump('front-showcase')">{{ t('frontShowcaseKicker') }}</button>
      </nav>
      <span class="oa-header-spacer" />
      <OaThemeToggle />
      <button type="button" class="oa-btn" @click="router.push('/login')">{{ t('frontSignIn') }}</button>
      <button
        v-if="site.registration_enabled"
        type="button"
        class="oa-btn primary"
        @click="router.push('/register')"
      >{{ t('frontCreateAccount') }}</button>
    </header>

    <HomeNotice />

    <section id="front-top" class="oa-front-hero" @pointermove="onPointer">
      <!-- Drifting fields, a travelling grid, two layers of motes and a glow
           under the pointer, behind everything. Decoration with no text in
           it, so it is hidden from assistive technology rather than
           described. The pointer handler is on the section rather than here:
           the hero's body covers the middle of this layer, and a handler
           here heard the pointer only at the margins. -->
      <div class="oa-front-aurora" aria-hidden="true">
        <span class="oa-front-blob one" />
        <span class="oa-front-blob two" />
        <span class="oa-front-blob three" />
        <span class="oa-front-grid" />
        <span class="oa-front-motes far" />
        <span class="oa-front-motes near" />
        <span class="oa-front-glow" />
      </div>

      <div class="oa-front-hero-body">
        <span class="oa-front-kicker"><IconSpark :size="13" />{{ t('frontKicker') }}</span>
        <h1 class="oa-front-title">
          <!-- One mask per line rather than per word: the lines are the
               sentence's own beats, and a word-by-word reveal of a headline
               this short reads as a stutter. -->
          <span class="oa-front-line"><span>{{ t('frontHeroLineOne') }}</span></span>
          <span class="oa-front-line"><span>{{ t('frontHeroLineTwo') }}</span></span>
          <span class="oa-front-line accent"><span>{{ t('frontHeroLineThree') }}</span></span>
        </h1>
        <p class="oa-front-lede">{{ t('frontHeroBody') }}</p>
        <div class="oa-front-actions">
          <button
            v-if="site.registration_enabled"
            type="button"
            class="oa-btn primary oa-front-cta"
            @click="router.push('/register')"
          >
            {{ t('frontCreateAccount') }}<IconChevronRight :size="15" />
          </button>
          <button type="button" class="oa-btn oa-front-cta" @click="router.push('/login')">
            {{ t('frontOpenChat') }}
          </button>
          <a class="oa-btn oa-front-cta" :href="DOCS_URL" target="_blank" rel="noopener noreferrer">
            {{ t('frontReadDocs') }}<IconArrowUpRight :size="15" />
          </a>
        </div>

        <div class="oa-front-speaks">
          <span class="oa-front-speaks-label">{{ t('frontSpeaks') }}</span>
          <!-- The strip is duplicated so the loop has a second copy to bring
               in as the first leaves; the clone is the same words, so it is
               hidden rather than read out twice. -->
          <div class="oa-front-marquee">
            <div class="oa-front-marquee-row">
              <span v-for="name in SPEAKS" :key="name">{{ name }}</span>
            </div>
            <div class="oa-front-marquee-row" aria-hidden="true">
              <span v-for="name in SPEAKS" :key="`clone-${name}`">{{ name }}</span>
            </div>
          </div>
        </div>
      </div>

      <span class="oa-front-scroll" :class="{ gone: scrolled }" aria-hidden="true">
        {{ t('frontScrollHint') }}
      </span>
    </section>

    <section id="front-features" class="oa-front-section oa-front-reveal">
      <header class="oa-front-head-block">
        <span class="oa-front-kicker">{{ t('frontFeaturesKicker') }}</span>
        <h2>{{ t('frontFeaturesTitle') }}</h2>
        <p>{{ t('frontFeaturesBody') }}</p>
      </header>
      <div class="oa-front-cards">
        <!-- The spotlight follows the pointer across the card it is on, which
             is why the handler is here and not on the grid: one listener per
             card keeps the coordinates in that card's own box, and a card the
             pointer never touches never repaints. -->
        <article
          v-for="(feature, index) in FEATURES"
          :key="feature.title"
          class="oa-front-card oa-front-reveal"
          :style="{ '--oa-front-delay': `${index * 70}ms` }"
          @pointermove="onPointer"
        >
          <span class="oa-front-card-icon"><component :is="feature.icon" :size="17" /></span>
          <h3>{{ t(feature.title) }}</h3>
          <p>{{ t(feature.body) }}</p>
        </article>
      </div>
    </section>

    <section id="front-flow" class="oa-front-section oa-front-reveal">
      <header class="oa-front-head-block">
        <span class="oa-front-kicker">{{ t('frontFlowKicker') }}</span>
        <h2>{{ t('frontFlowTitle') }}</h2>
        <p>{{ t('frontFlowBody') }}</p>
      </header>

      <div class="oa-front-flow">
        <div class="oa-front-flow-side">
          <div class="oa-front-node">
            <span class="oa-front-card-icon"><IconMessage :size="16" /></span>
            <strong>{{ t('frontFlowWeb') }}</strong>
            <span>{{ t('frontFlowWebNote') }}</span>
          </div>
          <div class="oa-front-node">
            <span class="oa-front-card-icon"><IconTerminal :size="16" /></span>
            <strong>{{ t('frontFlowApps') }}</strong>
            <span>{{ t('frontFlowAppsNote') }}</span>
          </div>
        </div>

        <!-- Pulses travelling along the wires, both sides at once. Drawn as
             a background gradient sliding along a line rather than as nodes
             moving, so the whole diagram is two painted elements. -->
        <div class="oa-front-wires" aria-hidden="true">
          <span class="oa-front-wire" /><span class="oa-front-wire" />
        </div>

        <div class="oa-front-core">
          <span class="oa-front-core-ring" aria-hidden="true" />
          <span class="oa-front-card-icon"><IconCpu :size="18" /></span>
          <strong>{{ t('frontFlowCore') }}</strong>
          <span>{{ t('frontFlowCoreNote') }}</span>
        </div>

        <div class="oa-front-wires" aria-hidden="true">
          <span class="oa-front-wire" />
        </div>

        <div class="oa-front-flow-side">
          <div class="oa-front-node">
            <span class="oa-front-card-icon"><IconServer :size="16" /></span>
            <strong>{{ t('frontFlowUpstream') }}</strong>
            <span>{{ t('frontFlowUpstreamNote') }}</span>
          </div>
        </div>
      </div>
    </section>

    <section id="front-showcase" class="oa-front-section oa-front-reveal">
      <header class="oa-front-head-block">
        <span class="oa-front-kicker">{{ t('frontShowcaseKicker') }}</span>
        <h2>{{ t('frontShowcaseTitle') }}</h2>
        <p>{{ t('frontShowcaseBody') }}</p>
      </header>

      <div class="oa-front-panes">
        <figure class="oa-front-pane">
          <figcaption>{{ t('frontShowcaseCurl') }}</figcaption>
          <pre class="oa-front-code">{{ CURL }}</pre>
        </figure>

        <figure class="oa-front-pane">
          <figcaption>{{ t('frontShowcaseTerminal') }}</figcaption>
          <div class="oa-front-term">
            <div class="oa-front-term-line">
              <span class="oa-front-prompt">arc&gt;</span>
              <span>{{ typed }}</span>
              <span v-if="typing" class="oa-front-caret" aria-hidden="true" />
            </div>
            <!-- Keyed by index, not by the line: these are lines of console
                 output, and two identical or empty ones are entirely normal —
                 a content key would collide and drop one. -->
            <div v-for="(line, at) in output" :key="at" class="oa-front-term-out">{{ line }}</div>
          </div>
        </figure>
      </div>
    </section>

    <section class="oa-front-section oa-front-facts oa-front-reveal">
      <span class="oa-front-kicker">{{ t('frontFactsKicker') }}</span>
      <div class="oa-front-fact-row">
        <div v-for="(fact, index) in FACTS" :key="fact.label" class="oa-front-fact">
          <strong>{{ counted[index] ?? 0 }}</strong>
          <span class="oa-front-fact-label">{{ t(fact.label) }}</span>
          <span class="oa-front-fact-note">{{ t(fact.note) }}</span>
        </div>
      </div>
    </section>

    <section class="oa-front-section oa-front-close oa-front-reveal">
      <div class="oa-front-close-card">
        <span class="oa-front-blob close" aria-hidden="true" />
        <h2>{{ t('frontCtaTitle') }}</h2>
        <p>{{ t('frontCtaBody') }}</p>
        <div class="oa-front-actions">
          <button
            v-if="site.registration_enabled"
            type="button"
            class="oa-btn primary oa-front-cta"
            @click="router.push('/register')"
          >
            {{ t('frontCreateAccount') }}<IconChevronRight :size="15" />
          </button>
          <button type="button" class="oa-btn oa-front-cta" @click="router.push('/login')">
            {{ t('frontSignIn') }}
          </button>
        </div>
      </div>

      <footer class="oa-front-foot">
        <span>{{ site.name }} · {{ t('frontFooterNote') }}</span>
        <span class="oa-header-spacer" />
        <a :href="DOCS_URL" target="_blank" rel="noopener noreferrer">{{ t('frontReadDocs') }}</a>
        <a :href="SOURCE_URL" target="_blank" rel="noopener noreferrer">
          <IconGithub :size="14" />{{ t('frontSource') }}
        </a>
      </footer>
    </section>
  </div>
</template>
