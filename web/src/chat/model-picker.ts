// The control that decides what answers, and how hard it thinks.
//
// It used to be two things in two places: a chip in the header that chose a
// model, and a section inside the composer's `+` menu that set the reasoning
// effort. They were always one decision — which model, and how much of it —
// and splitting them meant the two halves could disagree on screen, with the
// chip carrying a thinking marker for a state you had to open another menu to
// read or change.
//
// So it is one control, and it sits where the decision is acted on: in the
// composer, beside send. Opening it shows the model on top and the effort
// underneath, because that is the order they are chosen in — you pick what
// answers, then how much work it should do.
//
// The effort is a slider rather than three buttons and a switch. Off is the
// left end of it, which is what makes the separate toggle unnecessary: "how
// hard should it think" and "should it think at all" are the same question
// asked at different volumes.

import { api } from '../api/client';
import { t, type StringKey } from '../i18n';
import { ICONS, el, icon } from '../ui/dom';
import type { Effort, ReasoningState } from './composer-menu';

export interface ModelCapabilities {
  supports_reasoning: boolean;
  supports_images: boolean;
  supports_vision: boolean;
  supports_streaming: boolean;
  supports_system_prompt: boolean;
  supports_tools: boolean;
  context_window: number;
  max_output_tokens: number;
}

export interface AvailableModel extends ModelCapabilities {
  id: string;
  display_name: string;
  description: string;
  avatar: string;
  provider_name: string;
  usable?: boolean;
}

export interface ModelControlOptions {
  /** Restores the last choice; ignored when that model is no longer offered. */
  initialModelID?: string;
  reasoning(): ReasoningState;
  onReasoningChange(next: ReasoningState): void;
  onChange(): void;
  /** Persists a changed selection to the account. */
  onPersist?(modelID: string): void;
}

export interface ModelControl {
  element: HTMLElement;
  /** Fetches the list. Resolves once the chip is showing something real. */
  load(): Promise<void>;
  models(): AvailableModel[];
  current(): AvailableModel | null;
  /** Repaints the chip after something outside it changed. */
  sync(): void;
  select(modelID: string): void;
}

/**
 * The slider's stops.
 *
 * Off is a position on the same track rather than a switch beside it: the
 * question is how much thinking, and none is an amount.
 */
const STOPS: Array<{ enabled: boolean; effort: Effort; label: StringKey }> = [
  { enabled: false, effort: 'medium', label: 'effortOff' },
  { enabled: true, effort: 'low', label: 'effortLow' },
  { enabled: true, effort: 'medium', label: 'effortMedium' },
  { enabled: true, effort: 'high', label: 'effortHigh' },
];

function stopFor(state: ReasoningState): number {
  if (!state.enabled) return 0;
  const found = STOPS.findIndex((stop) => stop.enabled && stop.effort === state.effort);
  return found === -1 ? 2 : found;
}

/** How long the popover takes to settle into a new height. */
const MORPH_MS = 220;

export function createModelControl(options: ModelControlOptions): ModelControl {
  let models: AvailableModel[] = [];
  let selectedID = options.initialModelID ?? '';
  let open = false;
  /** Which face the popover is showing. */
  let view: 'effort' | 'models' = 'effort';

  const group = el('div', 'ai-model-control');

  // --- the chip --------------------------------------------------------------

  const chip = el('button', 'ai-model-chip');
  chip.type = 'button';
  chip.setAttribute('aria-haspopup', 'true');
  chip.setAttribute('aria-expanded', 'false');

  const chipSpark = icon(ICONS.spark, 13);
  chipSpark.classList.add('ai-model-chip-spark');
  const chipName = el('span', 'ai-model-chip-name');
  const chipEffort = el('span', 'ai-model-chip-effort');
  const chipChevron = icon(ICONS.chevron, 12);

  chip.appendChild(chipSpark);
  chip.appendChild(chipName);
  chip.appendChild(chipEffort);
  chip.appendChild(chipChevron);
  group.appendChild(chip);

  // --- the popover -----------------------------------------------------------

  const pop = el('div', 'ai-model-pop');
  pop.hidden = true;
  // The height is animated on this wrapper, so the content inside can be
  // replaced wholesale without the popover jumping to its new size.
  const body = el('div', 'ai-model-pop-body');
  pop.appendChild(body);
  group.appendChild(pop);

  chip.addEventListener('click', (event) => {
    event.stopPropagation();
    toggle(!open);
  });

  // Clicks inside never reach the closer below. Testing containment there
  // would not do: a click that swaps the face detaches the element it landed
  // on, so by the time the document sees the event its target is in no
  // document at all, and "is it inside" answers no.
  pop.addEventListener('click', (event) => event.stopPropagation());

  document.addEventListener('click', () => {
    if (open) toggle(false);
  });
  document.addEventListener('keydown', (event) => {
    if (open && event.key === 'Escape') {
      toggle(false);
      chip.focus();
    }
  });

  function toggle(next: boolean): void {
    if (next === open) return;
    open = next;
    chip.setAttribute('aria-expanded', String(open));
    chip.classList.toggle('open', open);

    if (open) {
      view = 'effort';
      paintBody();
      pop.hidden = false;
      // Reading a layout property commits the closed state, so the class
      // added next has something to transition away from. A frame callback
      // would do the same, except in a tab the browser is not painting —
      // where it never arrives, and the popover would stay invisible.
      void pop.offsetHeight;
      pop.classList.add('open');
      return;
    }
    pop.classList.remove('open');
    window.setTimeout(() => {
      if (!open) pop.hidden = true;
    }, MORPH_MS);
  }

  /**
   * Swaps the popover's contents, animating the height between the two.
   *
   * Measured rather than declared: the model list's height depends on how
   * many models this account has, which is not something a stylesheet can
   * know.
   */
  function morph(build: () => void): void {
    const from = body.offsetHeight;
    build();
    const to = body.scrollHeight;

    if (from === 0 || from === to) {
      body.style.height = '';
      return;
    }
    body.style.height = `${from}px`;
    void body.offsetHeight;
    body.style.height = `${to}px`;
    window.setTimeout(() => { body.style.height = ''; }, MORPH_MS);
  }

  function paintBody(): void {
    body.textContent = '';
    if (view === 'models') body.appendChild(modelsView());
    else body.appendChild(effortView());
  }

  // --- the effort face -------------------------------------------------------

  function effortView(): HTMLElement {
    const wrap = el('div', 'ai-pop-face');
    const model = current();

    // The model, on top, because it is the first half of the decision.
    const modelRow = el('button', 'ai-pop-model');
    modelRow.type = 'button';
    const mark = icon(ICONS.spark, 13);
    mark.classList.add('ai-pop-model-mark');
    modelRow.appendChild(mark);
    modelRow.appendChild(el('span', 'ai-pop-model-name', model ? model.display_name : t('modelNone')));
    modelRow.appendChild(icon(ICONS.chevronRight, 13));
    modelRow.addEventListener('click', () => {
      morph(() => {
        view = 'models';
        body.textContent = '';
        body.appendChild(modelsView());
      });
    });
    wrap.appendChild(modelRow);

    if (!model?.supports_reasoning) {
      // Nothing to set: saying so beats a slider that would be ignored.
      wrap.appendChild(el('p', 'ai-pop-note', t('reasoningUnavailable')));
      return wrap;
    }

    const state = options.reasoning();
    let position = stopFor(state);

    const head = el('div', 'ai-pop-effort-head');
    const label = el('span', 'ai-pop-effort-label', t(STOPS[position]!.label));
    head.appendChild(el('span', 'ai-pop-effort-title', t('reasoningToggle')));
    head.appendChild(el('span', 'oa-header-spacer'));
    head.appendChild(label);
    wrap.appendChild(head);

    const slider = el('div', 'ai-effort');
    const track = el('div', 'ai-effort-track');
    const fill = el('div', 'ai-effort-fill');
    // The sparks live inside the fill and are clipped by it, so they only
    // ever appear over the part that is "on".
    fill.appendChild(el('span', 'ai-effort-sparks'));
    track.appendChild(fill);
    const thumb = el('span', 'ai-effort-thumb');
    track.appendChild(thumb);
    slider.appendChild(track);

    // A real range input on top, invisible: the visual is ours, the keyboard
    // behaviour, the focus ring and the ARIA are the platform's.
    const range = el('input', 'ai-effort-range');
    range.type = 'range';
    range.min = '0';
    range.max = String(STOPS.length - 1);
    range.step = '1';
    range.value = String(position);
    range.setAttribute('aria-label', t('reasoningToggle'));
    slider.appendChild(range);
    wrap.appendChild(slider);

    function paintSlider(): void {
      // Unitless, because the stylesheet uses it inside a calc that mixes
      // pixels and percentages to keep the fill and the thumb agreeing about
      // where the value is.
      const ratio = position / (STOPS.length - 1);
      slider.style.setProperty('--effort-ratio', String(ratio));
      slider.classList.toggle('off', position === 0);
      label.textContent = t(STOPS[position]!.label);
      range.setAttribute('aria-valuetext', t(STOPS[position]!.label));
    }
    paintSlider();

    range.addEventListener('input', () => {
      position = Number(range.value);
      paintSlider();
      const stop = STOPS[position]!;
      options.onReasoningChange({ enabled: stop.enabled, effort: stop.effort });
      sync();
    });

    return wrap;
  }

  // --- the model face --------------------------------------------------------

  function modelsView(): HTMLElement {
    const wrap = el('div', 'ai-pop-face');

    const head = el('button', 'ai-pop-back');
    head.type = 'button';
    head.appendChild(icon(ICONS.chevron, 13));
    head.appendChild(el('span', null, t('chooseModel')));
    head.addEventListener('click', () => {
      morph(() => {
        view = 'effort';
        body.textContent = '';
        body.appendChild(effortView());
      });
    });
    wrap.appendChild(head);

    if (!models.length) {
      wrap.appendChild(el('p', 'ai-pop-note', t('modelsEmpty')));
      return wrap;
    }

    const list = el('div', 'ai-pop-models');
    for (const model of models) {
      const unusable = model.usable === false;
      const row = el('button', 'ai-pop-model-row');
      row.type = 'button';
      row.disabled = unusable;

      const text = el('span', 'ai-pop-model-text');
      text.appendChild(el('span', 'ai-pop-model-title', model.display_name));
      const sub = unusable
        ? t('modelNotAllowedGroup')
        : (model.description || model.provider_name);
      if (sub) text.appendChild(el('span', 'ai-pop-model-sub', sub));
      row.appendChild(text);

      if (model.id === selectedID) row.appendChild(icon(ICONS.check, 14));

      row.addEventListener('click', () => {
        if (unusable) return;
        select(model.id);
        morph(() => {
          view = 'effort';
          body.textContent = '';
          body.appendChild(effortView());
        });
      });
      list.appendChild(row);
    }
    wrap.appendChild(list);
    return wrap;
  }

  // --- state -----------------------------------------------------------------

  function paintChip(): void {
    const model = current();
    chipName.textContent = model ? model.display_name : t('modelNone');
    chip.classList.toggle('placeholder', !model);

    // The effort rides on the chip so the state is legible without opening
    // anything — which is the whole reason the two controls became one.
    const state = options.reasoning();
    const thinking = !!model?.supports_reasoning && state.enabled;
    chipSpark.style.display = thinking ? '' : 'none';
    chipEffort.textContent = thinking ? t(STOPS[stopFor(state)]!.label) : '';
    chipEffort.hidden = !thinking;

    chip.title = model ? `${model.display_name} · ${model.provider_name}` : t('modelNone');
  }

  function current(): AvailableModel | null {
    return models.find((model) => model.id === selectedID) ?? null;
  }

  function select(modelID: string): void {
    const model = models.find((entry) => entry.id === modelID);
    if (!model || model.usable === false) return;
    selectedID = modelID;
    paintChip();
    options.onChange();
    options.onPersist?.(modelID);
  }

  function sync(): void {
    paintChip();
  }

  async function load(): Promise<void> {
    const result = await api.get<{ models: AvailableModel[] }>('/api/models');
    models = result.models;

    const usable = models.filter((model) => model.usable !== false);
    // A remembered model that is gone, or that this account may see but not
    // use, falls back rather than leaving the composer pointing at nothing.
    if (!usable.some((model) => model.id === selectedID)) {
      selectedID = usable[0]?.id ?? '';
    }
    paintChip();
    options.onChange();
  }

  paintChip();

  return {
    element: group,
    load,
    models: () => models,
    current,
    sync,
    select,
  };
}
