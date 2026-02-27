import { useMemo, useEffect } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Combobox } from "@/components/ui/combobox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useProviders } from "@/pages/providers/hooks/use-providers";
import { useProviderModels } from "@/pages/providers/hooks/use-provider-models";
import { useProviderVerify } from "@/pages/providers/hooks/use-provider-verify";

interface LlmConfigSectionProps {
  provider: string;
  onProviderChange: (v: string) => void;
  model: string;
  onModelChange: (v: string) => void;
  contextWindow: number;
  onContextWindowChange: (v: number) => void;
  maxToolIterations: number;
  onMaxToolIterationsChange: (v: number) => void;
  savedProvider: string;
  savedModel: string;
  /** Called when verification status changes. True = save should be blocked. */
  onSaveBlockedChange?: (blocked: boolean) => void;
}

export function LlmConfigSection({
  provider,
  onProviderChange,
  model,
  onModelChange,
  contextWindow,
  onContextWindowChange,
  maxToolIterations,
  onMaxToolIterationsChange,
  savedProvider,
  savedModel,
  onSaveBlockedChange,
}: LlmConfigSectionProps) {
  const { providers } = useProviders();
  const enabledProviders = providers.filter((p) => p.enabled);

  const selectedProviderId = useMemo(
    () => enabledProviders.find((p) => p.name === provider)?.id,
    [enabledProviders, provider],
  );
  const { models, loading: modelsLoading } = useProviderModels(selectedProviderId);
  const { verify, verifying, result: verifyResult, reset: resetVerify } = useProviderVerify();

  const llmChanged = provider !== savedProvider || model !== savedModel;

  useEffect(() => {
    resetVerify();
  }, [provider, model, resetVerify]);

  // Report save-blocked status to parent
  useEffect(() => {
    onSaveBlockedChange?.(llmChanged && !verifyResult?.valid);
  }, [llmChanged, verifyResult, onSaveBlockedChange]);

  const handleVerify = async () => {
    if (!selectedProviderId || !model.trim()) return;
    await verify(selectedProviderId, model.trim());
  };

  return (
    <section className="space-y-4">
      <h3 className="text-sm font-medium text-muted-foreground">LLM Configuration</h3>
      <div className="space-y-4 rounded-lg border p-4">
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Provider</Label>
            {enabledProviders.length > 0 ? (
              <Select
                value={provider}
                onValueChange={(v) => {
                  onProviderChange(v);
                  onModelChange("");
                }}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select provider" />
                </SelectTrigger>
                <SelectContent>
                  {enabledProviders.map((p) => (
                    <SelectItem key={p.name} value={p.name}>
                      {p.display_name || p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                value={provider}
                onChange={(e) => onProviderChange(e.target.value)}
                placeholder="openrouter"
              />
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="model">Model</Label>
            <div className="flex gap-2">
              <div className="flex-1">
                <Combobox
                  value={model}
                  onChange={onModelChange}
                  options={models.map((m) => ({ value: m.id, label: m.name }))}
                  placeholder={modelsLoading ? "Loading models..." : "Enter or select model"}
                />
              </div>
              {llmChanged && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-9 px-3"
                  disabled={!selectedProviderId || !model.trim() || verifying}
                  onClick={handleVerify}
                >
                  {verifying ? "..." : "Check"}
                </Button>
              )}
            </div>
            {verifyResult && (
              <p className={`text-xs ${verifyResult.valid ? "text-success" : "text-destructive"}`}>
                {verifyResult.valid ? "Model verified" : verifyResult.error || "Verification failed"}
              </p>
            )}
          </div>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="contextWindow">Context Window</Label>
            <Input
              id="contextWindow"
              type="number"
              value={contextWindow || ""}
              onChange={(e) => onContextWindowChange(Number(e.target.value) || 0)}
              placeholder="200000"
            />
            <p className="text-xs text-muted-foreground">Token limit for the model context.</p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="maxToolIterations">Max Tool Iterations</Label>
            <Input
              id="maxToolIterations"
              type="number"
              value={maxToolIterations || ""}
              onChange={(e) => onMaxToolIterationsChange(Number(e.target.value) || 0)}
              placeholder="25"
            />
            <p className="text-xs text-muted-foreground">Max tool calls per request.</p>
          </div>
        </div>
      </div>
    </section>
  );
}
