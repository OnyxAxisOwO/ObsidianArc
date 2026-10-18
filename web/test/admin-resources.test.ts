import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App } from 'vue';
import AdminResources from '../src/views/admin/AdminResources.vue';
import { adminApi, type Resources } from '../src/admin/api';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle } from './helpers';

let app: App | undefined;
let host: HTMLElement;
let panels: HTMLElement;
let actions: HTMLElement;

function snapshot(processSec: number): Resources {
  return {
    storage: { held_bytes: 0, held_count: 0, discarded_count: 0, by_user: [] },
    memory: { heap_bytes: 0, heap_sys_bytes: 0, sys_bytes: 0, gc_count: 0, gc_pause_ms: 0, goroutines: 1 },
    cpu: { cores: 2, gomaxprocs: 2, process_sec: processSec },
    sampled_at: Date.now(),
  } as unknown as Resources;
}


async function mountWith(processSec: number): Promise<void> {
  vi.spyOn(adminApi, 'resources').mockResolvedValue(snapshot(processSec));
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(AdminResources);
  } });
  app.mount(host);
  await settle();
}

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

describe('AdminResources, the CPU time a process has used', () => {
  // The minutes and the seconds were each rounded on their own, so a total
  // just short of a minute was printed as "1m 60s".
  it('does not print sixty seconds next to a minute', async () => {
    await mountWith(119.6);
    expect(host.textContent).toContain('2m 0s');
    expect(host.textContent).not.toContain('1m 60s');
  });

  it('reads a total just under a minute as a minute, not as sixty seconds', async () => {
    app?.unmount();
    document.body.textContent = '';
    host = document.createElement('div');
    panels = document.createElement('div');
    actions = document.createElement('div');
    document.body.append(host, panels, actions);
    await mountWith(59.96);
    expect(host.textContent).toContain('1m 0s');
    expect(host.textContent).not.toContain('60.0s');
  });
});
