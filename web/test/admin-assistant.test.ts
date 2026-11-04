import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createAssistantSession, assistant } from '../src/admin/assistant';
import { BACKOFFICE_LOCKED } from '../src/api/client';
import AdminAssistant from '../src/views/admin/AdminAssistant.vue';
import { adminShell, button, mountComponent, settle } from './helpers';
import { t } from '../src/composables/useI18n';
import { adopt, forget } from '../src/stores/session';
import { makeAccount } from './fixtures/account';

function stream(event: string, data: unknown): Response {
  return new Response(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`, { headers: { 'Content-Type': 'text/event-stream' } });
}
function proposal(line = 'user list', readOnly = true, id = 'call-1'): Response {
  return stream('done', { content: 'I can do that.', tool_calls: [{ id, name: 'run_admin_command', arguments: JSON.stringify({ line }), read_only: readOnly }] });
}
function commandDone(ok = true, code = ''): Response { return stream('done', { ok, code, json: true, lang: 'en' }); }
const completed = () => stream('done', { content: 'Finished.', tool_calls: [] });
const thinking = { enabled: false, effort: 'medium' };

describe('administration assistant execution modes', () => {
  let fetcher: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    fetcher = vi.fn();
    vi.stubGlobal('fetch', fetcher);
    adopt(makeAccount({ role: 'super_admin' }));
  });
  afterEach(() => { assistant.reset(); forget(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it('asks before querying and only continues after the approved command has finished', async () => {
    fetcher.mockResolvedValueOnce(proposal()).mockResolvedValueOnce(commandDone()).mockResolvedValueOnce(completed());
    const chat = createAssistantSession();
    chat.draft.value = 'Inspect users';
    await chat.submit('model-1', thinking);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(chat.pending.value).toHaveLength(1);
    await chat.decide(chat.pending.value[0]!, true);
    expect(fetcher.mock.calls.map((call) => call[0])).toEqual(['/api/admin/assistant', '/api/console/exec', '/api/admin/assistant']);
    const continuation = JSON.parse(fetcher.mock.calls[2]![1].body);
    expect(continuation.messages.at(-1).role).toBe('tool');
    expect(JSON.parse(continuation.messages.at(-1).content).status).toBe('done');
    expect(chat.pending.value).toHaveLength(0);
  });

  it('assisted mode automatically queries but asks before changes', async () => {
    fetcher.mockResolvedValueOnce(proposal()).mockResolvedValueOnce(commandDone()).mockResolvedValueOnce(proposal('user edit someone --nickname Demo', false, 'call-2'));
    const chat = createAssistantSession();
    chat.setMode('assist');
    chat.draft.value = 'Inspect and update';
    await chat.submit('model-1', thinking);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(chat.pending.value[0]?.line).toContain('user edit');
    expect(chat.pending.value[0]?.status).toBe('pending');
  });

  it('full access automatically runs writes through the existing permission-checked command transport', async () => {
    fetcher.mockResolvedValueOnce(proposal('user delete someone --yes', false)).mockResolvedValueOnce(commandDone()).mockResolvedValueOnce(completed());
    const chat = createAssistantSession();
    chat.setMode('full');
    chat.draft.value = 'Remove the account';
    await chat.submit('model-1', thinking);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(JSON.parse(fetcher.mock.calls[0]![1].body).mode).toBe('full');
    expect(JSON.parse(fetcher.mock.calls[1]![1].body).line).toBe('user delete someone --yes');
  });

  it('skipping a proposal never executes it and reports the skip to the model', async () => {
    fetcher.mockResolvedValueOnce(proposal()).mockResolvedValueOnce(completed());
    const chat = createAssistantSession();
    chat.draft.value = 'Inspect';
    await chat.submit('model-1', thinking);
    await chat.decide(chat.pending.value[0]!, false);
    expect(fetcher.mock.calls.every((call) => call[0] === '/api/admin/assistant')).toBe(true);
    expect(JSON.parse(JSON.parse(fetcher.mock.calls[1]![1].body).messages.at(-1).content).status).toBe('skipped');
  });

  it('full access pauses for human verification and does not retry automatically', async () => {
    fetcher.mockResolvedValueOnce(proposal('user edit someone --nickname Demo', false))
      .mockResolvedValueOnce(commandDone(false, 'two_factor_backoffice_verify'));
    const locked = vi.fn();
    window.addEventListener(BACKOFFICE_LOCKED, locked);
    try {
      const chat = createAssistantSession();
      chat.setMode('full');
      chat.draft.value = 'Update';
      await chat.submit('model-1', thinking);
      expect(locked).toHaveBeenCalledOnce();
      expect(fetcher).toHaveBeenCalledTimes(2);
      expect(chat.pending.value[0]?.output).toBe(t('assistantVerify'));
      expect(chat.busy.value).toBe(false);
      chat.setMode('full');
      await settle();
      expect(fetcher).toHaveBeenCalledTimes(2);
    } finally { window.removeEventListener(BACKOFFICE_LOCKED, locked); }
  });

  it('a broken model stream never creates an executable approval card', async () => {
    fetcher.mockResolvedValue(stream('delta', { text: 'A partial answer' }));
    const chat = createAssistantSession();
    chat.setMode('full');
    chat.draft.value = 'Inspect';
    await chat.submit('model-1', thinking);
    expect(chat.pending.value).toHaveLength(0);
    expect(chat.error.value).not.toBe('');
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('stop cancels the provider and prevents its late result from running a command', async () => {
    let resolve!: (value: Response) => void;
    fetcher.mockImplementationOnce(() => new Promise<Response>((done) => { resolve = done; }));
    const chat = createAssistantSession();
    chat.setMode('full');
    chat.draft.value = 'Inspect';
    const sending = chat.submit('model-1', thinking);
    chat.stop();
    expect(fetcher.mock.calls[0]![1].signal.aborted).toBe(true);
    resolve(proposal());
    await sending;
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(chat.pending.value).toHaveLength(0);
    expect(chat.busy.value).toBe(false);
  });

  it('switching conversations during a command cannot write its result into the new conversation', async () => {
    let finish!: (value: Response) => void;
    fetcher.mockResolvedValueOnce(proposal()).mockImplementationOnce(() => new Promise<Response>((done) => { finish = done; }));
    const chat = createAssistantSession();
    chat.draft.value = 'Inspect';
    await chat.submit('model-1', thinking);
    const oldID = chat.activeID.value;
    const run = chat.decide(chat.pending.value[0]!, true);
    chat.newThread();
    finish(commandDone());
    await run;
    expect(chat.active.value).toBeUndefined();
    expect(chat.threads.value.find((thread) => thread.id === oldID)?.history.at(-1)?.role).toBe('tool');
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('draws the main chat message and model controls with three distinct work modes', async () => {
    fetcher.mockResolvedValue(new Response(JSON.stringify({ models: [] }), { headers: { 'Content-Type': 'application/json' } }));
    const actions = document.createElement('div');
    document.body.append(actions);
    const unmount = await mountComponent(AdminAssistant, {}, { adminView: adminShell(actions) });
    try {
      expect(document.querySelector('.ai-chat-composer .ai-chat-input')).not.toBeNull();
      expect(document.querySelector('.ai-model-control')).not.toBeNull();
      expect(document.querySelector('.oa-rune-field')).not.toBeNull();
      button(document, t('assistantModeAsk')).click();
      await settle();
      expect(document.body.textContent).toContain(t('assistantModeAssist'));
      expect(document.body.textContent).toContain(t('assistantModeFull'));
    } finally { unmount(); }
  });
});
