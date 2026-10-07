// What a plugin whose forms an operator writes can put in front of people —
// a list of entries, each opening its own form — and the controls it can ask
// an operator for. Held here: an entry opens from its address and draws what
// the plugin resolved; a required choice stops the send; the instance's own
// check is solved by the core and handed to the plugin as a proof, and a
// refused proof is worded by the core; an action card hides what the draft
// made irrelevant, builds rows, and lists options the server knows.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App, type Component } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { ApiError } from '../src/api/client';
import { changeLanguage, t } from '../src/composables/useI18n';
import { providePanelHost } from '../src/composables/usePanelHost';
import { installPlugins } from '../src/plugins/registry';
import type { ActionCardSpec, ChallengeProof, UserEntryView, UserFormValues, UserPanelSpec } from '../src/plugins/types';
import PluginUserPanel from '../src/views/PluginUserPanel.vue';
import { provideAdminView } from '../src/views/admin/adminView';
import PluginActionCard from '../src/views/admin/PluginActionCard.vue';

vi.mock('../src/lib/pow', () => ({
  solvePoW: vi.fn(() => ({ promise: Promise.resolve({ salt: 's', nonce: 7 }), cancel() {} })),
}));
vi.mock('../src/api/auth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../src/api/auth')>()),
  fetchPoWChallenge: vi.fn(async () => ({ challenge: 'c', salt: 's', maxNumber: 10, expires: 0, signature: 'x' })),
}));

let app: App | undefined;
let host: HTMLElement;
let panelHost: HTMLElement;

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
  }
}

async function mount(component: Component, props: Record<string, unknown> = {}): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  await router.push('/');
  await router.isReady();
  app = createApp({ setup() {
    providePanelHost(ref(panelHost));
    provideAdminView({ actionsHost: document.createElement('div'), setTitle() {}, reload() {}, params: [] });
    return () => h(component, props);
  } });
  app.use(router);
  app.mount(host);
  await settle();
}

function button(root: ParentNode, label: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>('button')]
    .find((node) => node.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

function label(root: ParentNode, text: string): HTMLElement | undefined {
  return [...root.querySelectorAll<HTMLElement>('.oa-field, .oa-switch-field')]
    .find((node) => node.textContent?.includes(text));
}

beforeEach(async () => {
  await changeLanguage('en');
  host = document.createElement('div');
  panelHost = document.createElement('div');
  document.body.append(host, panelHost);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  panelHost.remove();
  installPlugins([]);
  vi.clearAllMocks();
});

function survey(view: () => UserEntryView): UserPanelSpec {
  return {
    slug: 'events',
    title: () => 'Events',
    entries: {
      empty: () => 'Nothing on',
      load: async () => [{ id: 'e1', title: 'Spring survey', badge: { label: 'Open', tone: 'default' }, meta: ['Ends soon'] }],
      open: async () => view(),
    },
  };
}

function questions(run: UserEntryView['submit'] extends infer S ? S extends { run: infer R } ? R : never : never,
  challenge: UserEntryView['challenge'] = null): UserEntryView {
  return {
    title: 'Spring survey',
    body: 'Tell us how we did.\nTwo questions.',
    facts: [{ label: 'Reward', value: '5 credits' }],
    controls: [
      { kind: 'choice', key: 'q1', label: () => 'How was it?', list: true, required: true, options: [
        { value: '0', label: () => 'Very good' }, { value: '1', label: () => 'Not so good' },
      ] },
      { kind: 'checks', key: 'q2', label: () => 'What do you use?', max: 2, options: [
        { value: '0', label: () => 'Chat' }, { value: '1', label: () => 'Images' }, { value: '2', label: () => 'API' },
      ] },
    ],
    challenge,
    submit: { label: 'Submit answers', run },
  };
}

describe('a panel of entries an operator wrote', () => {
  it('lists the entries, and opens one from its address with the plugin\'s own controls', async () => {
    installPlugins([{ name: 'events', userPanels: [survey(() => questions(async () => ''))] }]);
    await mount(PluginUserPanel, { slug: 'events' });
    expect(panelHost.textContent).toContain('Spring survey');
    expect(panelHost.textContent).toContain('Ends soon');

    app?.unmount();
    await mount(PluginUserPanel, { slug: 'events', entry: 'e1' });
    expect(panelHost.querySelector('.oa-plugin-entry-body')?.textContent).toBe('Tell us how we did.\nTwo questions.');
    expect(panelHost.textContent).toContain('5 credits');
    expect(panelHost.querySelectorAll('.oa-plugin-option')).toHaveLength(5);
  });

  it('does not send until a required choice is made, then sends answers and an empty proof', async () => {
    const run = vi.fn(async (_values: UserFormValues, _proof: ChallengeProof) => 'Thanks');
    let opened = 0;
    installPlugins([{ name: 'events', userPanels: [survey(() => {
      opened += 1;
      return opened > 1 ? { title: 'Spring survey', outcome: { text: 'You took part', tone: 'default' } } : questions(run);
    })] }]);
    await mount(PluginUserPanel, { slug: 'events', entry: 'e1' });

    button(panelHost, 'Submit answers').click();
    await settle();
    expect(run).not.toHaveBeenCalled();
    expect(panelHost.textContent).toContain(t('pluginFieldRequired', { field: 'How was it?' }));

    for (const option of ['Very good', 'API', 'Chat']) {
      button(panelHost, option).click();
      await nextTick();
    }
    // A third tick past the limit is refused, and says so.
    button(panelHost, 'Images').click();
    await nextTick();
    expect(panelHost.textContent).toContain(t('pluginChecksTooMany', { count: 2 }));
    button(panelHost, 'Submit answers').click();
    await settle();

    // Picked in the order the options are listed, not the order they were ticked.
    expect(run).toHaveBeenCalledWith({ q1: '0', q2: ['0', '2'] }, {});
    // Opened again: what came of it, and no form — and the plugin's own
    // sentence is not said a second time beside it.
    expect(panelHost.textContent).toContain('You took part');
    expect(panelHost.textContent).not.toContain('Thanks');
    expect(panelHost.querySelector('.oa-plugin-option')).toBeNull();
  });

  it('solves the instance\'s proof of work and hands it over, and words a refused one itself', async () => {
    const run = vi.fn(async (_values: UserFormValues, _proof: ChallengeProof): Promise<string> => {
      throw new ApiError(400, 'challenge_failed', 'the challenge was not passed');
    });
    installPlugins([{ name: 'events', userPanels: [survey(() => questions(run, { pow: true, turnstile_site_key: '' }))] }]);
    await mount(PluginUserPanel, { slug: 'events', entry: 'e1' });
    button(panelHost, 'Not so good').click();
    await nextTick();
    button(panelHost, 'Submit answers').click();
    await settle();
    expect(run).toHaveBeenCalledWith({ q1: '1', q2: [] }, { pow: { salt: 's', nonce: 7 } });
    expect(panelHost.textContent).toContain(t('challengeFailed'));
  });
});

describe('an action card\'s controls', () => {
  function card(run: (draft: Record<string, string>) => Promise<string>): ActionCardSpec {
    return {
      id: 'publish', page: 'plugin:events', title: () => 'Publish',
      controls: [
        { kind: 'select', key: 'reward', label: () => 'Reward', options: [
          { value: 'none', label: () => 'None' }, { value: 'bonus', label: () => 'Bonus' },
        ] },
        {
          kind: 'select', key: 'bar', label: () => 'Bonus bar', options: [],
          load: async () => [{ value: 'b1', label: 'Prize bar' }],
          visible: (draft) => draft.reward === 'bonus',
        },
        { kind: 'switch', key: 'check', label: () => 'Human check' },
        {
          kind: 'rows', key: 'questions', label: () => 'Questions', add: () => 'Add question', min: 1,
          columns: [
            { kind: 'text', key: 'title', label: () => 'Question' },
            { kind: 'select', key: 'type', label: () => 'Type', options: [
              { value: 'single', label: () => 'Single' }, { value: 'text', label: () => 'Text' },
            ] },
            { kind: 'switch', key: 'required', label: () => 'Required' },
          ],
        },
      ],
      defaults: { reward: 'none', check: 'false' },
      button: { label: () => 'Publish it', run },
    };
  }

  it('hides what the draft made irrelevant, builds rows, and sends every value as a string', async () => {
    const run = vi.fn(async () => 'Published');
    await mount(PluginActionCard, { card: card(run) });
    expect(label(host, 'Bonus bar')).toBeUndefined();
    // A rows control with a minimum holds the button until it has them.
    expect(button(host, 'Publish it').disabled).toBe(true);

    button(host, 'Add question').click();
    await nextTick();
    const cell = host.querySelector<HTMLInputElement>('.oa-plugin-row .oa-field-input')!;
    cell.value = 'Favourite model?';
    cell.dispatchEvent(new Event('input', { bubbles: true }));
    await nextTick();
    host.querySelector<HTMLInputElement>('.oa-plugin-row-check input')!.click();
    await nextTick();
    label(host, 'Human check')!.querySelector<HTMLInputElement>('input')!.click();
    await nextTick();
    expect(button(host, 'Publish it').disabled).toBe(false);

    button(host, 'Publish it').click();
    await settle();
    expect(run).toHaveBeenCalledWith({
      reward: 'none', check: 'true',
      questions: JSON.stringify([{ title: 'Favourite model?', type: 'single', required: 'true' }]),
    });
  });

  it('lists the options only the server knows once the control is shown', async () => {
    const spec = card(async () => '');
    spec.defaults = { reward: 'bonus' };
    await mount(PluginActionCard, { card: spec });
    const bar = label(host, 'Bonus bar');
    expect(bar).toBeDefined();
    bar!.querySelector<HTMLElement>('button')?.click();
    await settle();
    expect(document.body.textContent).toContain('Prize bar');
  });
});
