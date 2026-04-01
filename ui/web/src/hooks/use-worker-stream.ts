import { useCallback, useEffect } from "react";
import { create } from "zustand";
import { useWsEvent } from "./use-ws-event";
import { Events } from "@/api/protocol";

const MAX_EVENTS = 500;

export interface WorkerStreamEvent {
  type: "init" | "text" | "tool" | "tool_result" | "progress" | "result" | "error";
  text?: string;
  toolName?: string;
  toolInput?: string;
  costUSD?: number;
  numTurns?: number;
  durationMs?: number;
  isError?: boolean;
  model?: string;
  timestamp: number;
}

export interface WorkerStreamSession {
  status: "connecting" | "connected" | "completed" | "error" | "disconnected";
  events: WorkerStreamEvent[];
  model: string;
  costUSD: number;
  numTurns: number;
  durationMs: number;
  startedAt: number;
}

interface WorkerStreamState {
  sessions: Record<string, WorkerStreamSession>;
  addEvent: (taskId: string, event: WorkerStreamEvent) => void;
  setStatus: (taskId: string, status: WorkerStreamSession["status"]) => void;
  clear: (taskId: string) => void;
}

function createSession(): WorkerStreamSession {
  return {
    status: "connecting",
    events: [],
    model: "",
    costUSD: 0,
    numTurns: 0,
    durationMs: 0,
    startedAt: Date.now(),
  };
}

export const useWorkerStreamStore = create<WorkerStreamState>((set) => ({
  sessions: {},

  addEvent: (taskId, event) =>
    set((s) => {
      const session = s.sessions[taskId] ?? createSession();
      const events =
        session.events.length >= MAX_EVENTS
          ? [...session.events.slice(1), event]
          : [...session.events, event];

      const updated: WorkerStreamSession = {
        ...session,
        events,
        model: event.model || session.model,
        costUSD: event.costUSD ?? session.costUSD,
        numTurns: event.numTurns ?? session.numTurns,
        durationMs: event.durationMs ?? session.durationMs,
      };

      if (event.type === "init") {
        updated.status = "connected";
      } else if (event.type === "result") {
        updated.status = event.isError ? "error" : "completed";
      } else if (event.type === "error") {
        updated.status = "error";
      }

      return { sessions: { ...s.sessions, [taskId]: updated } };
    }),

  setStatus: (taskId, status) =>
    set((s) => {
      const session = s.sessions[taskId];
      if (!session) return s;
      return { sessions: { ...s.sessions, [taskId]: { ...session, status } } };
    }),

  clear: (taskId) =>
    set((s) => {
      const { [taskId]: _, ...rest } = s.sessions;
      return { sessions: rest };
    }),
}));

/** Map event names to WorkerStreamEvent.type */
const EVENT_TYPE_MAP: Record<string, WorkerStreamEvent["type"]> = {
  [Events.WORKER_STREAM_INIT]: "init",
  [Events.WORKER_STREAM_TEXT]: "text",
  [Events.WORKER_STREAM_TOOL]: "tool",
  [Events.WORKER_STREAM_TOOL_RESULT]: "tool_result",
  [Events.WORKER_STREAM_PROGRESS]: "progress",
  [Events.WORKER_STREAM_RESULT]: "result",
  [Events.WORKER_STREAM_ERROR]: "error",
};

interface WorkerStreamPayload {
  team_id: string;
  task_id: string;
  event_type: string;
  seq: number;
  text?: string;
  tool_name?: string;
  tool_input?: string;
  cost_usd?: number;
  num_turns?: number;
  duration_ms?: number;
  is_error?: boolean;
  model?: string;
}

/**
 * Subscribe to worker stream events for a specific task.
 * Returns the session state from the Zustand store.
 */
export function useWorkerStream(taskId: string | undefined) {
  const session = useWorkerStreamStore((s) =>
    taskId ? s.sessions[taskId] : undefined,
  );
  const addEvent = useWorkerStreamStore((s) => s.addEvent);
  const setStatus = useWorkerStreamStore((s) => s.setStatus);

  // Subscribe to all worker.stream.* events via a wildcard-like approach:
  // WsTeamEventCapture already captures them, but we also listen directly.
  const handleEvent = useCallback(
    (raw: unknown) => {
      const { event, payload } = raw as { event: string; payload: unknown };
      const type = EVENT_TYPE_MAP[event];
      if (!type) return;

      const p = payload as WorkerStreamPayload;
      if (!taskId || p.task_id !== taskId) return;

      addEvent(taskId, {
        type,
        text: p.text,
        toolName: p.tool_name,
        toolInput: p.tool_input,
        costUSD: p.cost_usd,
        numTurns: p.num_turns,
        durationMs: p.duration_ms,
        isError: p.is_error,
        model: p.model,
        timestamp: Date.now(),
      });
    },
    [taskId, addEvent],
  );

  const handleDisconnect = useCallback(
    (raw: unknown) => {
      const { payload } = raw as { event: string; payload: unknown };
      const p = payload as WorkerStreamPayload;
      if (!taskId || p.task_id !== taskId) return;
      setStatus(taskId, "disconnected");
    },
    [taskId, setStatus],
  );

  useWsEvent("*", handleEvent);
  useWsEvent(Events.WORKER_DISCONNECTED, handleDisconnect);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      // Don't clear — session survives dialog close/reopen
    };
  }, [taskId]);

  return session;
}
