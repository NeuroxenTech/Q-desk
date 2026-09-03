"use client";

import { useEffect, useRef, type ReactNode, type WheelEvent } from "react";

// ContentGuard wraps evidence media with print/download deterrence:
//   - hide the embedded PDF viewer toolbar (print/download buttons)
//   - block right-click, drag, text selection
//   - block the common PDF/print/save keyboard shortcuts
//   - lay a pointer-capturing shim over the media frame so right-clicks and
//     toolbar clicks can't reach the PDF viewer, while forwarding wheel input to
//     the (same-origin blob) iframe so scrolling still works.
//
// This is deterrence on the client, not a hard DRM boundary: the backend still
// holds the source bytes. It blocks casual print/download attempts, which is
// the intended threat model for an officer workstation.

const BLOCK_KEYS: Record<string, boolean> = {
  p: true, // Ctrl+P / Ctrl+Shift+P
  s: true, // Ctrl+S / Ctrl+Shift+S
  o: true, // Ctrl+O
  w: true, // Ctrl+W
  u: true, // Ctrl+U (view source, also open-with)
  j: true, // Ctrl+J / Ctrl+Shift+J (devtools)
  i: true, // Ctrl+Shift+I (devtools)
  c: true, // Ctrl+Shift+C (inspect)
};

interface ContentGuardProps {
  children: ReactNode;
  className?: string;
  // When true (PDF), lay a pointer-capturing shim over the media so right-clicks
  // and toolbar clicks can't reach the PDF viewer. Interactive media (video,
  // audio, images) should leave this false so their controls stay clickable.
  capture?: boolean;
}

export default function ContentGuard({
  children,
  className = "",
  capture = false,
}: ContentGuardProps) {
  const shimRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      const key = e.key.toLowerCase();
      if (key === "p" || key === "s" || BLOCK_KEYS[key]) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    const onContextMenu = (e: Event) => {
      const t = e.target as HTMLElement | null;
      if (t?.closest("[data-guard-allow]")) return;
      e.preventDefault();
    };
    const onBeforePrint = (e: Event) => e.preventDefault();
    const onDragStart = (e: Event) => e.preventDefault();
    const onSelectStart = (e: Event) => e.preventDefault();

    document.addEventListener("keydown", onKeyDown, true);
    document.addEventListener("contextmenu", onContextMenu, true);
    window.addEventListener("beforeprint", onBeforePrint);
    document.addEventListener("dragstart", onDragStart, true);
    document.addEventListener("selectstart", onSelectStart, true);

    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.removeEventListener("contextmenu", onContextMenu, true);
      window.removeEventListener("beforeprint", onBeforePrint);
      document.removeEventListener("dragstart", onDragStart, true);
      document.removeEventListener("selectstart", onSelectStart, true);
    };
  }, []);

  // Forward mouse-wheel input to the inner PDF frame (a same-origin blob
  // iframe) so the shim — which exists to swallow right-clicks — doesn't stop
  // scrolling. The iframe's own document is accessible because it's a
  // same-origin blob: URL.
  const shimWheel = (e: WheelEvent<HTMLDivElement>) => {
    const frame = shimRef.current?.querySelector(
      "iframe"
    ) as HTMLIFrameElement | null;
    if (frame?.contentWindow) {
      frame.contentWindow.scrollBy(0, e.deltaY);
      frame.contentWindow.scrollBy(e.deltaX, 0);
      e.preventDefault();
    }
  };

  return (
    <div
      className={`relative select-none ${className}`}
      draggable={false}
      onContextMenu={(e) => e.preventDefault()}
    >
      <div className="pointer-events-none h-full w-full">{children}</div>
      {/* Pointer-capturing shim: blocks right-click and toolbar clicks over the
          PDF. Wheel is forwarded to the frame so scroll still works. Only
          rendered for capture media (PDF). */}
      {capture && (
        <div
          ref={shimRef}
          className="pointer-events-auto absolute inset-0 z-10 cursor-default"
          data-guard-shim="true"
          onContextMenu={(e) => e.preventDefault()}
          onWheel={shimWheel}
        />
      )}
    </div>
  );
}
