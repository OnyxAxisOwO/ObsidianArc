// The two skeletons the simpler administration screens share: a first read
// that always ends as loaded, and a single-row delete that keeps the panel
// usable when the server refuses. Before these were shared, a failed delete
// of one row had no test at all; only the bulk delete did.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, ref, shallowRef, type App } from 'vue';
import AdminGroups from '../src/views/admin/AdminGroups.vue';
import AdminCodes from '../src/views/admin/AdminCodes.vue';
import { adminApi, type Group } from '../src/admin/api';
import { ApiError } from '../src/api/client';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';
import { runLoad, runRemove } from '../src/views/admin/shared';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panels = document.createElement('div');
  actions = document.createElement('div');
  document.body.append(host, panels, actions);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
});

describe('runLoad', () => {
  it('words a failed read and still marks the screen loaded', async () => {
    const error = ref('');
    const loaded = ref(false);

    await runLoad(async () => {
      throw new ApiError(504, 'plugin_timeout', 'The plugin did not answer in time.');
    }, { error, loaded });

    expect(error.value).toBe(t('errPluginTimeout'));
    expect(loaded.value).toBe(true);
  });

  it('clears the last failure before a retry that succeeds', async () => {
    const error = ref('earlier failure');
    const loaded = ref(true);
    let seen = 'unset';

    await runLoad(async () => { seen = error.value; }, { error, loaded });

    expect(seen).toBe('');
    expect(error.value).toBe('');
    expect(loaded.value).toBe(true);
  });

  it('draws a failed first read on a real screen instead of the loading line', async () => {
    vi.spyOn(adminApi, 'codes').mockRejectedValue(new ApiError(503, 'store_down', 'The card store is down.'));
    app = createApp({ setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
      return () => h(AdminCodes);
    } });
    app.mount(host);
    await settle();

    expect(host.textContent).toContain('The card store is down.');
    expect(host.textContent).not.toContain(t('loading'));
  });
});

describe('runRemove', () => {
  it('lowers busy and words the refusal when the delete fails', async () => {
    const busy = ref(false);
    const error = ref('');
    const done = vi.fn();

    await runRemove(async () => {
      throw new ApiError(409, 'model_routed', 'Another model routes to this one.');
    }, { busy, error }, done);

    expect(busy.value).toBe(false);
    expect(error.value).toBe(t('errModelRouted'));
    expect(done).not.toHaveBeenCalled();
  });

  it('leaves busy raised on success, for the caller to reset with the next panel', async () => {
    const busy = ref(false);
    const error = ref('');
    const done = vi.fn();

    await runRemove(async () => undefined, { busy, error }, done);

    expect(busy.value).toBe(true);
    expect(done).toHaveBeenCalledOnce();
  });

  it('uses the page’s own refusal wording when one is passed', async () => {
    const busy = ref(false);
    const error = ref('');

    await runRemove(async () => {
      throw new ApiError(409, 'anything', 'English.');
    }, { busy, error }, () => {}, () => 'page wording');

    expect(error.value).toBe('page wording');
  });
});

describe('deleting one group, refused', () => {
  const group = {
    id: 'g1', name: 'Pro', description: '', is_default: false, allow_all_models: true, model_ids: [], members: 0,
  } as unknown as Group;

  it('keeps the panel open with the refusal and the delete offered again', async () => {
    vi.spyOn(adminApi, 'groups').mockResolvedValue({ groups: [group], policies: [] });
    vi.spyOn(adminApi, 'modelOptions').mockResolvedValue({ models: [] });
    vi.spyOn(adminApi, 'memberOptions').mockResolvedValue({ users: [] } as never);
    const remove = vi.spyOn(adminApi, 'deleteGroup')
      .mockRejectedValue(new ApiError(503, 'store_down', 'The group store is down.'));
    app = createApp({ setup() {
      providePanelHost(shallowRef(panels));
      provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
      return () => h(AdminGroups);
    } });
    app.mount(host);
    await settle();

    host.querySelector<HTMLElement>('.oa-table tbody tr')!.click();
    await settle();
    const deleteButton = (): HTMLButtonElement => [...panels.querySelectorAll<HTMLButtonElement>('button')]
      .find((node) => node.textContent?.trim() === t('deleteLabel'))!;
    deleteButton().click();
    await settle();
    deleteButton().click();
    await settle();

    expect(remove).toHaveBeenCalledWith('g1');
    expect(panels.querySelector('.oa-panel')).not.toBeNull();
    expect(panels.querySelector('.oa-drawer-flash')?.textContent).toBe('The group store is down.');
    // Busy came back down, so the button reads its label again rather than "…".
    expect(deleteButton().disabled).toBe(false);
  });
});
