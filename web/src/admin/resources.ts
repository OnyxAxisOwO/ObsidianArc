// What this instance is costing the machine it runs on.
//
// Three questions an operator asks when something feels wrong and the logs
// say nothing: whose files are filling the disk, how much memory the process
// is holding, and whether it is actually busy. The first is the one with a
// name attached, so it gets the table.
//
// The page re-reads itself while it is open, because a CPU figure that does
// not move is not a CPU figure. It stops when the body it drew into leaves
// the document — the admin rail swaps that node whole on every tab change,
// and nothing tells this file about it.

import { t } from '../i18n';
import { button, clear, el } from '../ui/dom';
import { renderTable } from '../ui/table';
import { adminApi, type Resources } from './api';
import { failure, type AdminView } from './admin-page';
import { section, statGrid } from './dashboard';

const REFRESH_MS = 5000;

export async function renderResources(view: AdminView): Promise<void> {
  view.setTitle(t('navResources'), t('resourcesSubtitle'));

  let snapshot: Resources;
  try {
    snapshot = await adminApi.resources();
  } catch (error) {
    failure(view, error);
    return;
  }

  clear(view.actions);
  view.actions.appendChild(button('oa-btn', t('refresh'), () => view.reload()));

  clear(view.body);
  paint(view.body, snapshot);

  // One timer per visit. It reads the page again rather than re-rendering it
  // through the router, so a table somebody is looking at does not jump.
  const timer = window.setInterval(() => {
    if (!view.body.isConnected) {
      window.clearInterval(timer);
      return;
    }
    void adminApi.resources()
      .then((next) => {
        if (!view.body.isConnected) return;
        clear(view.body);
        paint(view.body, next);
      })
      // A failed refresh leaves what is on screen. The next tick tries again,
      // and an operator watching a struggling instance would rather see the
      // last good numbers than an error where they were.
      .catch(() => {});
  }, REFRESH_MS);
}

function paint(body: HTMLElement, snapshot: Resources): void {
  const { storage, memory, cpu } = snapshot;

  body.appendChild(section(t('resStorage'), statGrid([
    { label: t('resHeld'), value: bytes(storage.held_bytes), note: t('resHeldNote') },
    { label: t('resFiles'), value: String(storage.held_count) },
    { label: t('resDiscarded'), value: String(storage.discarded_count), note: t('resDiscardedNote') },
  ])));

  body.appendChild(renderTable({
    columns: [
      { header: t('colAccount'), cell: (row) => row.name },
      { header: t('resFiles'), cell: (row) => String(row.count), numeric: true, width: '90px' },
      {
        header: t('resHeld'),
        cell: (row) => bytes(row.bytes),
        numeric: true,
        width: '110px',
        sort: (row) => row.bytes,
      },
    ],
    rows: storage.by_user,
    empty: t('resNoFiles'),
  }));

  body.appendChild(section(t('resMemory'), statGrid([
    // Heap in use answers "is this leaking"; what the process took from the
    // operating system is what a container's limit is measured against.
    { label: t('resHeap'), value: bytes(memory.heap_bytes), note: t('resHeapNote') },
    { label: t('resProcessMemory'), value: bytes(memory.sys_bytes), note: t('resProcessMemoryNote') },
    { label: t('resGoroutines'), value: String(memory.goroutines) },
    {
      label: t('resGC'),
      value: String(memory.gc_count),
      note: t('resGCPause', { ms: memory.gc_pause_ms.toFixed(2) }),
    },
  ])));

  body.appendChild(section(t('resCPU'), statGrid([
    {
      label: t('resCPUShare'),
      // Of one core, which is why the count is right beside it: 240% on an
      // eight-core box is busy, and on a two-core box it is saturated.
      value: cpu.percent === undefined ? t('resMeasuring') : `${cpu.percent.toFixed(1)}%`,
      note: cpu.window_sec ? t('resCPUWindow', { sec: Math.round(cpu.window_sec) }) : t('resCPUFirst'),
    },
    { label: t('resCores'), value: `${cpu.cores}`, note: t('resGomaxprocs', { n: cpu.gomaxprocs }) },
    {
      label: t('resCPUTime'),
      value: cpu.process_sec === undefined ? '—' : duration(cpu.process_sec),
      note: t('resCPUTimeNote'),
    },
  ])));

  body.appendChild(el('p', 'oa-field-hint', t('resNoDatabaseSize')));
}

/** Bytes as a figure a person reads, to one decimal below a gigabyte. */
function bytes(count: number): string {
  const mb = count / (1024 * 1024);
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`;
  if (mb >= 10) return `${Math.round(mb)} MB`;
  if (mb >= 0.1) return `${mb.toFixed(1)} MB`;
  if (count >= 1024) return `${Math.round(count / 1024)} KB`;
  return `${count} B`;
}

/** Seconds of CPU as hours, minutes and seconds — it is a total, not a clock. */
function duration(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${Math.round(seconds % 60)}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
