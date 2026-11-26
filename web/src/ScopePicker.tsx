import { useEffect, useId, useRef, useState } from "react";
import { IconChevronDown, IconFolder, IconSearch, IconUsers } from "@tabler/icons-react";
import { api, type Scope, type ScopeItem } from "./api";

export function ScopeIcon({ kind, size = 15 }: { kind: Scope["kind"]; size?: number }) {
  return kind === "group" ? <IconUsers size={size} aria-hidden /> : <IconFolder size={size} aria-hidden />;
}

// ScopePicker is the page title: the chosen group or project's name, which
// opens a search to switch scope.
export function ScopePicker({ value, name, onChange }: { value?: Scope; name?: string; onChange: (s: Scope) => void }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<ScopeItem[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [active, setActive] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listId = useId();

  useEffect(() => {
    if (!open) return;
    const ctrl = new AbortController();
    const t = setTimeout(async () => {
      setLoading(true);
      try {
        setItems(await api.scopes(query.trim(), ctrl.signal));
        setError("");
        setActive(0);
      } catch (e) {
        if ((e as Error).name !== "AbortError") setError((e as Error).message);
      } finally {
        if (!ctrl.signal.aborted) setLoading(false);
      }
    }, 250);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
  }, [open, query]);

  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus();
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const choose = (it: ScopeItem) => {
    onChange({ kind: it.kind, path: it.path });
    setOpen(false);
    setQuery("");
  };

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") setOpen(false);
    else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, items.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter" && items[active]) {
      e.preventDefault();
      choose(items[active]);
    }
  };

  return (
    <div className="scope" ref={rootRef}>
      <button
        className="scope-title"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={value ? `${value.kind}: ${value.path} (click to switch)` : undefined}
      >
        {value ? (
          <>
            <span className="scope-icon">
              <ScopeIcon kind={value.kind} size={18} />
            </span>
            <span className="scope-name">{name || value.path}</span>
          </>
        ) : (
          <span className="scope-name">Choose a group or project</span>
        )}
        <IconChevronDown size={17} aria-hidden className="scope-caret" />
      </button>
      {open && (
        <div className="scope-pop">
          <label className="scope-search">
            <IconSearch size={15} aria-hidden />
            <input
              ref={inputRef}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={onKey}
              placeholder="Search groups and projects"
              role="combobox"
              aria-controls={listId}
              aria-expanded
              aria-autocomplete="list"
            />
          </label>
          <ul id={listId} role="listbox" className="scope-list">
            {error && <li className="scope-msg error">{error}</li>}
            {!error && !loading && items.length === 0 && <li className="scope-msg">No matches</li>}
            {items.map((it, i) => (
              <li
                key={it.kind + it.path}
                role="option"
                aria-selected={i === active}
                className={i === active ? "active" : undefined}
                onMouseEnter={() => setActive(i)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  choose(it);
                }}
              >
                <ScopeIcon kind={it.kind} />
                <span className="scope-item-text">
                  <span className="scope-item-path">{it.path}</span>
                  <span className="scope-item-kind">{it.kind}</span>
                </span>
              </li>
            ))}
            {loading && <li className="scope-msg">Searching…</li>}
          </ul>
        </div>
      )}
    </div>
  );
}
