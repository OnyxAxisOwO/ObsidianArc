// The redemption-code panel: a cleared number is not a value, and the copy
// button tells the operator when a copy was refused.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, shallowRef, type App } from 'vue';
import AdminCodes from '../src/views/admin/AdminCodes.vue';
import { adminApi, type RedemptionCode } from '../src/admin/api';
import * as markdown from '../src/chat/markdown';
import { providePanelHost } from '../src/composables/usePanelHost';
import { changeLanguage, t } from '../src/composables/useI18n';
import { provideAdminView } from '../src/views/admin/adminView';
import { settle, button } from './helpers';

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
  vi.spyOn(adminApi, 'codes').mockResolvedValue({ codes: [] });
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  vi.restoreAllMocks();
});


async function mount(): Promise<void> {
  app = createApp({ setup() {
    providePanelHost(shallowRef(panels));
    provideAdminView({ actionsHost: actions, setTitle: () => {}, reload: () => {}, params: [] });
    return () => h(AdminCodes);
  } });
  app.mount(host);
  await settle();
}

function fieldFor(root: ParentNode, label: string): HTMLElement {
  const found = [...root.querySelectorAll<HTMLElement>('.oa-field')].find((field) =>
    field.querySelector('.oa-field-label')?.textContent === label);
  if (!found) throw new Error(`Field not found: ${label}`);
  return found;
}

function input(node: HTMLInputElement, value: string): void {
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
}

async function openNewBatch(): Promise<void> {
  (actions.querySelector<HTMLButtonElement>('#addCode')!).click();
  await settle();
}

describe('generating a batch of codes from a cleared number', () => {
  // A cleared card count used to mint one card per code, and a cleared card
  // length thirty days, neither of them shown in the panel once it was sent.
  it('does not mint a batch while a number is blank, and says that a field is empty', async () => {
    const create = vi.spyOn(adminApi, 'createCode').mockResolvedValue({ codes: [] });
    await mount();
    await openNewBatch();

    input(fieldFor(panels, t('codeCards')).querySelector<HTMLInputElement>('input')!, '');
    await settle();
    button(panels, t('add')).click();
    await settle();

    expect(create).not.toHaveBeenCalled();
    expect(panels.textContent).toContain(t('settingNumberRequired'));
  });
});

describe('copying a minted batch', () => {
  // The button carries the result. A refused copy used to show nothing at all,
  // which reads as a copy that worked, and a result that stayed on the button
  // hid the label the next copy starts from.
  it('says a refused copy on the button, then puts the label back', async () => {
    vi.spyOn(adminApi, 'createCode').mockResolvedValue({
      codes: [{
        id: 'c1', code: 'ABCD2345EFGH', name: '', windows: [], cards: 10, claimed: 0,
        card_days: 30, expires_at: 0, note: '', created_at: Date.now(),
      } as unknown as RedemptionCode],
    });
    vi.spyOn(markdown, 'copyToClipboard').mockResolvedValue(false);
    await mount();
    await openNewBatch();

    button(panels, t('add')).click();
    await settle();
    button(panels, t('copyAll')).click();
    await settle();
    expect(panels.textContent).toContain(t('copyFailed'));

    await new Promise((resolve) => setTimeout(resolve, 1600));
    await settle();
    expect(panels.textContent).not.toContain(t('copyFailed'));
    expect(button(panels, t('copyAll'))).toBeDefined();
  }, 10_000);
});
