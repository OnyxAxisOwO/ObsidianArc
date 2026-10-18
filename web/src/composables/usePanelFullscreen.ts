/**
 * The fullscreen state of a panel, kept by the view that owns the panel. The panel
 * reports only the answer to a toggle, not a reactive flag, so the view keeps its
 * own copy for the icon and the label. Taking the panel as a getter lets the view
 * declare the ref in any order, since it is read only when the toggle is pressed.
 */
import { ref } from 'vue';

export function usePanelFullscreen(panel: () => { toggleFullscreen(): boolean } | null) {
  const fullscreen = ref(false);
  function toggle(): void {
    fullscreen.value = panel()?.toggleFullscreen() ?? false;
  }
  return { fullscreen, toggle };
}
