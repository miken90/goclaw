import { useState, useCallback } from "react";
import { useWs } from "@/hooks/use-ws";
import { Methods } from "@/api/protocol";
import type { TeamData, TeamMemberData, TeamTaskData } from "@/types/team";

export function useTeams() {
  const ws = useWs();
  const [teams, setTeams] = useState<TeamData[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    if (!ws.isConnected) return;
    setLoading(true);
    try {
      const res = await ws.call<{ teams: TeamData[]; count: number }>(
        Methods.TEAMS_LIST,
      );
      setTeams(res.teams ?? []);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [ws]);

  const createTeam = useCallback(
    async (params: {
      name: string;
      lead: string;
      members: string[];
      description?: string;
    }) => {
      await ws.call(Methods.TEAMS_CREATE, params);
      load();
    },
    [ws, load],
  );

  const deleteTeam = useCallback(
    async (teamId: string) => {
      await ws.call(Methods.TEAMS_DELETE, { teamId });
      load();
    },
    [ws, load],
  );

  const getTeam = useCallback(
    async (teamId: string) => {
      const res = await ws.call<{ team: TeamData; members: TeamMemberData[] }>(
        Methods.TEAMS_GET,
        { teamId },
      );
      return res;
    },
    [ws],
  );

  const getTeamTasks = useCallback(
    async (teamId: string) => {
      const res = await ws.call<{ tasks: TeamTaskData[]; count: number }>(
        Methods.TEAMS_TASK_LIST,
        { teamId },
      );
      return res;
    },
    [ws],
  );

  const addMember = useCallback(
    async (teamId: string, agent: string) => {
      await ws.call(Methods.TEAMS_MEMBERS_ADD, { teamId, agent });
    },
    [ws],
  );

  const removeMember = useCallback(
    async (teamId: string, agentId: string) => {
      await ws.call(Methods.TEAMS_MEMBERS_REMOVE, { teamId, agentId });
    },
    [ws],
  );

  return { teams, loading, load, createTeam, deleteTeam, getTeam, getTeamTasks, addMember, removeMember };
}
