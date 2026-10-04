<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { adminApi, type Group } from '@/admin/api';
import type { Account } from '@/api/auth';
import OaPagination from '@/components/OaPagination.vue';
import type { PageState } from '@/components/table-types';
import OaCheckList from '@/components/OaCheckList.vue';
import OaFormSection from '@/components/OaFormSection.vue';
import OaSearchField from '@/components/OaSearchField.vue';
import OaSelectField from '@/components/OaSelectField.vue';
import OaTextField from '@/components/OaTextField.vue';
import { t } from '@/composables/useI18n';
import { absoluteTime } from '@/lib/format';
import { rememberedPageSize } from '@/lib/page-size';
import { maskUser } from '@/admin/safeMode';
import ExpiryPresets from './ExpiryPresets.vue';

const props = defineProps<{ group: Group; groups: Group[] }>();
const emit = defineEmits<{ (event: 'saved'): void }>();
const search = ref('');
const scope = ref('members');
const offset = ref(0);
const accounts = ref<Account[]>([]);
const total = ref(0);
const selected = ref<string[]>([]);
const expiresAt = ref('');
const loading = ref(false);
const busy = ref(false);
const error = ref('');
const message = ref('');
const pageSize = ref(rememberedPageSize());
let request = 0;
let timer = 0;


const items = computed(() => accounts.value.map((account) => {
  const groupName = props.groups.find((entry) => entry.id === account.group_id)?.name ?? t('noGroup');
  const term = account.group_expires_at
    ? t('membershipExpiresOn', { when: absoluteTime(account.group_expires_at) })
    : t('membershipPermanent');
  return {
    value: account.id,
    label: maskUser(account.nickname || account.username),
    sub: `@${maskUser(account.username)} · ${groupName} · ${term}`,
  };
}));

async function load(): Promise<void> {
  const current = ++request;
  loading.value = true;
  error.value = '';
  const query = new URLSearchParams({ q: search.value, limit: String(pageSize.value), offset: String(offset.value) });
  if (scope.value === 'members') query.set('group_id', props.group.id);
  try {
    const result = await adminApi.memberOptions(`?${query}`);
    if (current !== request) return;
    accounts.value = result.users ?? [];
    total.value = result.total;
  } catch (failure) {
    if (current === request) error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    if (current === request) loading.value = false;
  }
}

watch([search, scope], () => {
  // Invalidate during the debounce too, so an earlier response cannot refill
  // the list under the new search term.
  ++request;
  loading.value = true;
  offset.value = 0;
  window.clearTimeout(timer);
  timer = window.setTimeout(() => void load(), 200);
});

function page(next: PageState): void {
  pageSize.value = next.pageSize;
  offset.value = (next.page - 1) * next.pageSize;
  void load();
}

async function save(): Promise<void> {
  if (busy.value || !selected.value.length) return;
  error.value = '';
  message.value = '';
  const expiry = expiresAt.value ? new Date(expiresAt.value).getTime() : 0;
  if (expiresAt.value && (!Number.isFinite(expiry) || expiry <= Date.now())) {
    error.value = t('membershipExpiryInvalid');
    return;
  }
  busy.value = true;
  try {
    const result = await adminApi.assignGroupMembers(props.group.id, [...selected.value], expiry);
    selected.value = [];
    message.value = t('membersSaved', { count: result.updated });
    emit('saved');
    await load();
  } catch (failure) {
    error.value = failure instanceof Error ? failure.message : String(failure);
  } finally {
    busy.value = false;
  }
}

void load();
onBeforeUnmount(() => {
  ++request;
  window.clearTimeout(timer);
});
</script>

<template>
  <section id="groupMembers" class="oa-group-members">
    <OaFormSection :title="t('groupMembers')" :hint="t('groupMembersHint')" />
    <OaSelectField
      v-model="scope"
      :label="t('memberScope')"
      :options="[
        { value: 'members', label: t('currentMembers') },
        { value: 'all', label: t('allAccounts') },
      ]"
    />
    <OaSearchField v-model="search" :label="t('searchUsers')" />
    <p v-if="loading" class="oa-field-hint">{{ t('loading') }}</p>
    <OaCheckList
      v-else
      v-model="selected"
      :label="t('selectMembers')"
      :items="items"
      :empty-text="t('noSearchResults')"
    />
    <OaPagination :page="Math.floor(offset / pageSize) + 1" :page-size="pageSize" :total="total" :busy="loading" @change="page" />
    <p class="oa-field-hint">{{ t('membersSelected', { count: selected.length }) }}</p>
    <OaTextField
      v-model="expiresAt"
      type="datetime-local"
      :label="t('membershipExpiry')"
      :hint="t('membershipExpiryHint')"
    />
    <ExpiryPresets permanent @pick="expiresAt = $event" />
    <div class="oa-log-filter-actions">
      <button type="button" class="oa-btn" :disabled="busy || !selected.length" @click="selected = []">{{ t('clearSelection') }}</button>
      <button type="button" class="oa-btn primary" :disabled="busy || !selected.length || selected.length > 200" @click="save">
        {{ busy ? t('loading') : t('saveMembers') }}
      </button>
    </div>
    <p v-if="error" class="oa-field-hint" role="alert">{{ error }}</p>
    <p v-if="message" class="oa-field-hint" role="status">{{ message }}</p>
  </section>
</template>
