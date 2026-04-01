import { useState, useCallback } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Circle, Square, ChevronDown, ChevronRight, Terminal, Wifi, WifiOff,
} from "lucide-react";
import { useWorkerStream, type WorkerStreamEvent } from "@/hooks/use-worker-stream";
import { useWs } from "@/hooks/use-ws";
import { useAutoScroll } from "@/hooks/use-auto-scroll";
import { Methods } from "@/api/protocol";
import { useTranslation } from "react-i18next";

/* ── Status indicator ─────────────────────────────────────────── */

const STATUS_CONFIG = {
  connecting: { color: "text-yellow-500", pulse: true, label: "Connecting" },
  connected: { color: "text-green-500", pulse: true, label: "Streaming" },
  completed: { color: "text-blue-500", pulse: false, label: "Completed" },
  error: { color: "text-red-500", pulse: false, label: "Error" },
  disconnected: { color: "text-zinc-500", pulse: false, label: "Disconnected" },
} as const;

function StatusDot({ status }: { status: keyof typeof STATUS_CONFIG }) {
  const cfg = STATUS_CONFIG[status];
  return (
    <Circle
      className={`h-2.5 w-2.5 fill-current ${cfg.color} ${cfg.pulse ? "animate-pulse" : ""}`}
    />
  );
}

/* ── Tool call card ───────────────────────────────────────────── */

function ToolCallCard({ toolName, toolInput }: { toolName: string; toolInput?: string }) {
  const [open, setOpen] = useState(false);
  const truncated = toolInput && toolInput.length > 100
    ? toolInput.slice(0, 100) + "…"
    : toolInput;

  return (
    <div className="my-1.5 rounded border border-zinc-700 bg-zinc-900">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-1.5 text-xs font-mono text-cyan-400 hover:bg-zinc-800 transition-colors"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        <Terminal className="h-3 w-3" />
        <span>{toolName}</span>
      </button>
      {open && truncated && (
        <div className="border-t border-zinc-700 px-3 py-2 text-xs font-mono text-zinc-400 whitespace-pre-wrap break-all">
          {truncated}
        </div>
      )}
    </div>
  );
}

/* ── Elapsed time formatter ───────────────────────────────────── */

function formatElapsed(startedAt: number): string {
  const ms = Date.now() - startedAt;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return `${m}m ${rem}s`;
}

function formatCost(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(4)}`;
  return `$${usd.toFixed(2)}`;
}

/* ── Main panel ───────────────────────────────────────────────── */

interface WorkerStreamPanelProps {
  taskId: string;
  teamId: string;
}

export default function WorkerStreamPanel({ taskId, teamId }: WorkerStreamPanelProps) {
  const session = useWorkerStream(taskId);
  const ws = useWs();
  const { t } = useTranslation("teams");
  const [interrupting, setInterrupting] = useState(false);

  const eventCount = session?.events.length ?? 0;
  const { ref, onScroll } = useAutoScroll<HTMLDivElement>(
    [eventCount],
  );

  const handleInterrupt = useCallback(async () => {
    setInterrupting(true);
    try {
      await ws.call(Methods.TEAMS_TASK_COMMENT, {
        team_id: teamId,
        task_id: taskId,
        content: "[interrupt_worker]",
      });
    } catch {
      // ignore — toast handled upstream
    } finally {
      setInterrupting(false);
    }
  }, [ws, teamId, taskId]);

  const status = session?.status ?? "connecting";
  const isActive = status === "connecting" || status === "connected";

  // Elapsed timer — re-render every second while active
  const [, setTick] = useState(0);
  // useEffect for timer is fine here since it only runs while streaming
  useState(() => {
    if (!isActive) return;
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  });

  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950 overflow-hidden">
      {/* Status bar */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-zinc-800 bg-zinc-900/50">
        <StatusDot status={status} />
        <span className="text-xs font-medium text-zinc-300">
          {t("tasks.worker.title", { defaultValue: "Worker Stream" })}
        </span>

        {session?.model && (
          <Badge variant="outline" className="text-[10px] px-1.5 py-0 border-zinc-700 text-zinc-400">
            {session.model}
          </Badge>
        )}

        <div className="ml-auto flex items-center gap-3 text-[11px] text-zinc-500">
          {session && (
            <>
              <span>{formatCost(session.costUSD)}</span>
              <span>{session.numTurns}t</span>
              <span>{formatElapsed(session.startedAt)}</span>
            </>
          )}
          {isActive && (
            <Button
              variant="destructive"
              size="sm"
              className="h-6 px-2 text-[11px]"
              onClick={handleInterrupt}
              disabled={interrupting}
            >
              <Square className="mr-1 h-2.5 w-2.5" />
              {t("tasks.worker.stop", { defaultValue: "Stop" })}
            </Button>
          )}
        </div>
      </div>

      {/* Stream body */}
      <div
        ref={ref}
        onScroll={onScroll}
        className="max-h-80 overflow-y-auto overscroll-contain p-3 font-mono text-xs leading-relaxed text-zinc-300"
      >
        {(!session || session.events.length === 0) && (
          <div className="flex items-center gap-2 text-zinc-500">
            <Wifi className="h-3.5 w-3.5 animate-pulse" />
            <span>{t("tasks.worker.waiting", { defaultValue: "Waiting for worker stream…" })}</span>
          </div>
        )}

        {session?.events.map((evt, i) => (
          <StreamEventLine key={i} event={evt} />
        ))}

        {status === "disconnected" && (
          <div className="flex items-center gap-2 mt-2 text-zinc-500">
            <WifiOff className="h-3.5 w-3.5" />
            <span>{t("tasks.worker.disconnected", { defaultValue: "Worker disconnected" })}</span>
          </div>
        )}
      </div>
    </div>
  );
}

/* ── Individual event line ────────────────────────────────────── */

function StreamEventLine({ event }: { event: WorkerStreamEvent }) {
  switch (event.type) {
    case "text":
      return (
        <div className="whitespace-pre-wrap break-words text-zinc-200">
          {event.text}
        </div>
      );
    case "tool":
      return <ToolCallCard toolName={event.toolName ?? "unknown"} toolInput={event.toolInput} />;
    case "tool_result":
      return null; // tool results are implicit (next text block)
    case "progress":
      return (
        <div className="text-zinc-500 text-[10px]">
          ⏳ {event.toolName}
        </div>
      );
    case "result":
      return (
        <div className="mt-2 pt-2 border-t border-zinc-800 text-green-400">
          ✓ {event.isError ? "Completed with errors" : "Completed"}{" "}
          <span className="text-zinc-500">
            — {formatCost(event.costUSD ?? 0)} · {event.numTurns ?? 0} turns
          </span>
        </div>
      );
    case "error":
      return (
        <div className="text-red-400">
          ✗ {event.text || "Worker error"}
        </div>
      );
    case "init":
      return (
        <div className="text-zinc-500 text-[10px] mb-1">
          ● Connected — {event.model}
        </div>
      );
    default:
      return null;
  }
}
