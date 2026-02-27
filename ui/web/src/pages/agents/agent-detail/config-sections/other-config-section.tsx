import { useState, useEffect } from "react";
import { Textarea } from "@/components/ui/textarea";
import { Button } from "@/components/ui/button";
import { ConfigSection } from "./config-section";

interface OtherConfigSectionProps {
  enabled: boolean;
  value: string;
  onToggle: (v: boolean) => void;
  onChange: (v: string) => void;
}

export function OtherConfigSection({ enabled, value, onToggle, onChange }: OtherConfigSectionProps) {
  const [validJson, setValidJson] = useState(true);

  useEffect(() => {
    try {
      JSON.parse(value || "{}");
      setValidJson(true);
    } catch {
      setValidJson(false);
    }
  }, [value]);

  const handleFormat = () => {
    try {
      const parsed = JSON.parse(value);
      onChange(JSON.stringify(parsed, null, 2));
    } catch {
      // can't format invalid JSON
    }
  };

  return (
    <ConfigSection
      title="Other Config"
      description="Additional JSON config for advanced settings not covered by other sections"
      enabled={enabled}
      onToggle={onToggle}
    >
      <Textarea
        className={`font-mono text-sm ${!validJson ? "border-destructive" : ""}`}
        rows={6}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder='{ "key": "value" }'
      />
      <div className="flex items-center justify-between">
        <Button variant="ghost" size="sm" onClick={handleFormat} disabled={!validJson} className="h-7 px-2 text-xs">
          Format JSON
        </Button>
        {!validJson && <span className="text-xs text-destructive">Invalid JSON syntax</span>}
      </div>
    </ConfigSection>
  );
}
