import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, h, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { changeLanguage } from '../src/composables/useI18n';
import AdminWorkbench from '../src/views/admin/AdminWorkbench.vue';
import type { WorkbenchGroup } from '../src/views/admin/workbench';
import { settle } from './helpers';

// Two categories with three sections between them. The words are the ones the
// search reads for a section that has no dictionary keys of its own, and they
// are not the category labels, so a search matches a section and not its heading.
const groups: WorkbenchGroup[] = [
  { id: 'alpha', label: 'close', hint: 'close', icon: { render: () => null }, sections: ['alpha-one', 'alpha-two'] },
  { id: 'beta', label: 'save', hint: 'save', icon: { render: () => null }, sections: ['beta-one'] },
];
const words = { 'alpha-one': ['quartz'], 'alpha-two': ['marble'], 'beta-one': ['zebra'] };

let app: App | undefined;
let host: HTMLElement;

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
});

// Not mountComponent: the workbench is given a default slot, which mountComponent's props cannot carry.
async function mount(path: string): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:rest(.*)*', component: { render: () => null } }],
  });
  await router.push(path);
  await router.isReady();
  app = createApp({
    render: () => h(AdminWorkbench, { page: 'test', groups, words }, {
      default: ({ visible }: { visible: (id: string) => boolean }) =>
        ['alpha-one', 'alpha-two', 'beta-one'].filter(visible).map((id) => h('p', { class: 'sec' }, id)),
    }),
  });
  app.use(router);
  app.mount(host);
  await settle();
}

function shownSections(): string[] {
  return [...host.querySelectorAll('.sec')].map((node) => node.textContent!);
}

function searchField(): HTMLInputElement {
  return host.querySelector<HTMLInputElement>('.oa-workbench-section-head input[type="search"]')!;
}

function type(value: string): void {
  const field = searchField();
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('AdminWorkbench', () => {
  it('shows the sections of the chosen category, and moves with the tabs', async () => {
    await mount('/admin');

    expect(shownSections()).toEqual(['alpha-one', 'alpha-two']);

    host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab')[1]!.click();
    await settle();

    expect(shownSections()).toEqual(['beta-one']);
    expect(host.querySelectorAll<HTMLButtonElement>('.oa-workbench-tab')[1]!.getAttribute('aria-pressed')).toBe('true');
  });

  it('searches every category at once, whichever one is open', async () => {
    await mount('/admin');

    type('zebra');
    await settle();

    expect(shownSections()).toEqual(['beta-one']);
    expect(host.querySelector('.oa-workbench')!.classList.contains('is-searching')).toBe(true);
  });

  it('says so, and draws nothing, when a search matches no section', async () => {
    await mount('/admin');

    type('no-such-setting');
    await settle();

    expect(shownSections()).toEqual([]);
    const empty = host.querySelector<HTMLParagraphElement>('.oa-search-empty')!;
    expect(empty).not.toBeNull();
    expect(empty.getAttribute('role')).toBe('status');
  });

  it('opens the category that holds a section named in the hash, even when it is closed', async () => {
    await mount('/admin#beta-one');

    expect(shownSections()).toEqual(['beta-one']);
  });
});
