import { useState, useEffect } from "react";
import type { TeamTaskData } from "@/types/team";
import { TaskList } from "./task-sections";

interface TeamTasksTabProps {
  teamId: string;
  getTeamTasks: (teamId: string) => Promise<{ tasks: TeamTaskData[]; count: number }>;
}

export function TeamTasksTab({ teamId, getTeamTasks }: TeamTasksTabProps) {
  const [tasks, setTasks] = useState<TeamTaskData[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const res = await getTeamTasks(teamId);
        if (!cancelled) setTasks(res.tasks ?? []);
      } catch {
        // ignore
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [teamId, getTeamTasks]);

  return (
    <div className="space-y-6">
      <TaskList tasks={tasks} loading={loading} />
    </div>
  );
}
