import { FileText } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { BootstrapFile } from "@/types/agent";

interface FileSidebarProps {
  files: BootstrapFile[];
  selectedFile: string | null;
  onSelect: (name: string) => void;
  isUserScoped: (name: string) => boolean;
}

export function FileSidebar({
  files,
  selectedFile,
  onSelect,
  isUserScoped,
}: FileSidebarProps) {
  return (
    <div className="w-48 shrink-0 overflow-y-auto rounded-lg bg-muted/40 p-2">
      <div className="space-y-0.5">
        {files.map((file) => {
          const userScoped = isUserScoped(file.name);
          const active = selectedFile === file.name;
          return (
            <button
              key={file.name}
              type="button"
              onClick={() => !userScoped && onSelect(file.name)}
              disabled={userScoped}
              className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] transition-colors ${
                userScoped
                  ? "cursor-not-allowed opacity-50"
                  : active
                    ? "bg-background text-foreground shadow-sm"
                    : "text-muted-foreground hover:bg-background/60 hover:text-foreground"
              }`}
            >
              <FileText className="h-3.5 w-3.5 shrink-0" />
              <span className="min-w-0 flex-1 truncate text-left">
                {file.name}
              </span>
              {userScoped ? (
                <Badge variant="outline" className="shrink-0 text-[10px]">
                  per-user
                </Badge>
              ) : file.missing ? (
                <span className="shrink-0 text-[10px] text-muted-foreground/60">
                  empty
                </span>
              ) : (
                <span className="shrink-0 text-[10px] text-muted-foreground/60">
                  {file.size > 1024
                    ? `${(file.size / 1024).toFixed(1)}K`
                    : `${file.size}B`}
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}
