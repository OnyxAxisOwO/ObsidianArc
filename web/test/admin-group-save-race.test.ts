import { afterEach, expect, it, vi } from 'vitest';
import AdminGroups from '../src/views/admin/AdminGroups.vue';
import { adminApi, emptyPolicy, type Group } from '../src/admin/api';
import { changeLanguage } from '../src/composables/useI18n';
import { adminShell, mountComponent, settle } from './helpers';

afterEach(() => vi.restoreAllMocks());

it('saves the original group policy and leaves the newly opened group alone', async () => {
  await changeLanguage('en');
  const first = { id: 'a', name: 'Alpha', description: '', is_default: false, allow_all_models: true, members: 0, model_ids: [] } as unknown as Group;
  const second = { ...first, id: 'b', name: 'Beta' } as Group;
  const firstPolicy = { ...emptyPolicy('group', 'a'), rpm: 10 };
  const secondPolicy = { ...emptyPolicy('group', 'b'), rpm: 20 };
  vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [first, second], policies: [firstPolicy, secondPolicy] });
  vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
  let release!: (value: { group: Group }) => void;
  vi.spyOn(adminApi, 'updateGroup').mockReturnValue(new Promise((resolve) => { release = resolve; }));
  const savePolicy = vi.spyOn(adminApi, 'savePolicy').mockResolvedValue({ policy: firstPolicy });
  const host = document.createElement('div');
  const panels = document.createElement('div');
  const actions = document.createElement('div');
  document.body.append(host, panels, actions);
  const unmount = await mountComponent(AdminGroups, {}, { host, panels, adminView: adminShell(actions) });
  try {
    await settle();
    const rows = host.querySelectorAll<HTMLTableRowElement>('tbody tr');
    rows[0]!.click();
    await settle();
    panels.querySelector<HTMLButtonElement>('.oa-panel-foot .oa-btn.primary')!.click();
    await settle();
    rows[1]!.click();
    await settle();
    release({ group: first });
    await settle();
    expect(savePolicy).toHaveBeenCalledWith(expect.objectContaining({ scope_id: 'a', rpm: 10 }));
    expect(panels.textContent).toContain('Beta');
  } finally { unmount(); }
});
