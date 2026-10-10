<script setup lang="ts">
// The About panel.
//
// An instance can be renamed and can describe itself in Markdown however its
// operator wants. The facts below do not change with that copy: they identify
// the software a rebranded server is actually running and where it came from.

import { computed, onMounted, ref } from 'vue';
import { health } from '@/api/client';
import OaMarkdown from '@/components/OaMarkdown.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { usePanelExit } from '@/composables/usePanelExit';
import { formatUptime } from '@/lib/format';
import { siteInfo, isAdmin } from '@/stores/session';

const PRODUCT = 'Obsidian Arc';
const SOURCE_URL = 'https://github.com/OnyxAxisOwO/ObsidianArc';

// Everyone whose work is in the binary, most commits first.
//
// Written down rather than fetched. The page's own CSP is
// `connect-src 'self'`, so a call to the GitHub API from here is refused
// before it leaves, and widening the policy so that an About screen can
// decorate itself would be a poor trade. A list that has to be edited when
// somebody joins is the cost, and it is a small one — it changes a few times
// a year, and it is one line.
//
// dian-ZD is here for prompt-based tool calling, which arrived as a pull
// request rather than as commits of their own: adopting somebody's code and
// leaving them off the list is not something this page should help with.
const CONTRIBUTORS = [
  'OnyxAxisOwO',
  'momo-mnsjtxy',
  'abloom25',
  'Evan0148',
  'dian-ZD',
  'amnssb',
  'mnsjtxy',
  'TXBk0032-1',
];

const panels = usePanelExit();

const version = ref('—');
const uptime = ref('—');

// Whether software facts (version, runtime, contributors) are visible.
// Administrators always see them; regular users see them only when enabled.
const showSoftwareInfo = computed(() => {
  if (isAdmin.value) return true;
  return siteInfo.value.about?.show_software_info !== false;
});

onMounted(() => {
  if (!showSoftwareInfo.value) {
    return;
  }
  void health()
    .then((status) => {
      version.value = status.version ?? '—';
      uptime.value = status.uptime_sec ? formatUptime(status.uptime_sec) : '—';
    })
    .catch(() => {
      // The facts box just keeps its placeholders; nothing else on the page
      // depends on the server being reachable.
    });
});
</script>

<template>
  <OaPanel
    :title="t('about')"
    :footer="false"
    :width="460"
    @close="panels.close('replace')"
  >
    <div class="oa-about">
      <!-- Both fall back rather than render empty: an operator who has never
           opened the settings screen still gets a finished page. -->
      <h2 class="oa-about-name">{{ siteInfo.about?.title?.trim() || siteInfo.name }}</h2>

      <section class="oa-about-details">
        <h3 class="oa-drawer-subhead">{{ t('aboutText') }}</h3>
        <!-- The transcript renderer builds DOM nodes from an allowlist rather
             than accepting HTML, so operator Markdown gets the same XSS
             boundary as model output. -->
        <OaMarkdown
          class="ai-answer oa-about-lede"
          :text="siteInfo.about?.body?.trim() || t('aboutBody')"
        />
      </section>

      <div class="oa-about-facts">
        <div v-if="showSoftwareInfo" class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutVersionOf', { product: PRODUCT }) }}</span>
          <span class="oa-about-fact-value">
            {{ version }}
            <span
              v-if="isAdmin && siteInfo.about?.show_software_info === false"
              class="oa-badge oa-badge-muted"
              style="margin-left: 6px; font-size: 11px;"
            >
              {{ t('aboutAdminOnlyBadge') }}
            </span>
          </span>
        </div>

        <!-- Directly under the version, and showing the address rather than
             the word "Source": the two together are what identifies the
             software when everything above them has been rewritten. -->
        <div class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutSource') }}</span>
          <a
            class="oa-about-fact-value oa-about-link"
            :href="SOURCE_URL"
            target="_blank"
            rel="noopener noreferrer"
          >{{ SOURCE_URL.replace('https://', '') }}</a>
        </div>

        <div v-if="showSoftwareInfo" class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutRuntime') }}</span>
          <span class="oa-about-fact-value">
            {{ uptime }}
            <span
              v-if="isAdmin && siteInfo.about?.show_software_info === false"
              class="oa-badge oa-badge-muted"
              style="margin-left: 6px; font-size: 11px;"
            >
              {{ t('aboutAdminOnlyBadge') }}
            </span>
          </span>
        </div>
        <div class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutLicense') }}</span>
          <span class="oa-about-fact-value">MIT</span>
        </div>
      </div>

      <section v-if="showSoftwareInfo" class="oa-about-details">
        <h3 class="oa-drawer-subhead">
          {{ t('aboutContributors') }}
          <span
            v-if="isAdmin && siteInfo.about?.show_software_info === false"
            class="oa-badge oa-badge-muted"
            style="margin-left: 6px; font-size: 11px;"
          >
            {{ t('aboutAdminOnlyBadge') }}
          </span>
        </h3>
        <p class="oa-about-lede">{{ t('aboutContributorsThanks') }}</p>
        <ul class="oa-about-people">
          <li v-for="handle in CONTRIBUTORS" :key="handle">
            <a
              class="oa-about-person"
              :href="`https://github.com/${handle}`"
              target="_blank"
              rel="noopener noreferrer"
            >{{ handle }}</a>
          </li>
        </ul>
      </section>
    </div>
  </OaPanel>
</template>
