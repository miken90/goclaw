import { useState, useEffect, useCallback } from "react";
import { useHttp, useWs } from "@/hooks/use-ws";
import { Methods } from "@/api/protocol";
import type { AgentData, BootstrapFile } from "@/types/agent";

export function useAgentDetail(agentId: string | undefined) {
  const http = useHttp();
  const ws = useWs();
  const [agent, setAgent] = useState<AgentData | null>(null);
  const [files, setFiles] = useState<BootstrapFile[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    if (!agentId) return;
    setLoading(true);
    try {
      // Try HTTP first (may fail with 403 if user isn't owner/shared)
      let ag: AgentData | null = null;
      try {
        ag = await http.get<AgentData>(`/v1/agents/${agentId}`);
      } catch {
        // HTTP failed - construct minimal agent from agentId (which is the agent_key)
        ag = {
          id: agentId,
          agent_key: agentId,
          owner_id: "",
          provider: "",
          model: "",
          context_window: 0,
          max_tool_iterations: 0,
          workspace: "",
          restrict_to_workspace: false,
          agent_type: "open" as const,
          is_default: false,
          status: "active",
        };
      }
      setAgent(ag);

      // Load files via WS (no access control)
      if (ws.isConnected) {
        const filesRes = await ws.call<{ files: BootstrapFile[] }>(
          Methods.AGENTS_FILES_LIST,
          { agentId: ag.agent_key },
        );
        setFiles(filesRes.files ?? []);
      }
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [agentId, http, ws]);

  useEffect(() => {
    load();
  }, [load]);

  const updateAgent = useCallback(
    async (updates: Record<string, unknown>) => {
      if (!agentId) return;
      await http.put(`/v1/agents/${agentId}`, updates);
      load();
    },
    [agentId, http, load],
  );

  const getFile = useCallback(
    async (name: string): Promise<BootstrapFile | null> => {
      if (!agent || !ws.isConnected) return null;
      const res = await ws.call<{ file: BootstrapFile }>(Methods.AGENTS_FILES_GET, {
        agentId: agent.agent_key,
        name,
      });
      return res.file;
    },
    [agent, ws],
  );

  const setFile = useCallback(
    async (name: string, content: string) => {
      if (!agent || !ws.isConnected) return;
      await ws.call(Methods.AGENTS_FILES_SET, {
        agentId: agent.agent_key,
        name,
        content,
      });
      load();
    },
    [agent, ws, load],
  );

  const regenerateAgent = useCallback(
    async (prompt: string) => {
      if (!agentId) return;
      await http.post(`/v1/agents/${agentId}/regenerate`, { prompt });
    },
    [agentId, http],
  );

  const resummonAgent = useCallback(async () => {
    if (!agentId) return;
    await http.post(`/v1/agents/${agentId}/resummon`);
  }, [agentId, http]);

  return { agent, files, loading, updateAgent, getFile, setFile, regenerateAgent, resummonAgent, refresh: load };
}
