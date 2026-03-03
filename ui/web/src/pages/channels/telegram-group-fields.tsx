import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { groupPolicyOptions } from "./channel-schemas";

export interface TelegramGroupConfigValues {
  group_policy?: string;
  require_mention?: boolean;
  enabled?: boolean;
  allow_from?: string[];
  skills?: string[];
  system_prompt?: string;
}

const INHERIT = "__inherit__";

const triStateOptions = [
  { value: INHERIT, label: "Inherit (channel default)" },
  { value: "true", label: "Yes" },
  { value: "false", label: "No" },
];

const groupPolicyWithInherit = [
  { value: INHERIT, label: "Inherit (channel default)" },
  ...groupPolicyOptions,
];

function triStateValue(val: boolean | undefined): string {
  if (val === undefined || val === null) return INHERIT;
  return val ? "true" : "false";
}

function parseTriState(val: string): boolean | undefined {
  if (val === INHERIT || val === "") return undefined;
  return val === "true";
}

interface Props {
  config: TelegramGroupConfigValues;
  onChange: (config: TelegramGroupConfigValues) => void;
  idPrefix: string;
}

export function TelegramGroupFields({ config, onChange, idPrefix }: Props) {
  const update = (patch: Partial<TelegramGroupConfigValues>) => {
    onChange({ ...config, ...patch });
  };

  return (
    <div className="grid gap-3">
      <div className="grid gap-1.5">
        <Label>Group Policy</Label>
        <Select
          value={config.group_policy || INHERIT}
          onValueChange={(v) => update({ group_policy: v === INHERIT ? undefined : v })}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {groupPolicyWithInherit.map((opt) => (
              <SelectItem key={opt.value} value={opt.value}>
                {opt.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="grid gap-1.5">
        <Label>Require @mention</Label>
        <Select
          value={triStateValue(config.require_mention)}
          onValueChange={(v) => update({ require_mention: parseTriState(v) })}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {triStateOptions.map((opt) => (
              <SelectItem key={opt.value} value={opt.value}>
                {opt.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="grid gap-1.5">
        <Label>Enabled</Label>
        <Select
          value={triStateValue(config.enabled)}
          onValueChange={(v) => update({ enabled: parseTriState(v) })}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {triStateOptions.map((opt) => (
              <SelectItem key={opt.value} value={opt.value}>
                {opt.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="grid gap-1.5">
        <Label htmlFor={`${idPrefix}-allow`}>Allowed Users</Label>
        <Textarea
          id={`${idPrefix}-allow`}
          value={config.allow_from?.join("\n") ?? ""}
          onChange={(e) => {
            const lines = e.target.value.split("\n").map((l) => l.trim()).filter(Boolean);
            update({ allow_from: lines.length > 0 ? lines : undefined });
          }}
          placeholder="One user ID per line"
          rows={2}
          className="font-mono text-sm"
        />
      </div>

      <div className="grid gap-1.5">
        <Label htmlFor={`${idPrefix}-skills`}>Skills Filter</Label>
        <Textarea
          id={`${idPrefix}-skills`}
          value={config.skills?.join("\n") ?? ""}
          onChange={(e) => {
            const lines = e.target.value.split("\n").map((l) => l.trim()).filter(Boolean);
            update({ skills: lines.length > 0 ? lines : undefined });
          }}
          placeholder="One skill name per line (empty = inherit)"
          rows={2}
          className="font-mono text-sm"
        />
      </div>

      <div className="grid gap-1.5">
        <Label htmlFor={`${idPrefix}-prompt`}>System Prompt</Label>
        <Textarea
          id={`${idPrefix}-prompt`}
          value={config.system_prompt ?? ""}
          onChange={(e) => update({ system_prompt: e.target.value || undefined })}
          placeholder="Additional system prompt for this group/topic"
          rows={3}
        />
      </div>
    </div>
  );
}
