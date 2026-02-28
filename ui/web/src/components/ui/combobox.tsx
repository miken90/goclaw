import * as React from "react";
import { ChevronDownIcon, CheckIcon } from "lucide-react";
import { cn } from "@/lib/utils";

export interface ComboboxOption {
  value: string;
  label?: string;
}

interface ComboboxProps {
  value: string;
  onChange: (value: string) => void;
  options: ComboboxOption[];
  placeholder?: string;
  className?: string;
}

export function Combobox({
  value,
  onChange,
  options,
  placeholder,
  className,
}: ComboboxProps) {
  const [open, setOpen] = React.useState(false);
  const [search, setSearch] = React.useState("");
  const containerRef = React.useRef<HTMLDivElement>(null);

  // Sync search text when value changes externally — show label if available
  React.useEffect(() => {
    const match = options.find((o) => o.value === value);
    setSearch(match?.label || value);
  }, [value, options]);

  // Close on outside click
  React.useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const filtered = React.useMemo(() => {
    if (!search) return options;
    const q = search.toLowerCase();
    return options.filter(
      (o) =>
        o.value.toLowerCase().includes(q) ||
        (o.label && o.label.toLowerCase().includes(q)),
    );
  }, [options, search]);

  const handleSelect = (val: string) => {
    onChange(val);
    const match = options.find((o) => o.value === val);
    setSearch(match?.label || val);
    setOpen(false);
  };

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = e.target.value;
    setSearch(val);
    onChange(val);
    if (!open && options.length > 0) setOpen(true);
  };

  return (
    <div ref={containerRef} className={cn("relative", className)}>
      <input
        value={search}
        onChange={handleInputChange}
        onFocus={() => options.length > 0 && setOpen(true)}
        placeholder={placeholder}
        className={cn(
          "border-input placeholder:text-muted-foreground dark:bg-input/30 h-9 w-full rounded-md border bg-transparent px-3 py-1 pr-8 text-sm shadow-xs outline-none transition-[color,box-shadow]",
          "focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]",
        )}
      />
      {options.length > 0 && (
        <ChevronDownIcon
          className="text-muted-foreground absolute top-1/2 right-2.5 size-4 -translate-y-1/2 cursor-pointer opacity-50"
          onClick={() => setOpen(!open)}
        />
      )}
      {open && filtered.length > 0 && (
        <div className="bg-popover text-popover-foreground absolute top-full left-0 z-50 mt-1 max-h-60 min-w-full overflow-y-auto rounded-md border p-1 shadow-md">
          {filtered.map((o) => (
            <button
              key={o.value}
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => handleSelect(o.value)}
              className="hover:bg-accent hover:text-accent-foreground relative flex w-full cursor-pointer items-center rounded-sm py-1.5 pr-8 pl-2 text-sm outline-hidden select-none"
            >
              <span className="truncate">{o.label || o.value}</span>
              {o.value === value && (
                <CheckIcon className="absolute right-2 size-4" />
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
