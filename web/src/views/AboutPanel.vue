<script setup lang="ts">
// The About panel.
//
// An instance can be renamed and can describe itself however its operator
// wants, and the heading and body honour that. The two facts below do not:
// they name the software rather than the deployment, so a rebranded server
// still answers "what am I actually running, and where did it come from" —
// which is the question this panel exists for.

import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { health } from '@/api/client';
import OaFormSection from '@/components/OaFormSection.vue';
import OaPanel from '@/components/OaPanel.vue';
import { t } from '@/composables/useI18n';
import { formatUptime } from '@/lib/format';
import { siteInfo } from '@/stores/session';

const PRODUCT = 'Obsidian Arc';
const SOURCE_URL = 'https://github.com/OnyxAxisOwO/ObsidianArc';

const router = useRouter();

const version = ref('—');
const uptime = ref('—');

onMounted(() => {
  void health()
    .then((status) => {
      version.value = status.version;
      uptime.value = formatUptime(status.uptime_sec);
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
    @close="router.replace('/')"
  >
    <div class="oa-about">
      <!-- Both fall back rather than render empty: an operator who has never
           opened the settings screen still gets a finished page. -->
      <div class="oa-about-head">
        <h2 class="oa-about-name">{{ siteInfo.about?.title?.trim() || siteInfo.name }}</h2>
        <p class="oa-about-lede">{{ siteInfo.about?.body?.trim() || t('aboutBody') }}</p>
      </div>

      <div class="oa-about-facts">
        <div class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutVersionOf', { product: PRODUCT }) }}</span>
          <span class="oa-about-fact-value">{{ version }}</span>
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

        <div class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutRuntime') }}</span>
          <span class="oa-about-fact-value">{{ uptime }}</span>
        </div>
        <div class="oa-about-fact">
          <span class="oa-about-fact-label">{{ t('aboutLicense') }}</span>
          <span class="oa-about-fact-value">MIT</span>
        </div>
      </div>

      <OaFormSection :title="t('aboutBuiltWith')" :hint="t('aboutBuiltWithBody')" />
    </div>
  </OaPanel>
</template>
