import { useState, useEffect, useMemo } from "react";
import { Link } from "react-router";
import { Info } from "lucide-react";
import { ConfirmDialog } from "@/components/shared/confirm-dialog";
import { useAgentLinks } from "../hooks/use-agent-links";
import { useAgents } from "../hooks/use-agents";
import type { AgentLinkData } from "@/types/agent";
import { LinkCreateForm, LinkList, LinkEditDialog, linkTargetName } from "./link-sections";
import { ROUTES } from "@/lib/constants";

interface AgentLinksTabProps {
  agentId: string;
}

export function AgentLinksTab({ agentId }: AgentLinksTabProps) {
  const { links, loading, load, createLink, updateLink, deleteLink } =
    useAgentLinks(agentId);
  const { agents } = useAgents();

  const [editLink, setEditLink] = useState<AgentLinkData | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<{
    id: string;
    name: string;
  } | null>(null);

  useEffect(() => {
    load();
  }, [load]);

  // Only predefined agents can be delegation targets (open agents have no agent-level context)
  const agentOptions = useMemo(
    () =>
      agents
        .filter((a) => a.id !== agentId && a.agent_type === "predefined")
        .map((a) => ({
          value: a.id,
          label: a.display_name || a.agent_key,
        })),
    [agents, agentId],
  );

  const handleStatusToggle = async (link: AgentLinkData) => {
    const newStatus = link.status === "active" ? "disabled" : "active";
    await updateLink(link.id, { status: newStatus });
  };

  return (
    <div className="max-w-4xl space-y-6">
      <div className="flex items-start gap-3 rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 dark:border-blue-900 dark:bg-blue-950/30">
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-blue-600 dark:text-blue-400" />
        <p className="text-sm text-blue-800 dark:text-blue-300">
          For multi-agent collaboration with task tracking and quality control, consider using{" "}
          <Link to={ROUTES.TEAMS} className="font-medium underline underline-offset-2 hover:text-blue-900 dark:hover:text-blue-200">
            Agent Teams
          </Link>{" "}
          instead. Agent Links are best for simple 1-to-1 delegation between two agents.
        </p>
      </div>

      <LinkCreateForm agentOptions={agentOptions} onSubmit={createLink} />

      <LinkList
        links={links}
        loading={loading}
        agentId={agentId}
        onStatusToggle={handleStatusToggle}
        onEdit={setEditLink}
        onDelete={(link) =>
          setDeleteTarget({ id: link.id, name: linkTargetName(link, agentId) })
        }
      />

      <LinkEditDialog
        link={editLink}
        onClose={() => setEditLink(null)}
        onSave={updateLink}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={() => setDeleteTarget(null)}
        title="Delete Link"
        description={`Remove the delegation link to "${deleteTarget?.name}"? Agents will no longer be able to delegate to each other through this link.`}
        confirmLabel="Delete"
        variant="destructive"
        onConfirm={async () => {
          if (deleteTarget) {
            try {
              await deleteLink(deleteTarget.id);
            } catch {
              // ignore
            }
            setDeleteTarget(null);
          }
        }}
      />
    </div>
  );
}
