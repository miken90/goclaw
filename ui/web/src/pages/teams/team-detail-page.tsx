import { useState, useEffect, useCallback } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ArrowLeft, Users } from "lucide-react";
import { DeferredSpinner } from "@/components/shared/loading-skeleton";
import { useTeams } from "./hooks/use-teams";
import { TeamMembersTab } from "./team-members-tab";
import { TeamTasksTab } from "./team-tasks-tab";
import { TeamDelegationsTab } from "./team-delegations-tab";
import type { TeamData, TeamMemberData } from "@/types/team";

interface TeamDetailPageProps {
  teamId: string;
  onBack: () => void;
}

export function TeamDetailPage({ teamId, onBack }: TeamDetailPageProps) {
  const { getTeam, getTeamTasks, addMember, removeMember } = useTeams();
  const [team, setTeam] = useState<TeamData | null>(null);
  const [members, setMembers] = useState<TeamMemberData[]>([]);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const res = await getTeam(teamId);
      setTeam(res.team);
      setMembers(res.members ?? []);
    } catch {
      // ignore
    }
  }, [teamId, getTeam]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const res = await getTeam(teamId);
        if (!cancelled) {
          setTeam(res.team);
          setMembers(res.members ?? []);
        }
      } catch {
        // ignore
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [teamId, getTeam]);

  const handleAddMember = useCallback(async (agentId: string) => {
    await addMember(teamId, agentId);
    await reload();
  }, [teamId, addMember, reload]);

  const handleRemoveMember = useCallback(async (agentId: string) => {
    await removeMember(teamId, agentId);
    await reload();
  }, [teamId, removeMember, reload]);

  if (loading || !team) {
    return (
      <div className="p-6">
        <Button variant="ghost" onClick={onBack} className="mb-4 gap-1">
          <ArrowLeft className="h-4 w-4" /> Back
        </Button>
        <DeferredSpinner />
      </div>
    );
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6 flex items-start gap-4">
        <Button variant="ghost" size="icon" onClick={onBack} className="mt-0.5 shrink-0">
          <ArrowLeft className="h-4 w-4" />
        </Button>
        <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
          <Users className="h-6 w-6" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-xl font-semibold">{team.name}</h2>
            <Badge variant={team.status === "active" ? "success" : "secondary"}>
              {team.status}
            </Badge>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
            {team.lead_agent_key && (
              <>
                <span>Lead: {team.lead_agent_key}</span>
                <span className="text-border">|</span>
              </>
            )}
            <span>{members.length} member{members.length !== 1 ? "s" : ""}</span>
          </div>
          {team.description && (
            <p className="mt-1 text-sm text-muted-foreground/70">{team.description}</p>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="rounded-xl border bg-card p-4 shadow-sm">
        <Tabs defaultValue="members">
          <TabsList>
            <TabsTrigger value="members">Members</TabsTrigger>
            <TabsTrigger value="tasks">Tasks</TabsTrigger>
            <TabsTrigger value="delegations">Delegations</TabsTrigger>
          </TabsList>

          <TabsContent value="members" className="mt-4">
            <TeamMembersTab
              teamId={teamId}
              members={members}
              onAddMember={handleAddMember}
              onRemoveMember={handleRemoveMember}
            />
          </TabsContent>

          <TabsContent value="tasks" className="mt-4">
            <TeamTasksTab teamId={teamId} getTeamTasks={getTeamTasks} />
          </TabsContent>

          <TabsContent value="delegations" className="mt-4">
            <TeamDelegationsTab teamId={teamId} />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
