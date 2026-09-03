"use client";

import { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { getSession, clearSession } from "@/lib/tauri";
import {
  reportBreach,
  createStreamConnection,
  fetchDownloadUrl,
  fetchFileBytes,
  fetchStreamVersions,
  fetchVersionTree,
} from "@/lib/api";
import { canUploadEvidence, mimeToKind, parseFilePayload } from "@/lib/content";
import CaptureProtectionBadge from "@/components/CaptureProtectionBadge";
import BreachOverlay from "@/components/BreachOverlay";
import CountdownTimer from "@/components/ui/CountdownTimer";
import VersionTree from "@/components/ui/VersionTree";
import BranchDialog from "@/components/BranchDialog";
import MergeDialog from "@/components/MergeDialog";
import PrimaryButton from "@/components/ui/PrimaryButton";
import SecondaryButton from "@/components/ui/SecondaryButton";
import StatusBadge from "@/components/ui/StatusBadge";
import ContentTypeIcon from "@/components/ui/ContentTypeIcon";
import ContentGuard from "@/components/ContentGuard";
import PdfDoc from "@/components/PdfDoc";
import UploadEvidenceDialog from "@/components/UploadEvidenceDialog";
import type {
  DownloadURLResponse,
  StreamChunk,
  StreamChunkMeta,
  UploadDraft,
  UploadResponse,
  VersionTreeResponse,
  VersionTreeNode,
  BranchResponse,
  MergeResponse,
} from "@/lib/types";

export default function EvidenceViewer({
  ticketId,
  firNumber: firProp,
}: {
  ticketId: string;
  firNumber?: string;
}) {
  const router = useRouter();

  const [sessionId, setSessionId] = useState<string | null>(null);
  const [badge, setBadge] = useState("");
  const [role, setRole] = useState("");
  const [firNumber, setFirNumber] = useState("");
  const [expiresAt, setExpiresAt] = useState<Date | null>(null);
  const [remaining, setRemaining] = useState<number>(0);
  const [wsConnected, setWsConnected] = useState(false);
  const [chunks, setChunks] = useState<StreamChunk[]>([]);
  const [showBreach, setShowBreach] = useState(false);
  const [expired, setExpired] = useState(false);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [streamEpoch, setStreamEpoch] = useState(0);
  const [zoom, setZoom] = useState(100);
  const [brightness, setBrightness] = useState(100);
  const [rotated, setRotated] = useState(false);
  const [camState, setCamState] = useState<
    "unknown" | "requesting" | "on" | "denied" | "error"
  >("unknown");
  const [modelState, setModelState] = useState<
    "idle" | "loading" | "ready" | "failed"
  >("idle");
  // Version tree + selection for branching/merging.
  const [tree, setTree] = useState<VersionTreeNode[]>([]);
  const [treeLoading, setTreeLoading] = useState(false);
  const [treeError, setTreeError] = useState<string | null>(null);
  const [selectedNode, setSelectedNode] = useState<VersionTreeNode | null>(null);
  const [branchOpen, setBranchOpen] = useState(false);
  const [mergeOpen, setMergeOpen] = useState(false);

  const wsRef = useRef<WebSocket | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const detectionIntervalRef = useRef<ReturnType<typeof setInterval> | null>(
    null
  );
  const modelRef = useRef<any>(null);
  const streamRef = useRef<MediaStream | null>(null);

  const handleBreach = useCallback(
    async (reason: string) => {
      setShowBreach(true);
      try {
        await clearSession();
      } catch {
        /* best-effort */
      }
      try {
        if (sessionId && ticketId) {
          await reportBreach(sessionId, ticketId, badge, firNumber, reason);
        }
      } catch {
        /* network may be down — session already cleared locally */
      }
    },
    [sessionId, ticketId, badge, firNumber]
  );

  // Initialize session + stream (WebSocket with HTTP polling fallback for
  // webviews that cannot open a raw WebSocket to localhost).
  useEffect(() => {
    let mounted = true;
    let ws: WebSocket | null = null;
    let pollTimer: ReturnType<typeof setInterval> | null = null;
    let fallbackTimer: ReturnType<typeof setTimeout> | null = null;

    (async () => {
      const session = await getSession();
      if (!mounted) return;
      if (!session) {
        router.replace("/");
        return;
      }

      setSessionId(session.ticket_id);
      setBadge(sessionStorage.getItem("qdesk_badge") || "UNKNOWN");
      setRole(sessionStorage.getItem("qdesk_role") || "OFFICER");
      setFirNumber(
        firProp || new URLSearchParams(window.location.search).get("fir") || "UNKNOWN"
      );

      const storedExpiry = sessionStorage.getItem("qdesk_ticket_expires");
      if (storedExpiry) {
        setExpiresAt(new Date(storedExpiry));
      } else {
        const exp = new Date(Date.now() + 10 * 60 * 1000);
        setExpiresAt(exp);
        sessionStorage.setItem("qdesk_ticket_expires", exp.toISOString());
      }

      // A stream re-open (streamEpoch bump after an upload) re-pulls all
      // versions, so reset the inbox.
      if (streamEpoch > 0) setChunks([]);

      const applyChunks = (incoming: StreamChunk[]) => {
        if (!mounted) return;
        setChunks((prev) => {
          const seen = new Set(
            prev.map((c) => c.meta?.hash || `${c.payload}|${c.meta?.version}`)
          );
          const added = incoming.filter(
            (c) => !seen.has(c.meta?.hash || `${c.payload}|${c.meta?.version}`)
          );
          return added.length ? [...prev, ...added] : prev;
        });
      };

      // HTTP polling fallback: same chunks as the stream, over GET.
      const startPolling = () => {
        if (pollTimer) return;
        setWsConnected(true);
        const poll = async () => {
          try {
            const chunks = await fetchStreamVersions(session.ticket_id, ticketId);
            if (chunks) applyChunks(chunks);
          } catch {
            /* retry next tick */
          }
        };
        poll();
        pollTimer = setInterval(poll, 3000);
      };

      ws = createStreamConnection(ticketId);
      wsRef.current = ws;
      let wsGotChunk = false;

      ws.onopen = () => {
        if (mounted) setWsConnected(true);
      };

      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data);
          if (msg.type === "chunk" && msg.payload) {
            wsGotChunk = true;
            applyChunks([msg as StreamChunk]);
          }
        } catch {
          /* ignore malformed frames */
        }
      };

      ws.onclose = (ev) => {
        if (ev.code === 4008) {
          clearSession().catch(() => {});
          sessionStorage.removeItem("qdesk_ticket_expires");
          if (mounted) setExpired(true);
        }
        if (mounted && !pollTimer) setWsConnected(false);
      };

      ws.onerror = () => {
        // WebSocket failed — fall back to HTTP polling so evidence still loads.
        if (mounted && !pollTimer && ws && ws.readyState === WebSocket.CONNECTING) {
          ws.close();
        }
      };

      // Fall back to HTTP polling if the WebSocket delivers no data within 4s,
      // even if onopen fired (some webviews open the socket locally but never
      // receive frames from localhost). Polling is idempotent (chunk dedup) so
      // switching mid-stream — including an eventual live WS — is safe.
      fallbackTimer = setTimeout(() => {
        if (mounted && !pollTimer && !wsGotChunk) {
          startPolling();
        }
      }, 4000);
    })();

    return () => {
      mounted = false;
      if (fallbackTimer) clearTimeout(fallbackTimer);
      if (pollTimer) clearInterval(pollTimer);
      ws?.close();
    };
  }, [ticketId, router, streamEpoch]);

  // Countdown timer
  useEffect(() => {
    if (!expiresAt) return;
    const tick = () => {
      const ms = expiresAt.getTime() - Date.now();
      setRemaining(Math.max(0, Math.floor(ms / 1000)));
      if (ms <= 0) {
        clearSession().catch(() => {});
        sessionStorage.removeItem("qdesk_ticket_expires");
        setExpired(true);
      }
    };
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [expiresAt]);

  // Webcam + COCO-SSD detection
  useEffect(() => {
    let mounted = true;

    const startDetection = async () => {
      setCamState("requesting");
      let stream: MediaStream;
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          video: { width: 320, height: 240, facingMode: "user" },
        });
      } catch (err: any) {
        if (mounted) {
          console.error("getUserMedia failed:", err);
          setCamState(
            err?.name === "NotAllowedError" || err?.name === "SecurityError"
              ? "denied"
              : "error"
          );
        }
        return;
      }
      if (!mounted) {
        stream.getTracks().forEach((t) => t.stop());
        return;
      }
      streamRef.current = stream;
      setCamState("on");

      if (videoRef.current) {
        videoRef.current.srcObject = stream;
        videoRef.current.play().catch(() => {});
      }

      try {
        setModelState("loading");
        const [{ load: loadCoco }] = await Promise.all([
          import("@tensorflow-models/coco-ssd"),
          import("@tensorflow/tfjs"),
        ]);
        if (!mounted) return;
        modelRef.current = await loadCoco();
        setModelState("ready");

        detectionIntervalRef.current = setInterval(async () => {
          if (!modelRef.current || !videoRef.current || !mounted) return;
          if (videoRef.current.readyState < 2 || videoRef.current.paused)
            return;

          try {
            const predictions = await modelRef.current.detect(videoRef.current);
            for (const pred of predictions) {
              const cls = pred.class.toLowerCase();
              if (
                cls.includes("cell phone") ||
                cls.includes("cellphone") ||
                cls.includes("camera") ||
                cls.includes("remote")
              ) {
                handleBreach(`Device detected: ${pred.class}`);
                return;
              }
            }
          } catch {
            /* inference error — continue */
          }
        }, 200);
      } catch (err) {
        if (mounted) {
          console.error("detection model load failed:", err);
          setModelState("failed");
        }
      }
    };

    startDetection();

    const videoEl = videoRef.current;

    return () => {
      mounted = false;
      if (detectionIntervalRef.current)
        clearInterval(detectionIntervalRef.current);
      if (streamRef.current)
        streamRef.current.getTracks().forEach((t) => t.stop());
      if (videoEl) videoEl.srcObject = null;
      if (modelRef.current) {
        try {
          modelRef.current.dispose?.();
        } catch {
          /* noop */
        }
      }
    };
  }, [handleBreach]);

  const handleEndSession = async () => {
    wsRef.current?.close();
    try {
      await clearSession();
    } catch {
      /* best-effort */
    }
    sessionStorage.removeItem("qdesk_ticket_expires");
    router.push("/dashboard");
  };

  // ------------------------------------------------------------------
  // Derived evidence model: version tree, next-version chain inputs.
  // ------------------------------------------------------------------

  // The newest chunk (chunks stream in ascending order) supplies the parent
  // chain inputs for the next uploaded version.
  const lastMeta = useMemo(() => {
    const metas = chunks
      .map((c) => c.meta)
      .filter((m): m is StreamChunkMeta => !!m && typeof m.version === "number");
    return metas.length > 0 ? metas[metas.length - 1] : null;
  }, [chunks]);

  const uploadDraft: UploadDraft = {
    isNewDocument: false,
    documentId: lastMeta?.document_id,
    versionNumber: (lastMeta?.version || 0) + 1,
    treePath: `v${(lastMeta?.version || 0) + 1}`,
    parentSha256Hash: lastMeta?.hash,
  };

  // Build a lookup: version_id -> chunk meta, so a selected tree node can
  // resolve to the streamed chunk whose content we render in the canvas.
  const chunkByVersionId = useMemo(() => {
    const map = new Map<string, StreamChunk>();
    for (const c of chunks) {
      if (c.meta?.version_id) map.set(c.meta.version_id, c);
    }
    return map;
  }, [chunks]);

  // Load the server-resolved version tree once we know this FIR's document id
  // (from the first streamed chunk) and have an active session.
  const reloadTree = useCallback(() => {
    if (!sessionId || !firNumber || !lastMeta?.document_id) return;
    setTreeLoading(true);
    setTreeError(null);
    fetchVersionTree(sessionId, badge, lastMeta.document_id)
      .then((resp: VersionTreeResponse) => {
        // Default the selection to the newest leaf so the canvas opens on the
        // latest version rather than an empty panel.
        const leaves: VersionTreeNode[] = [];
        const walk = (nodes: VersionTreeNode[]) => {
          for (const n of nodes) {
            if (n.children.length === 0) leaves.push(n);
            walk(n.children);
          }
        };
        walk(resp.tree);
        let newest = leaves.sort(
          (a, b) =>
            new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
        )[0];
        if (!newest && resp.tree[0]) newest = resp.tree[0];
        setTree(resp.tree);
        setSelectedNode((prev) => prev || newest || null);
      })
      .catch((err: any) => {
        setTree([]);
        setTreeError(
          err?.message || "Failed to load the version tree for this document."
        );
      })
      .finally(() => setTreeLoading(false));
  }, [sessionId, badge, firNumber, lastMeta?.document_id]);

  useEffect(() => {
    reloadTree();
  }, [reloadTree, streamEpoch]);

  const handleSelectNode = (node: VersionTreeNode) => setSelectedNode(node);

  // All nodes flattened for lookups (parent map for the merge dialog's
  // default target, and the list of mainline candidates).
  const flatTree = useMemo(() => {
    const nodes: VersionTreeNode[] = [];
    const walk = (ns: VersionTreeNode[]) => {
      for (const n of ns) {
        nodes.push(n);
        walk(n.children);
      }
    };
    walk(tree);
    return nodes;
  }, [tree]);

  const parentOf = useMemo(() => {
    const map = new Map<string, VersionTreeNode>();
    const walk = (ns: VersionTreeNode[]) => {
      for (const n of ns) {
        for (const c of n.children) {
          map.set(c.id, n);
          walk(c.children);
        }
      }
    };
    walk(tree);
    return map;
  }, [tree]);

  // Mainline versions are depth-1 nodes (kind "mainline"); these are the valid
  // merge targets shown in the Merge dialog.
  const mainlineCandidates = useMemo(
    () => flatTree.filter((n) => n.kind === "mainline"),
    [flatTree]
  );

  const mergingBranch = selectedNode && selectedNode.kind === "branch" ? selectedNode : null;
  const mergeDefaultTargetId = mergingBranch
    ? (parentOf.get(mergingBranch.id)?.id ?? undefined)
    : undefined;

  const isBranchable = !!selectedNode && canUploadEvidence(role);

  const handleUploadSuccess = (result: UploadResponse) => {
    setUploadOpen(false);
    // Re-open the stream so the sidebar + inbox pick up the new version.
    setStreamEpoch((e) => e + 1);
    void result;
  };

  const handleBranchSuccess = (result: BranchResponse) => {
    setBranchOpen(false);
    setMergeOpen(false);
    // Reload the tree (the new branch node appears under its source).
    setSelectedNode(null);
    setStreamEpoch((e) => e + 1);
    void result;
  };

  const handleMergeSuccess = (result: MergeResponse) => {
    setMergeOpen(false);
    setBranchOpen(false);
    setSelectedNode(null);
    setStreamEpoch((e) => e + 1);
    void result;
  };

  if (showBreach) {
    return <BreachOverlay />;
  }

  if (expired) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <div className="max-w-md p-8 text-center">
          <div className="mx-auto mb-6 flex h-20 w-20 items-center justify-center rounded-full border border-danger/40 bg-danger/10">
            <svg
              className="h-10 w-10 text-danger"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M15.75 5.25a3 3 0 0 1 3 3m3 0a6 6 0 0 1-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1 1 21.75 8.25Z"
              />
            </svg>
          </div>
          <h2 className="text-2xl font-extrabold uppercase tracking-tight text-danger">
            Session Expired
          </h2>
          <p className="mt-3 text-sm text-slate-400">
            Your ticket has expired and evidence is no longer accessible. Please
            re-authenticate to continue.
          </p>
          <PrimaryButton
            onClick={() => {
              sessionStorage.removeItem("qdesk_ticket_expires");
              router.push("/");
            }}
            className="mt-6"
          >
            Go to Login
          </PrimaryButton>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col bg-slate-950">
      {/* Top bar */}
      <header className="relative flex items-center justify-between border-b border-slate-800 bg-slate-900/80 px-6 py-2.5">
        <div className="flex items-center gap-3">
          <CaptureProtectionBadge />
        </div>

        <div className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2">
          <CountdownTimer seconds={remaining} />
        </div>

        <div className="flex items-center gap-4">
          <StatusBadge
            tone={wsConnected ? "success" : "warning"}
            className="hidden md:inline-flex"
          >
            {wsConnected ? "Stream Live" : "Connecting"}
          </StatusBadge>
          <StatusBadge
            tone={
              camState === "on"
                ? "success"
                : camState === "requesting"
                  ? "warning"
                  : "danger"
            }
            className="hidden md:inline-flex"
          >
            Camera: {camState}
          </StatusBadge>
          <StatusBadge
            tone={
              modelState === "ready"
                ? "success"
                : modelState === "loading"
                  ? "warning"
                  : modelState === "failed"
                    ? "danger"
                    : "default"
            }
            className="hidden md:inline-flex"
          >
            Model: {modelState}
          </StatusBadge>
          <div className="border-l border-slate-800 pl-4 text-right">
            <p className="text-sm font-medium text-white">{badge}</p>
            <p className="text-xs text-slate-500">{role} &middot; {firNumber}</p>
          </div>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* Evidence workspace */}
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex items-center justify-between border-b border-slate-800 bg-slate-900/40 px-5 py-2">
            <div className="flex items-center gap-2">
              <svg className="h-4 w-4 text-slate-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5m-13.5-9L12 3m0 0 4.5 4.5M12 3v13.5" />
              </svg>
              <span className="font-mono text-sm text-slate-200">
                evidence_fir_{firNumber.replace(/\D/g, "") || "0000"}.stream
              </span>
              <span className="text-xs text-slate-500">
                &middot; {chunks.length} chunks received
              </span>
            </div>
            <span className="font-mono text-xs text-slate-500">
              {ticketId.slice(0, 8)}...
            </span>
          </div>

          <div className="relative flex-1 overflow-auto p-6">
            {chunks.length === 0 ? (
              <div className="flex h-full min-h-[420px] items-center justify-center">
                <div className="flex flex-col items-center gap-3 text-center">
                  <div className="h-10 w-10 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
                  <p className="text-sm font-medium text-slate-300">
                    {wsConnected
                      ? "Waiting for evidence data..."
                      : "Connecting to stream..."}
                  </p>
                  <p className="text-xs text-slate-500">Establishing secure stream...</p>
                </div>
              </div>
            ) : (
              <div className="flex items-start justify-center py-4">
                <div
                  className="w-full max-w-3xl space-y-3"
                  style={{
                    // Width-based zoom (no CSS transform) keeps the content in
                    // normal scroll flow so the mouse wheel scrolls it. A
                    // `transform: scale()` on an ancestor would otherwise break
                    // wheel scrolling of the nested PDF/evidence container.
                    width: `${zoom}%`,
                    transform: rotated ? "rotate(90deg)" : undefined,
                    filter: `brightness(${brightness / 100})`,
                  }}
                >
                  {(() => {
                    // Render only the selected version's content. The selected
                    // tree node maps back to the streamed chunk for that
                    // version id, so clicking a mainline or branch node loads
                    // exactly that version into the evidence canvas.
                    const selected =
                      selectedNode && chunkByVersionId.get(selectedNode.id);
                    if (!selected) {
                      return (
                        <div className="rounded border border-slate-800 bg-slate-900/40 p-6 text-center text-sm text-slate-500">
                          Select a version from the tree to view its evidence.
                        </div>
                      );
                    }
                    const fileMeta = parseFilePayload(selected.payload || "");
                    if (fileMeta && selected.meta?.version_id) {
                      return (
                        <EvidenceFileView
                          key={selected.meta.version_id}
                          sessionId={sessionId || ""}
                          ticketId={ticketId}
                          versionId={selected.meta.version_id}
                          firNumber={firNumber}
                        />
                      );
                    }
                    return (
                      <div>
                        <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                          <span className="font-bold text-blue-300">
                            {selectedNode?.tree_path}
                          </span>
                          <span>by {selectedNode?.badge_number}</span>
                          {selectedNode?.created_at && (
                            <span>
                              {new Date(selectedNode.created_at).toLocaleString()}
                            </span>
                          )}
                          {selectedNode?.kind === "merge" && (
                            <StatusBadge tone="success">merged</StatusBadge>
                          )}
                        </div>
                        <div className="rounded border border-slate-800 bg-slate-900/60 p-3 font-mono text-xs leading-relaxed text-slate-300">
                          {selected.payload}
                        </div>
                      </div>
                    );
                  })()}
                </div>
              </div>
            )}

            {/* View toolbar — in normal flow so it scrolls with the content */}
            <div className="mt-3 inline-flex items-center rounded border border-slate-700 bg-slate-900/95 p-1">
              <button
                onClick={() => setZoom((z) => Math.min(200, z + 10))}
                className="rounded-sm p-2 text-slate-300 transition-colors hover:bg-slate-800 hover:text-white"
                aria-label="Zoom in"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-4.35-4.35M11 8a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm9 3a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM11 8v3m-1.5-1.5h3" />
                </svg>
              </button>
              <button
                onClick={() => setZoom((z) => Math.max(40, z - 10))}
                className="rounded-sm p-2 text-slate-300 transition-colors hover:bg-slate-800 hover:text-white"
                aria-label="Zoom out"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-4.35-4.35M11 8a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm9 3a9 9 0 1 1-18 0 9 9 0 0 1 18 0ZM9.5 11h3" />
                </svg>
              </button>
              <span className="min-w-[48px] px-2 text-center text-xs tabular-nums text-slate-400">{zoom}%</span>
              <button
                onClick={() => setRotated((r) => !r)}
                className="rounded-sm p-2 text-slate-300 transition-colors hover:bg-slate-800 hover:text-white"
                aria-label="Rotate"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0 3.181 3.183a8.25 8.25 0 0 0 13.803-3.7M4.031 9.865a8.25 8.25 0 0 1 13.803-3.7l3.181 3.182m0-4.991v4.99" />
                </svg>
              </button>
              <button
                onClick={() => setBrightness((b) => Math.min(150, b + 10))}
                className="rounded-sm p-2 text-slate-300 transition-colors hover:bg-slate-800 hover:text-white"
                aria-label="Increase brightness"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386-1.591 1.591M21 12h-2.25m-.386 6.364-1.591-1.591M12 18.75V21m-4.773-4.227-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 1 1-7.5 0 3.75 3.75 0 0 1 7.5 0Z" />
                </svg>
              </button>
              <button
                onClick={() => setBrightness((b) => Math.max(50, b - 10))}
                className="rounded-sm p-2 text-slate-300 transition-colors hover:bg-slate-800 hover:text-white"
                aria-label="Decrease brightness"
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.72 9.72 0 0 1 18 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 0 0 3 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 0 0 9.002-5.998Z" />
                </svg>
              </button>
            </div>
          </div>
        </div>

        {/* Sidebar */}
        <aside className="flex w-80 shrink-0 flex-col border-l border-slate-800 bg-slate-900/40">
          <div className="section-head">
            <span className="section-title">Case Actions</span>
          </div>
          <div className="flex-1 overflow-auto p-5">
            <div className="mb-3 flex items-center justify-between">
              <span className="field-label mb-0">Version Tree</span>
              {treeLoading && (
                <span className="text-[11px] text-slate-500">loading…</span>
              )}
            </div>
            {treeError ? (
              <div className="rounded border border-red-700/60 bg-red-950/40 px-3 py-2 text-[11px] leading-relaxed text-red-300">
                {treeError}
              </div>
            ) : (
              <VersionTree
                tree={tree}
                selectedId={selectedNode?.id}
                onSelect={handleSelectNode}
              />
            )}

            {/* Branch / merge actions for the selected node */}
            {selectedNode && canUploadEvidence(role) && (
              <div className="mt-4 rounded border border-slate-800 bg-slate-900/60 p-3">
                <p className="mb-2 text-[11px] uppercase tracking-wide text-slate-500">
                  Selected {selectedNode.tree_path}
                </p>
                <div className="space-y-2">
                  <PrimaryButton
                    onClick={() => setBranchOpen(true)}
                    className="w-full text-xs"
                    title={
                      selectedNode.kind === "mainline"
                        ? "Create a branch off this version for a parallel annotation or file"
                        : "Create a sub-branch off this version"
                    }
                  >
                    Branch for Annotation
                  </PrimaryButton>
                  {selectedNode.kind === "branch" && (
                    <SecondaryButton
                      onClick={() => setMergeOpen(true)}
                      className="w-full text-xs"
                      title="Merge this branch back into a mainline version"
                    >
                      Merge into Mainline
                    </SecondaryButton>
                  )}
                </div>
              </div>
            )}
          </div>
          <div className="space-y-2 border-t border-slate-800 p-4">
            {canUploadEvidence(role) ? (
              <PrimaryButton
                onClick={() => setUploadOpen(true)}
                disabled={!lastMeta?.document_id}
                className="w-full text-sm"
                title={
                  lastMeta?.document_id
                    ? "Upload a new evidence version, hash-chained to the current one"
                    : "No document loaded yet on this stream"
                }
              >
                Upload New Evidence Version
              </PrimaryButton>
            ) : (
              <SecondaryButton disabled className="w-full text-sm">
                Uploads restricted for {role}
              </SecondaryButton>
            )}
            <SecondaryButton onClick={handleEndSession} danger className="w-full text-sm">
              End Session
            </SecondaryButton>
          </div>
        </aside>
      </div>

      <video ref={videoRef} className="hidden" playsInline muted autoPlay />
      <canvas ref={canvasRef} className="hidden" width={320} height={240} />

      <UploadEvidenceDialog
        open={uploadOpen}
        onClose={() => setUploadOpen(false)}
        sessionId={sessionId}
        badge={badge}
        firNumber={firNumber}
        draft={uploadDraft}
        onSuccess={handleUploadSuccess}
      />

      <BranchDialog
        open={branchOpen}
        onClose={() => setBranchOpen(false)}
        sessionId={sessionId}
        badge={badge}
        firNumber={firNumber}
        source={selectedNode}
        onSuccess={handleBranchSuccess}
      />

      <MergeDialog
        open={mergeOpen && !!mergingBranch}
        onClose={() => setMergeOpen(false)}
        sessionId={sessionId}
        badge={badge}
        firNumber={firNumber}
        branch={mergingBranch}
        mainlineCandidates={mainlineCandidates}
        defaultTargetId={mergeDefaultTargetId}
        onSuccess={handleMergeSuccess}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// EvidenceFileView — renders one file-type chunk through a 60-second signed
// storage URL. Shows a skeleton while the URL is being issued.
// ---------------------------------------------------------------------------

function EvidenceFileView({
  sessionId,
  ticketId,
  versionId,
  firNumber,
}: {
  sessionId: string;
  ticketId: string;
  versionId: string;
  firNumber: string;
}) {
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [res, setRes] = useState<DownloadURLResponse | null>(null);
  const [pdfData, setPdfData] = useState<ArrayBuffer | null>(null);
  const [pdfError, setPdfError] = useState(false);

  useEffect(() => {
    let mounted = true;
    fetchDownloadUrl(sessionId, ticketId, versionId)
      .then(async (r) => {
        if (!mounted) return;
        setRes(r);
        // Fetch the PDF bytes and render them page-by-page into the host DOM via
        // pdf.js. This keeps the document in the same origin/context as the
        // guard so right-click, print and save shortcuts can be blocked, while
        // scrolling remains native.
        if (r.mime_type === "application/pdf") {
          try {
            const blob = await fetchFileBytes(r.url);
            if (!mounted) return;
            setPdfData(await blob.arrayBuffer());
          } catch {
            if (mounted) setPdfError(true);
          }
        }
        if (mounted) setState("ready");
      })
      .catch(() => {
        if (mounted) setState("error");
      });
    return () => {
      mounted = false;
    };
  }, [sessionId, ticketId, versionId]);

  if (state === "loading") {
    return (
      <div className="flex h-56 animate-pulse items-center justify-center rounded border border-slate-800 bg-slate-900/60">
        <div className="flex flex-col items-center gap-2">
          <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
          <span className="text-xs text-slate-500">
            Requesting signed access URL…
          </span>
        </div>
      </div>
    );
  }

  if (state === "error" || !res) {
    return (
      <div className="rounded border border-red-700/60 bg-red-950/40 px-4 py-3 text-sm text-red-300">
        Could not open this evidence file. The access ticket may have expired —
        reopen the case to mint a fresh one.
      </div>
    );
  }

  const kind = mimeToKind(res.mime_type);
  const frameCls =
    "w-full overflow-hidden rounded border border-slate-800 bg-slate-900/60";
  // Hide the embedded PDF viewer's toolbar/nav side pane (where the print and
  // PDFs are rendered page-by-page into the host DOM with pdf.js (see PdfDoc);
  // there is no PDF iframe or toolbar, so controls/print/download can't be
  // reached and scrolling is native.
  const isPdf = res.mime_type === "application/pdf";
  const pdfLoading = isPdf && !pdfData && !pdfError;
  const metaLine = (
    <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 px-3 py-2">
      <div className="flex items-center gap-2 text-xs">
        <ContentTypeIcon mime={res.mime_type} />
        <span className="truncate font-mono text-slate-300">{res.filename}</span>
      </div>
      <span className="text-[11px] tabular-nums text-slate-500">
        v{res.version_number} &middot; {firNumber} &middot;{" "}
        {(res.size_bytes / 1024).toFixed(1)} KB
      </span>
    </div>
  );

  return (
    <div className={frameCls}>
      {metaLine}
      <ContentGuard className="w-full" capture={false}>
        <div className="flex items-center justify-center bg-black/40">
          {kind === "image" && (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={res.url}
              alt={res.filename}
              className="max-h-[560px] w-auto max-w-full object-contain"
              draggable={false}
            />
          )}
          {kind === "pdf" && pdfLoading && (
            <div className="flex h-[560px] w-full items-center justify-center">
              <div className="flex flex-col items-center gap-2">
                <div className="h-8 w-8 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
                <span className="text-xs text-slate-500">Preparing document…</span>
              </div>
            </div>
          )}
          {kind === "pdf" && pdfError && (
            <p className="w-full p-6 text-center text-sm text-amber-300">
              This document could not be prepared for protected viewing.
            </p>
          )}
          {kind === "pdf" && pdfData && !pdfError && (
            <PdfDoc data={pdfData} className="w-full bg-slate-950/70" />
          )}
          {kind === "video" && (
            <video
              src={res.url}
              controls
              className="max-h-[560px] w-full"
              playsInline
              controlsList="nodownload noremoteplayback"
              disablePictureInPicture
            />
          )}
          {kind === "audio" && (
            <div className="w-full p-5">
              <audio
                src={res.url}
                controls
                className="w-full"
                controlsList="nodownload"
              />
            </div>
          )}
          {(kind === "unknown" || kind === "text") && (
            <p className="p-6 text-center text-sm text-slate-500">
              Cannot preview <span className="font-mono text-slate-300">{res.mime_type}</span>{" "}
              inline. The signed URL is valid for {res.expires_seconds}s.
            </p>
          )}
        </div>
      </ContentGuard>
    </div>
  );
}
