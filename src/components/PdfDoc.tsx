"use client";

import { useEffect, useRef, useState } from "react";
import * as pdfjsLib from "pdfjs-dist";

// PdfDoc renders a PDF's pages onto canvases in the host DOM. Because every
// page is a plain <canvas> in the parent document (no <iframe>, no native PDF
// plugin), scrolling is native (wheel/scrollbar) while the parent guard
// (ContentGuard in viewer.tsx) blocks right-click, print and save shortcuts.
// There is no PDF toolbar at all, so there are no built-in download/print
// buttons to reach.

interface PdfDocProps {
  data: ArrayBuffer;
  className?: string;
}

export default function PdfDoc({ data, className = "" }: PdfDocProps) {
  const [, setTick] = useState(0);
  const [error, setError] = useState(false);
  const [errorText, setErrorText] = useState("");
  const [scale, setScale] = useState(1);
  const mountRef = useRef<HTMLDivElement | null>(null);
  const canvasLayer = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    let task: pdfjsLib.PDFDocumentLoadingTask | null = null;
    const layerEl = canvasLayer.current;

    (async () => {
      try {
        const mod = await import("pdfjs-dist");
        // The worker is copied into /public and served by Next so pdf.js can
        // fetch it without a CDN. Using a plain URL avoids the `?url` loader,
        // which returns a non-string in Next 14 and broke workerSrc.
        mod.GlobalWorkerOptions.workerSrc = "/pdf.worker.min.mjs";

        task = mod.getDocument({ data: data.slice(0) });
        const doc = await task.promise;
        if (cancelled) return;

        const dpr = Math.min(window.devicePixelRatio || 1, 2);
        for (let i = 1; i <= doc.numPages; i++) {
          if (cancelled) return;
          const page = await doc.getPage(i);
          const vp = page.getViewport({ scale: scale * dpr });
          const canvas = document.createElement("canvas");
          canvas.width = Math.floor(vp.width);
          canvas.height = Math.floor(vp.height);
          canvas.style.width = `${Math.floor(vp.width / dpr)}px`;
          canvas.style.height = `${Math.floor(vp.height / dpr)}px`;
          canvas.style.pointerEvents = "none";
          canvas.className = "mb-3 block shadow-md";
          await page.render({ canvas, viewport: vp }).promise;
          if (cancelled) return;
          layerEl?.appendChild(canvas);
          setTick((t) => t + 1);
        }
      } catch (err) {
        if (!cancelled) {
          setErrorText(err instanceof Error ? err.message : String(err));
          setError(true);
        }
      }
    })();

    return () => {
      cancelled = true;
      task?.destroy();
      if (layerEl) {
        layerEl.innerHTML = "";
      }
    };
    // Re-render only when zoom changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scale]);

  if (error) {
    return (
      <p className="w-full p-6 text-center text-sm text-amber-300">
        This document could not be prepared for protected viewing.
        {errorText ? <span className="block text-xs text-rose-400">{errorText}</span> : null}
      </p>
    );
  }

  return (
    <div
      ref={mountRef}
      className={`block w-full ${className}`}
      onContextMenu={(e) => e.preventDefault()}
      onDragStart={(e) => e.preventDefault()}
    >
      <div ref={canvasLayer} className="flex flex-col items-center" />
    </div>
  );
}
