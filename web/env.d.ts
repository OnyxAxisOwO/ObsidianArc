/// <reference types="vite/client" />

// Vue's SFC compiler emits a component per .vue file; this is what tells
// TypeScript that importing one yields a component rather than nothing.
declare module '*.vue' {
  import type { DefineComponent } from 'vue';
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>;
  export default component;
}
