import { computed, ref } from 'vue';
import { ApiError, BACKOFFICE_LOCKED, fetchApi, noteBackofficeRefusal, noteSessionEnded } from '@/api/client';
import type { ConsoleExecHandlers, ConsoleExecRequest, ConsoleExecDone } from '@/api/console';
import { readSSE, streamRefusal } from '@/api/sse';
import type { Message } from '@/api/chat';
import { currentLanguage, t } from '@/composables/useI18n';
import { onSignOut } from '@/stores/session';
import { apiErrorText } from '@/lib/api-error';

interface ToolCall { id: string; name: string; arguments: string; read_only?: boolean }
export type WorkMode = 'ask' | 'assist' | 'full';
interface WireMessage {
  role: 'user' | 'assistant' | 'tool';
  content: string;
  tool_calls?: ToolCall[];
  tool_call_id?: string;
}
interface Answer { content: string; reasoning?: string; tool_calls: ToolCall[] }
export interface AssistantAction {
  id: string;
  line: string;
  readOnly: boolean;
  status: 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'unknown';
  output: string;
}
interface AssistantMessage extends Message { actions?: AssistantAction[] }
interface Thread {
  id: string;
  title: string;
  messages: AssistantMessage[];
  history: WireMessage[];
}

function requiresVerification(code: string): boolean {
  return code === 'two_factor_backoffice' || code === 'two_factor_backoffice_verify';
}

function failure(error: unknown): string {
  return error instanceof ApiError ? apiErrorText(error) : t('failed');
}

// Consume the command frames as transcript data. The terminal's runtime
// client stays in its own chunk; both readers share the wire types and SSE
// reader, so opening ordinary chat gains no command-transport dependency.
async function execConsoleLine(request: ConsoleExecRequest, handlers: ConsoleExecHandlers, signal: AbortSignal): Promise<void> {
  const response = await fetchApi('/api/console/exec', {
    method: 'POST', credentials: 'same-origin', signal,
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ ...request, lang: currentLanguage() }),
  });
  if (!response.ok) throw await streamRefusal(response);
  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));
  await readSSE(response.body, ({ event, data }) => {
    const frame = JSON.parse(data) as Record<string, unknown> | null;
    if (!frame) return;
    if (event === 'out' && typeof frame.text === 'string') handlers.onOut?.(frame.text);
    else if (event === 'done') handlers.onDone?.(frame as unknown as ConsoleExecDone);
    else if (event === 'error') handlers.onError?.({ code: String(frame.code), message: String(frame.message) });
  });
}

async function answer(
  model: string, reasoning: { enabled: boolean; effort: string }, history: WireMessage[],
  mode: WorkMode, signal: AbortSignal, delta: (kind: 'content' | 'reasoning', text: string) => void,
): Promise<Answer> {
  const response = await fetchApi('/api/admin/assistant', {
    method: 'POST', credentials: 'same-origin', signal,
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ model_id: model, reasoning, messages: history, stream: true, mode, lang: currentLanguage() }),
  });
  if (!response.ok) throw await streamRefusal(response);
  if (!response.body) throw new ApiError(0, 'stream', t('streamMissing'));
  let result: Answer | undefined;
  await readSSE(response.body, ({ event, data }) => {
    const value = JSON.parse(data) as Record<string, unknown> | null;
    if (!value) return;
    if ((event === 'delta' || event === 'reasoning') && typeof value.text === 'string') {
      delta(event === 'delta' ? 'content' : 'reasoning', value.text);
    } else if (event === 'error') {
      throw new ApiError(502, String(value.code), String(value.message));
    } else if (event === 'done' && typeof value.content === 'string' && Array.isArray(value.tool_calls)) {
      result = value as unknown as Answer;
    }
  });
  // A broken stream must never turn a partial command into an approval card.
  if (!result) throw new ApiError(0, 'stream', t('streamMissing'));
  return result;
}

export function createAssistantSession() {
  const threads = ref<Thread[]>([]);
  const activeID = ref('');
  const draft = ref('');
  const busy = ref(false);
  const mode = ref<WorkMode>('ask');
  const error = ref('');
  const active = computed(() => threads.value.find((thread) => thread.id === activeID.value));
  const messages = computed(() => active.value?.messages ?? []);
  const pending = computed(() => messages.value.flatMap((message) => message.actions ?? []).filter((action) => action.status === 'pending'));
  let controller: AbortController | undefined;
  let modelID = '';
  let reasoning = { enabled: false, effort: 'medium' };
  let rounds = 0;

  function newThread(): void {
    stop();
    activeID.value = '';
    draft.value = '';
    error.value = '';
  }

  function open(id: string): void {
    if (id === activeID.value) return;
    stop();
    activeID.value = id;
    draft.value = '';
    error.value = '';
  }

  function message(role: 'user' | 'assistant', content = ''): AssistantMessage {
    return { id: crypto.randomUUID(), seq: messages.value.length, role, content, created_at: Date.now() };
  }

  async function generate(): Promise<void> {
    const thread = active.value;
    if (!thread) return;
    if (++rounds > 12 || thread.history.length > 56) {
      error.value = t('assistantLimit');
      return;
    }
    const abort = new AbortController();
    controller = abort;
    busy.value = true;
    error.value = '';
    const row = message('assistant');
    row.model_id = modelID;
    thread.messages.push(row);
    const live = thread.messages[thread.messages.length - 1]!;
    try {
      const result = await answer(modelID, reasoning, thread.history, mode.value, abort.signal, (kind, text) => {
        if (!abort.signal.aborted) live[kind] = (live[kind] ?? '') + text;
      });
      if (abort.signal.aborted) return;
      live.content = result.content;
      if (result.reasoning) live.reasoning = result.reasoning;
      // Decode the whole batch before committing any of it to the transcript.
      const actions = result.tool_calls.map((call): AssistantAction => {
        const args = JSON.parse(call.arguments) as { line?: unknown };
        if (typeof args.line !== 'string' || !args.line.trim() || call.name !== 'run_admin_command') throw new Error('invalid tool');
        return { id: call.id, line: args.line, readOnly: call.read_only === true, status: 'pending', output: '' };
      });
      live.actions = actions;
      thread.history.push({ role: 'assistant', content: result.content, ...(actions.length ? { tool_calls: result.tool_calls } : {}) });
    } catch (cause) {
      if (!abort.signal.aborted) error.value = failure(cause);
    } finally {
      if (!live.content && !live.reasoning && !live.actions?.length) thread.messages = thread.messages.filter((entry) => entry.id !== live.id);
      if (controller === abort) { controller = undefined; busy.value = false; }
    }
    if (!abort.signal.aborted && active.value?.id === thread.id) await drive();
  }

  async function submit(model: string, thinking: { enabled: boolean; effort: string }): Promise<void> {
    const text = draft.value.trim();
    if (!text || !model || busy.value || pending.value.length) return;
    if (!active.value) {
      const thread: Thread = { id: crypto.randomUUID(), title: text.slice(0, 40), messages: [], history: [] };
      threads.value.unshift(thread);
      // These conversations live only in this signed-in browser session.
      threads.value = threads.value.slice(0, 20);
      activeID.value = thread.id;
    }
    modelID = model;
    reasoning = { ...thinking };
    rounds = 0;
    active.value!.messages.push(message('user', text));
    active.value!.history.push({ role: 'user', content: text });
    draft.value = '';
    await generate();
  }

  function settle(action: AssistantAction, status: AssistantAction['status'], output: string, thread = active.value): void {
    action.status = status;
    action.output = output;
    thread?.history.push({ role: 'tool', tool_call_id: action.id, content: JSON.stringify({ status, output }) });
  }

  async function decide(action: AssistantAction, approve: boolean): Promise<void> {
    if (busy.value || pending.value[0]?.id !== action.id) return;
    const thread = active.value;
    if (!thread) return;
    if (!approve) {
      settle(action, 'skipped', t('assistantSkipped'), thread);
    } else {
      const abort = new AbortController();
      controller = abort;
      busy.value = true;
      action.status = 'running';
      let output = '';
      let verdict: 'done' | 'failed' = 'failed';
      let terminal = false;
      let verification = false;
      let truncated = false;
      try {
        await execConsoleLine({ line: action.line, cols: 120, json: true }, {
          onOut(text) { truncated ||= output.length + text.length > 60000; output = (output + text).slice(0, 60000); },
          onDone(result) {
            terminal = true; verdict = result.ok ? 'done' : 'failed';
            verification = requiresVerification(result.code);
            noteBackofficeRefusal(result.code);
            noteSessionEnded(result.code);
            if (result.code === 'two_factor_backoffice') window.dispatchEvent(new Event(BACKOFFICE_LOCKED));
          },
          onError(result) { output += `\n${result.message}`; verification = requiresVerification(result.code); },
        }, abort.signal);
        if (verification) {
          action.status = 'pending';
          action.output = t('assistantVerify');
          return;
        }
        if (truncated) output += `\n${t('assistantTruncated')}`;
        if (abort.signal.aborted) {
          settle(action, 'unknown', t('assistantInterrupted'), thread);
        } else {
          settle(action, terminal ? verdict : 'unknown', output || t(terminal ? 'assistantNoOutput' : 'assistantInterrupted'), thread);
        }
      } catch (cause) {
        if (cause instanceof ApiError && requiresVerification(cause.code)) {
          action.status = 'pending';
          action.output = t('assistantVerify');
          return;
        }
        settle(action, 'unknown', `${failure(cause)}\n${t('assistantInterrupted')}`, thread);
      } finally {
        if (controller === abort) { controller = undefined; busy.value = false; }
      }
      if (abort.signal.aborted) return;
    }
    if (active.value?.id !== thread.id) return;
    if (!pending.value.length) await generate();
    else await drive();
  }

  async function drive(): Promise<void> {
    const action = pending.value[0];
    if (!busy.value && action && !action.output && (mode.value === 'full' || (mode.value === 'assist' && action.readOnly))) {
      await decide(action, true);
    }
  }

  function setMode(next: WorkMode): void {
    mode.value = next;
    void drive();
  }

  function pause(): void {
    controller?.abort();
  }

  function stop(): void {
    controller?.abort();
    // A pending call needs a result even when cancelled, or the next model
    // request would contain tool calls with no matching responses.
    for (const action of pending.value) settle(action, 'skipped', t('assistantSkipped'));
  }

  function reset(): void {
    stop();
    controller = undefined;
    busy.value = false;
    threads.value = [];
    activeID.value = '';
    draft.value = '';
    error.value = '';
    mode.value = 'ask';
  }

  return { threads, activeID, active, messages, draft, busy, error, pending, mode, submit, decide, stop, pause, setMode, newThread, open, reset };
}

export const assistant = createAssistantSession();
onSignOut(assistant.reset);
