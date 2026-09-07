<script setup lang="ts">
// The composer's `+` menu.
//
// What goes with the next message: what to attach, and how much allowance is
// left to send it with.
//
// Reasoning used to live here too, which was the wrong place for it. How hard
// a model thinks is part of choosing the model, not part of attaching a file,
// and having it here meant the state was set in one menu and displayed in a
// chip on the other side of the screen. It moved into the model control,
// beside send.

import { ref } from 'vue';
import { fetchUsage, type UsageSummary } from '@/api/usage';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import OaMenuItem from '@/components/OaMenuItem.vue';
import OaUsageWindow from '@/components/OaUsageWindow.vue';
import { t } from '@/composables/useI18n';
import { IconFile, IconImage, IconPlus } from '@/icons';

const props = defineProps<{ disabled: boolean }>();
const emit = defineEmits<{
  (event: 'pick-images'): void;
  (event: 'pick-files'): void;
}>();

// Usage is fetched when the menu opens rather than on a timer: it is only
// ever read while the panel is on screen, and polling it would be a request
// per user per interval for a number nobody is looking at.
const usage = ref<UsageSummary | null>(null);
const loaded = ref(false);

function onToggle(wasOpen: boolean): void {
  if (wasOpen || loaded.value) return;
  loaded.value = true;
  void fetchUsage()
    .then((summary) => { usage.value = summary; })
    // Usage is a courtesy; a failure just leaves the row out.
    .catch(() => { usage.value = null; });
}
</script>

<template>
  <OaMenu group-class="oa-composer-menu" menu-class="oa-menu-up">
    <template #trigger="{ open, toggle }">
      <OaIconButton
        class="ai-chat-plus"
        :label="t('composerMenu')"
        :disabled="props.disabled"
        aria-haspopup="menu"
        :aria-expanded="open ? 'true' : 'false'"
        @click="onToggle(open); toggle()"
      >
        <IconPlus :size="18" />
      </OaIconButton>
    </template>

    <template #default="{ close }">
      <OaMenuItem :title="t('addImage')" @click="close(); emit('pick-images')">
        <template #leading><IconImage :size="14" /></template>
      </OaMenuItem>
      <OaMenuItem :title="t('addFile')" :sub="t('addFileHint')" @click="close(); emit('pick-files')">
        <template #leading><IconFile :size="14" /></template>
      </OaMenuItem>

      <!-- A build that reports no usage gets no frame at all: an empty one
           would be worse than showing nothing. -->
      <div v-if="!loaded || usage" class="oa-menu-quota">
        <span v-if="!loaded" class="oa-menu-quota-reset">{{ t('loading') }}</span>
        <span
          v-else-if="usage!.unlimited || !usage!.windows.some((entry) => entry.enforced)"
          class="oa-menu-quota-reset"
        >{{ t('quotaUnlimited') }}</span>
        <template v-else>
          <OaUsageWindow
            v-for="entry in usage!.windows.filter((window) => window.enforced)"
            :key="entry.kind"
            :window="entry"
            :display="usage!.display ?? 'absolute'"
          />
        </template>
      </div>
    </template>
  </OaMenu>
</template>
