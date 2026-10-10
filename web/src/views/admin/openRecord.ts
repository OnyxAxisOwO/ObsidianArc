// Which of a page's plugin lists has a record open.
//
// Each list opens its record in a 520px panel beside the table. A page with
// two lists — a plugin's pending and decided requests, say — used to open one
// per list, and two such columns left the tables and the page's own heading a
// sliver one character wide. A page reads one record at a time, so
// opening a record in one list closes whatever another list had open.
//
// Provided by the page rather than held in a module: tests mount many pages
// in one process, and a list drawn where nothing provides it keeps its own.

import { inject, provide, ref, type InjectionKey, type Ref } from 'vue';

const OPEN_RECORD: InjectionKey<Ref<symbol | null>> = Symbol('oa-plugin-open-record');

export function provideOpenRecord(): void {
  provide(OPEN_RECORD, ref<symbol | null>(null));
}

export function useOpenRecord(): Ref<symbol | null> {
  return inject(OPEN_RECORD, () => ref<symbol | null>(null), true);
}
