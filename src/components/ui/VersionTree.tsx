"use client";

import ContentTypeIcon from "@/components/ui/ContentTypeIcon";
import type { VersionTreeNode } from "@/lib/types";
import { truncateDigest } from "@/lib/content";

interface VersionTreeProps {
  tree: VersionTreeNode[];
  selectedId?: string;
  onSelect: (node: VersionTreeNode) => void;
}

// VersionTree renders a document's version history as a tree (indentation +
// connecting lines) derived from the server-resolved ltree tree_path. Mainline
// versions are shown at depth 1, branches are indented beneath their parent,
// and merge nodes carry a distinct 'merged' badge plus both parent hashes.
export default function VersionTree({
  tree,
  selectedId,
  onSelect,
}: VersionTreeProps) {
  if (tree.length === 0) {
    return (
      <div className="rounded border border-slate-800 bg-slate-900/50 p-4 text-center text-xs text-slate-500">
        No version tree loaded.
      </div>
    );
  }

  const renderNode = (node: VersionTreeNode, depth: number) => {
    const isSelected = node.id === selectedId;
    return (
      <li key={node.id} className="relative">
        <button
          onClick={() => onSelect(node)}
          className={`group w-full rounded border text-left transition-colors ${
            isSelected
              ? "border-blue-500/60 bg-blue-500/10"
              : "border-transparent hover:border-slate-700 hover:bg-slate-800/40"
          } ${depth > 0 ? "pl-6" : ""}`}
        >
          {/* indentation + connecting line */}
          {depth > 0 && (
            <span className="absolute left-0 top-0 h-full w-4 border-l border-slate-700" />
          )}
          <div className="px-2 py-1.5">
            <div className="flex items-center gap-1.5">
              <VersionBadge node={node} />
              <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400">
                {versionLabel(node.tree_path)}
              </span>
              {node.content_type && (
                <ContentTypeIcon mime={node.content_type} />
              )}
            </div>
            <div className="mt-0.5 flex items-center gap-2">
              <span className="font-mono text-[11px] text-slate-400">
                {truncateDigest(node.sha256_hash, 10)}
              </span>
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-slate-500">
              <span>by {node.badge_number}</span>
              <span>&middot;</span>
              <span>{new Date(node.created_at).toLocaleString()}</span>
            </div>
            {node.kind === "merge" && (
              <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-[10px] text-slate-500">
                <span title={node.parent_sha256_hash}>
                  parent {truncateDigest(node.parent_sha256_hash, 8)}
                </span>
                <span title={node.merged_from_hash} className="text-emerald-400/80">
                  + merged {truncateDigest(node.merged_from_hash, 8)}
                </span>
              </div>
            )}
            {node.kind !== "merge" && node.parent_sha256_hash && (
              <div
                className="mt-1 font-mono text-[10px] text-slate-600"
                title={node.parent_sha256_hash}
              >
                ← {truncateDigest(node.parent_sha256_hash, 10)}
              </div>
            )}
          </div>
        </button>
        {node.children.length > 0 && (
          <ul className="relative ml-4 space-y-1 border-l border-slate-700 pl-2 pt-1">
            {node.children.map((c) => renderNode(c, depth + 1))}
          </ul>
        )}
      </li>
    );
  };

  return (
    <ul className="space-y-1">
      {tree.map((n) => renderNode(n, 0))}
    </ul>
  );
}

function VersionBadge({ node }: { node: VersionTreeNode }) {
  if (node.kind === "merge") {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-emerald-500/15 px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wide text-emerald-300">
        merged
      </span>
    );
  }
  if (node.kind === "branch") {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-blue-500/15 px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wide text-blue-300">
        <svg
          className="h-2.5 w-2.5"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2.5}
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M7.217 10.907a2.25 2.25 0 1 0 0 2.186m0-2.186c.18.324.283.696.283 1.093s-.103.77-.283 1.093m0-2.186 9.566-3.642m-9.566 7.828 9.566 3.642m-9.566-3.642 4.277 1.71M18 8.25a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm0 12a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Z"
          />
        </svg>
        branch
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded bg-slate-500/15 px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wide text-slate-300">
      v
    </span>
  );
}

// versionLabel maps a tree_path to the UI label — the ltree path itself, so a
// mainline is "v2" and a branch off it is "v2.1".
function versionLabel(treePath: string): string {
  return treePath || "?";
}
