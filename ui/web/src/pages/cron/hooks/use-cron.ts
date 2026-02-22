import { useState, useEffect, useCallback } from "react";
import { useWs } from "@/hooks/use-ws";
import { Methods } from "@/api/protocol";

export interface CronSchedule {
  kind: "at" | "every" | "cron";
  atMs?: number;
  everyMs?: number;
  expr?: string;
  tz?: string;
}

export interface CronPayload {
  kind: string;
  message: string;
  deliver: boolean;
  channel: string;
  to: string;
}

export interface CronJob {
  id: string;
  name: string;
  agentId?: string;
  enabled: boolean;
  schedule: CronSchedule;
  payload: CronPayload;
  createdAtMs: number;
  updatedAtMs: number;
  deleteAfterRun?: boolean;
  state?: {
    nextRunAtMs?: number;
    lastRunAtMs?: number;
    lastStatus?: string;
    lastError?: string;
  };
}

export interface CronRunLogEntry {
  ts: number;
  jobId: string;
  status?: string;
  error?: string;
  summary?: string;
}

export function useCron() {
  const ws = useWs();
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    if (!ws.isConnected) return;
    setLoading(true);
    try {
      const res = await ws.call<{ jobs: CronJob[] }>(Methods.CRON_LIST, {
        includeDisabled: true,
      });
      setJobs(res.jobs ?? []);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [ws]);

  useEffect(() => {
    load();
  }, [load]);

  const createJob = useCallback(
    async (params: {
      name: string;
      schedule: CronSchedule;
      message: string;
      agentId?: string;
      deliver?: boolean;
      channel?: string;
      to?: string;
    }) => {
      await ws.call(Methods.CRON_CREATE, params);
      load();
    },
    [ws, load],
  );

  const toggleJob = useCallback(
    async (jobId: string, enabled: boolean) => {
      await ws.call(Methods.CRON_TOGGLE, { jobId, enabled });
      load();
    },
    [ws, load],
  );

  const deleteJob = useCallback(
    async (jobId: string) => {
      await ws.call(Methods.CRON_DELETE, { jobId });
      load();
    },
    [ws, load],
  );

  const runJob = useCallback(
    async (jobId: string) => {
      await ws.call(Methods.CRON_RUN, { jobId, mode: "force" });
    },
    [ws],
  );

  const getRunLog = useCallback(
    async (jobId: string, limit = 20): Promise<CronRunLogEntry[]> => {
      if (!ws.isConnected) return [];
      const res = await ws.call<{ entries: CronRunLogEntry[] }>(Methods.CRON_RUNS, {
        jobId,
        limit,
      });
      return res.entries ?? [];
    },
    [ws],
  );

  return { jobs, loading, refresh: load, createJob, toggleJob, deleteJob, runJob, getRunLog };
}
