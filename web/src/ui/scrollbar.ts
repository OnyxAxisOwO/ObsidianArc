// Overlay scrollbar.
//
// Windows Chromium takes 15-17px for native scrollbars which shrinks the
// layout and causes horizontal jitter when switching tabs between short and
// long content. This overlay scrollbar floats inside the element padding,
// keeping clientWidth 100% stable while providing a fully draggable,
// visible scrollbar without consuming layout space.

export interface OverlayScrollbarHandle {
  update: () => void;
  destroy: () => void;
  setScrollElement: (nextScrollEl: HTMLElement) => void;
}

export function attachOverlayScrollbar(
  initialScrollEl: HTMLElement,
  containerEl: HTMLElement = initialScrollEl.parentElement || initialScrollEl,
): OverlayScrollbarHandle {
  let scrollEl = initialScrollEl;

  const track = document.createElement('div');
  track.className = 'oa-overlay-track';

  const thumb = document.createElement('div');
  thumb.className = 'oa-overlay-thumb';
  track.appendChild(thumb);
  containerEl.appendChild(track);

  let isDragging = false;
  let startY = 0;
  let startScrollTop = 0;
  let scrollTimer = 0;

  function update(): void {
    const clientHeight = scrollEl.clientHeight;
    const scrollHeight = scrollEl.scrollHeight;
    const scrollTop = scrollEl.scrollTop;

    if (clientHeight <= 0 || scrollHeight <= clientHeight + 1) {
      track.style.display = 'none';
      return;
    }

    track.style.display = 'block';
    const trackHeight = track.clientHeight || clientHeight;
    const thumbHeight = Math.max(28, Math.round((clientHeight / scrollHeight) * trackHeight));
    const maxScroll = scrollHeight - clientHeight;
    const ratio = maxScroll > 0 ? scrollTop / maxScroll : 0;
    const top = Math.round(ratio * (trackHeight - thumbHeight));

    thumb.style.height = `${thumbHeight}px`;
    thumb.style.transform = `translateY(${top}px)`;
  }

  function onScroll(): void {
    update();
    track.classList.add('scrolling');
    window.clearTimeout(scrollTimer);
    scrollTimer = window.setTimeout(() => {
      track.classList.remove('scrolling');
    }, 800);
  }

  function onThumbMouseDown(e: MouseEvent): void {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();

    isDragging = true;
    startY = e.clientY;
    startScrollTop = scrollEl.scrollTop;

    track.classList.add('dragging');
    document.body.style.userSelect = 'none';

    window.addEventListener('mousemove', onMouseMove, { capture: true });
    window.addEventListener('mouseup', onMouseUp, { capture: true });
  }

  function onMouseMove(e: MouseEvent): void {
    if (!isDragging) return;
    e.preventDefault();

    const clientHeight = scrollEl.clientHeight;
    const scrollHeight = scrollEl.scrollHeight;
    const trackHeight = track.clientHeight || clientHeight;
    const thumbHeight = Math.max(28, Math.round((clientHeight / scrollHeight) * trackHeight));

    const deltaY = e.clientY - startY;
    const maxTrackTravel = trackHeight - thumbHeight;
    const maxScroll = scrollHeight - clientHeight;

    if (maxTrackTravel > 0) {
      const scrollDelta = (deltaY / maxTrackTravel) * maxScroll;
      scrollEl.scrollTop = startScrollTop + scrollDelta;
    }
  }

  function onMouseUp(): void {
    if (!isDragging) return;
    isDragging = false;
    track.classList.remove('dragging');
    document.body.style.userSelect = '';

    window.removeEventListener('mousemove', onMouseMove, { capture: true });
    window.removeEventListener('mouseup', onMouseUp, { capture: true });
  }

  function onTrackMouseDown(e: MouseEvent): void {
    if (e.target === thumb || e.button !== 0) return;
    e.preventDefault();

    const rect = track.getBoundingClientRect();
    const clickY = e.clientY - rect.top;
    const clientHeight = scrollEl.clientHeight;
    const scrollHeight = scrollEl.scrollHeight;
    const trackHeight = track.clientHeight || clientHeight;
    const thumbHeight = Math.max(28, Math.round((clientHeight / scrollHeight) * trackHeight));

    const targetRatio = (clickY - thumbHeight / 2) / (trackHeight - thumbHeight);
    const clampedRatio = Math.max(0, Math.min(1, targetRatio));
    scrollEl.scrollTop = clampedRatio * (scrollHeight - clientHeight);
  }

  thumb.addEventListener('mousedown', onThumbMouseDown);
  track.addEventListener('mousedown', onTrackMouseDown);
  scrollEl.addEventListener('scroll', onScroll, { passive: true });

  const resizeObserver = typeof ResizeObserver !== 'undefined'
    ? new ResizeObserver(() => update())
    : null;

  const mutationObserver = typeof MutationObserver !== 'undefined'
    ? new MutationObserver(() => {
        update();
        if (resizeObserver && scrollEl.firstElementChild) {
          resizeObserver.observe(scrollEl.firstElementChild);
        }
      })
    : null;

  function observeTarget(target: HTMLElement): void {
    resizeObserver?.observe(target);
    if (target.firstElementChild) {
      resizeObserver?.observe(target.firstElementChild);
    }
    mutationObserver?.observe(target, { childList: true, subtree: true, attributes: true });
  }

  observeTarget(scrollEl);
  requestAnimationFrame(() => update());

  return {
    update,
    setScrollElement(nextScrollEl: HTMLElement): void {
      if (nextScrollEl === scrollEl) {
        update();
        return;
      }
      scrollEl.removeEventListener('scroll', onScroll);
      resizeObserver?.unobserve(scrollEl);
      mutationObserver?.disconnect();

      scrollEl = nextScrollEl;
      scrollEl.addEventListener('scroll', onScroll, { passive: true });
      observeTarget(scrollEl);
      update();
    },
    destroy(): void {
      window.clearTimeout(scrollTimer);
      thumb.removeEventListener('mousedown', onThumbMouseDown);
      track.removeEventListener('mousedown', onTrackMouseDown);
      scrollEl.removeEventListener('scroll', onScroll);
      resizeObserver?.disconnect();
      mutationObserver?.disconnect();
      track.remove();
    },
  };
}
