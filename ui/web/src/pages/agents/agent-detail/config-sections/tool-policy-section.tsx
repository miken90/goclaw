import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ToolPolicyConfig } from "@/types/agent";
import { ConfigSection, InfoLabel } from "./config-section";
import { ToolNameSelect } from "@/components/shared/tool-name-select";

interface ToolPolicySectionProps {
  enabled: boolean;
  value: ToolPolicyConfig;
  onToggle: (v: boolean) => void;
  onChange: (v: ToolPolicyConfig) => void;
}

export function ToolPolicySection({ enabled, value, onToggle, onChange }: ToolPolicySectionProps) {
  return (
    <ConfigSection
      title="Tool Policy"
      description="Control which tools this agent can use"
      enabled={enabled}
      onToggle={onToggle}
    >
      <div className="space-y-2">
        <InfoLabel tip="Base tool profile. 'default' includes common tools, 'strict' limits to safe tools only, 'permissive' allows all tools.">Profile</InfoLabel>
        <Select
          value={value.profile ?? ""}
          onValueChange={(v) => onChange({ ...value, profile: v || undefined })}
        >
          <SelectTrigger><SelectValue placeholder="default" /></SelectTrigger>
          <SelectContent>
            <SelectItem value="default">default</SelectItem>
            <SelectItem value="strict">strict</SelectItem>
            <SelectItem value="permissive">permissive</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="space-y-2">
        <InfoLabel tip="Explicit allowlist. Only these tools will be available (overrides profile). Leave empty to use profile defaults.">Allow</InfoLabel>
        <ToolNameSelect
          value={value.allow ?? []}
          onChange={(v) => onChange({ ...value, allow: v.length > 0 ? v : undefined })}
          placeholder="Select tools to allow..."
        />
      </div>
      <div className="space-y-2">
        <InfoLabel tip="Denylist. These tools will be blocked even if allowed by the profile.">Deny</InfoLabel>
        <ToolNameSelect
          value={value.deny ?? []}
          onChange={(v) => onChange({ ...value, deny: v.length > 0 ? v : undefined })}
          placeholder="Select tools to deny..."
        />
      </div>
      <div className="space-y-2">
        <InfoLabel tip="Additional tools on top of profile defaults. Useful for enabling optional tools without overriding the whole profile.">Also Allow</InfoLabel>
        <ToolNameSelect
          value={value.alsoAllow ?? []}
          onChange={(v) => onChange({ ...value, alsoAllow: v.length > 0 ? v : undefined })}
          placeholder="Select additional tools..."
        />
      </div>
    </ConfigSection>
  );
}
