<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import OaSearchField from '@/components/OaSearchField.vue';
import { t, type StringKey } from '@/composables/useI18n';
import { matchesSearch } from '@/lib/search';
import { ADMIN_FEATURES } from './features';
import type { WorkbenchGroup } from './workbench';

const props = withDefaults(defineProps<{
  page: string;
  groups: WorkbenchGroup[];
  terms?: Record<string, StringKey[]>;
  /** Already-translated words, for sections that have no dictionary keys: a plugin's. */
  words?: Record<string, string[]>;
  searchable?: boolean;
  columns?: [string[], string[]];
}>(), { searchable: true });
const route = useRoute();
const router = useRouter();
const selected = ref(props.groups[0]!.id);
const query = ref('');
const active = computed(() => props.groups.find((group) => group.id === selected.value) ?? props.groups[0]!);
const visibleIds = computed(() => new Set(props.groups.flatMap((group) => group.sections.filter((id) => {
  if (!query.value.trim()) return group.id === selected.value;
  const feature = ADMIN_FEATURES.find((item) => item.pageSlug === props.page && item.id === id);
  const keys = props.terms?.[id] ?? (feature ? [feature.titleKey, ...(feature.searchKeys ?? [])] : []);
  return matchesSearch(query.value, t(group.label), ...keys.map((key) => t(key)), ...(feature?.keywords ?? []),
    ...(props.words?.[id] ?? []));
}))));
function visible(id: string): boolean { return visibleIds.value.has(id); }
function select(id: string): void {
  selected.value = id;
  query.value = '';
  if (route.hash) void router.replace({ hash: '' });
}
// Keep global search links working even when their target is in a closed
// category. v-show in the pages preserves drafts and conditional controls.
watch(() => route.hash, (hash) => {
  const group = props.groups.find((entry) => entry.sections.includes(hash.slice(1)));
  if (group) { selected.value = group.id; query.value = ''; }
}, { immediate: true });
</script>

<template>
  <div class="oa-workbench" :class="{ 'is-searching': query.trim() }">
    <nav class="oa-workbench-nav" :aria-label="t('controlCategories')">
      <button v-for="group in groups" :key="group.id" type="button"
        class="oa-workbench-tab" :class="{ active: selected === group.id && !query.trim() }"
        :aria-pressed="selected === group.id && !query.trim()" @click="select(group.id)">
        <component :is="group.icon" :size="15" /><strong>{{ t(group.label) }}</strong>
      </button>
    </nav>
    <div class="oa-workbench-section-head">
      <div><h2>{{ query.trim() ? t('controlSearchResults') : t(active.label) }}</h2>
        <p>{{ query.trim() ? t('controlSearchHint') : t(active.hint) }}</p></div>
      <OaSearchField v-if="searchable" v-model="query" :label="t('searchSettings')" />
    </div>
    <p v-if="!visibleIds.size" class="oa-search-empty" role="status">{{ t('noSearchResults') }}</p>
    <div class="oa-workbench-grid">
      <template v-if="columns">
        <div v-for="(ids, index) in columns" :key="index" v-show="ids.some(visible)" class="oa-workbench-column">
          <slot :name="index === 0 ? 'left' : 'right'" :visible="visible" />
        </div>
      </template>
      <slot :visible="visible" />
    </div>
  </div>
</template>
