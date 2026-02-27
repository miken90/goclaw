import { useState, useCallback } from "react";
import { Save, Check, AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import type { AgentData } from "@/types/agent";
import { IdentitySection, LlmConfigSection, WorkspaceSection } from "./general-sections";

interface AgentGeneralTabProps {
  agent: AgentData;
  onUpdate: (updates: Record<string, unknown>) => Promise<void>;
}

export function AgentGeneralTab({ agent, onUpdate }: AgentGeneralTabProps) {
  // Identity
  const [displayName, setDisplayName] = useState(agent.display_name ?? "");
  const [frontmatter, setFrontmatter] = useState(agent.frontmatter ?? "");
  const [status, setStatus] = useState(agent.status);
  const [isDefault, setIsDefault] = useState(agent.is_default);

  // LLM
  const [provider, setProvider] = useState(agent.provider);
  const [model, setModel] = useState(agent.model);
  const [contextWindow, setContextWindow] = useState(agent.context_window || 200000);
  const [maxToolIterations, setMaxToolIterations] = useState(agent.max_tool_iterations || 20);
  const [llmSaveBlocked, setLlmSaveBlocked] = useState(false);

  // Workspace
  const [restrictToWorkspace, setRestrictToWorkspace] = useState(agent.restrict_to_workspace);

  // Save state
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const handleSaveBlockedChange = useCallback((blocked: boolean) => {
    setLlmSaveBlocked(blocked);
  }, []);

  const handleSave = async () => {
    setSaving(true);
    setSaveError(null);
    setSaved(false);
    try {
      await onUpdate({
        display_name: displayName,
        frontmatter: frontmatter || null,
        provider,
        model,
        context_window: contextWindow,
        max_tool_iterations: maxToolIterations,
        restrict_to_workspace: restrictToWorkspace,
        status,
        is_default: isDefault,
      });
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "Failed to save");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="max-w-4xl space-y-6">
      <IdentitySection
        agentKey={agent.agent_key}
        displayName={displayName}
        onDisplayNameChange={setDisplayName}
        frontmatter={frontmatter}
        onFrontmatterChange={setFrontmatter}
        status={status}
        onStatusChange={setStatus}
        isDefault={isDefault}
        onIsDefaultChange={setIsDefault}
      />

      <Separator />

      <LlmConfigSection
        provider={provider}
        onProviderChange={setProvider}
        model={model}
        onModelChange={setModel}
        contextWindow={contextWindow}
        onContextWindowChange={setContextWindow}
        maxToolIterations={maxToolIterations}
        onMaxToolIterationsChange={setMaxToolIterations}
        savedProvider={agent.provider}
        savedModel={agent.model}
        onSaveBlockedChange={handleSaveBlockedChange}
      />

      <Separator />

      <WorkspaceSection
        workspace={agent.workspace}
        restrictToWorkspace={restrictToWorkspace}
        onRestrictChange={setRestrictToWorkspace}
      />

      {/* Save */}
      {saveError && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {saveError}
        </div>
      )}
      <div className="flex items-center justify-end gap-2">
        {saved && (
          <span className="flex items-center gap-1 text-sm text-success">
            <Check className="h-3.5 w-3.5" /> Saved
          </span>
        )}
        <Button onClick={handleSave} disabled={saving || llmSaveBlocked}>
          {!saving && <Save className="h-4 w-4" />}
          {saving ? "Saving..." : "Save Changes"}
        </Button>
      </div>
    </div>
  );
}
