<script setup lang="ts">
// Admin Safe Mode: an eye toggle button with a dropdown menu in the backoffice header.
//
// Clicking the switch opens a dropdown menu where the operator can toggle Safe Mode
// on/off and select which categories of sensitive information to mask (turning values into ******).

import { computed } from 'vue';
import OaIconButton from '@/components/OaIconButton.vue';
import OaMenu from '@/components/OaMenu.vue';
import { t } from '@/composables/useI18n';
import {
  IconCheck,
  IconEye,
  IconEyeOff,
  IconUsers,
  IconServer,
  IconKey,
  IconChart,
  IconFile,
} from '@/icons';
import {
  activeMaskCount,
  SAFE_CATEGORIES,
  safeModeCategories,
  safeModeEnabled,
  setAllCategories,
  toggleCategory,
  toggleSafeMode,
  type SafeCategory,
} from '@/admin/safeMode';

const categoryIcons: Record<SafeCategory, typeof IconUsers> = {
  users: IconUsers,
  providers: IconServer,
  credentials: IconKey,
  billing: IconChart,
  logs: IconFile,
};

const label = computed(() => (
  safeModeEnabled.value ? t('safeModeActive') : t('safeModeInactive')
));

function onCategoryClick(cat: SafeCategory): void {
  if (!safeModeEnabled.value) {
    safeModeEnabled.value = true;
    safeModeCategories.value[cat] = true;
  } else {
    toggleCategory(cat);
  }
}
</script>

<template>
  <OaMenu menu-class="oa-menu-safe-mode">
    <template #trigger="{ open, toggle }">
      <OaIconButton
        class="oa-icon-btn oa-safe-mode-btn"
        :class="{ 'is-active': safeModeEnabled }"
        :label="label"
        aria-haspopup="menu"
        :aria-expanded="open ? 'true' : 'false'"
        @click="toggle"
      >
        <IconEyeOff v-if="safeModeEnabled" :size="17" />
        <IconEye v-else :size="17" />
        <span v-if="safeModeEnabled" class="oa-safe-dot" />
      </OaIconButton>
    </template>

    <template #default>
      <div class="oa-safe-mode-head" @click="toggleSafeMode()">
        <div class="oa-safe-mode-title">
          <IconEyeOff v-if="safeModeEnabled" :size="16" />
          <IconEye v-else :size="16" />
          <span>{{ t('safeMode') }}</span>
        </div>
        <label class="oa-safe-switch" :title="label" @click.stop>
          <input
            type="checkbox"
            :checked="safeModeEnabled"
            @change="toggleSafeMode()"
          />
        </label>
      </div>

      <p class="oa-safe-mode-desc">{{ t('safeModeHint') }}</p>

      <div class="oa-safe-mode-list" role="group" :aria-label="t('safeModeWhatToMask')">
        <button
          v-for="cat in SAFE_CATEGORIES"
          :key="cat.key"
          type="button"
          role="checkbox"
          :aria-checked="safeModeCategories[cat.key] && safeModeEnabled"
          class="oa-safe-mode-row"
          :class="{
            'is-checked': safeModeCategories[cat.key],
            'is-dimmed': !safeModeEnabled,
          }"
          @click="onCategoryClick(cat.key)"
        >
          <span class="oa-safe-mode-check" aria-hidden="true">
            <IconCheck v-if="safeModeCategories[cat.key]" :size="12" />
          </span>
          <component :is="categoryIcons[cat.key]" :size="15" class="oa-safe-mode-icon" aria-hidden="true" />
          <div class="oa-safe-mode-row-info">
            <span class="oa-safe-mode-row-name">{{ t(cat.labelKey) }}</span>
            <span class="oa-safe-mode-row-sub">{{ t(cat.hintKey) }}</span>
          </div>
          <span class="oa-safe-mode-sample" aria-hidden="true">******</span>
        </button>
      </div>

      <div class="oa-safe-mode-foot">
        <div class="oa-safe-mode-actions">
          <button
            type="button"
            class="oa-menu-head-action oa-safe-mode-action-btn"
            @click="setAllCategories(true); toggleSafeMode(true)"
          >
            {{ t('safeModeSelectAll') }}
          </button>
          <button
            type="button"
            class="oa-menu-head-action oa-safe-mode-action-btn"
            @click="setAllCategories(false)"
          >
            {{ t('safeModeClearAll') }}
          </button>
        </div>
        <span class="oa-safe-mode-status">
          {{ t('safeModeStatusActive', { count: activeMaskCount, total: SAFE_CATEGORIES.length }) }}
        </span>
      </div>
    </template>
  </OaMenu>
</template>
