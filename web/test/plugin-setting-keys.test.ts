// A package may label a setting however it likes. The install panel and the
// settings card both draw these controls, and the key the value is written
// under must stay visible beside the label: approving an install means
// approving a write to that key.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import PluginSettingControls from '../src/views/admin/PluginSettingControls.vue';
import type { SettingsSection } from '../src/plugins/types';

let app: App | undefined;
let host: HTMLElement;

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
});

function mountControls(section: SettingsSection, draft: Record<string, string>): void {
  app = createApp({ render: () => h(PluginSettingControls, { section, draft, hints: {} }) });
  app.mount(host);
}

describe('plugin setting controls show the key they write', () => {
  it('shows the storage key beside a label the package chose', async () => {
    const section = {
      id: 'install', page: 'security', category: '', column: 0,
      title: () => 'Package',
      controls: [{ kind: 'text', key: 'quota.global_reset_at', label: () => 'Batch window' }],
      defaults: { 'quota.global_reset_at': '9999999999999' },
    } as unknown as SettingsSection;
    mountControls(section, { 'quota.global_reset_at': '9999999999999' });
    await nextTick();

    const key = host.querySelector<HTMLElement>('code.oa-plugin-control-key');
    expect(key?.textContent).toBe('quota.global_reset_at');
    expect(host.textContent).toContain('Batch window');
  });

  it('shows one key per control that is drawn', async () => {
    const section = {
      id: 'settings', page: 'security', category: '', column: 0,
      title: () => 'Package',
      controls: [
        { kind: 'text', key: 'plugin.endpoint', label: () => 'Endpoint' },
        { kind: 'switch', key: 'plugin.enabled', label: () => 'Enabled' },
      ],
      defaults: {},
    } as unknown as SettingsSection;
    mountControls(section, { 'plugin.endpoint': '', 'plugin.enabled': 'true' });
    await nextTick();

    const keys = [...host.querySelectorAll<HTMLElement>('code.oa-plugin-control-key')].map((node) => node.textContent);
    expect(keys).toEqual(['plugin.endpoint', 'plugin.enabled']);
  });
});
