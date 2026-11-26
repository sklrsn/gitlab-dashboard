import { useCallback, useRef, useState } from "react";
import { api, type Pipeline } from "./api";
import type { ChildState } from "./PipelineRow";

// useChildTree tracks which pipeline rows are expanded and their loaded children.
export function useChildTree() {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [children, setChildren] = useState<Record<string, ChildState>>({});
  const gen = useRef(0);
  const open = useRef(new Map<string, Pipeline>());

  const load = useCallback(async (p: Pipeline, g: number) => {
    setChildren((c) => ({ ...c, [p.key]: { ...c[p.key], loading: true } }));
    try {
      const items = await api.children(p);
      if (g !== gen.current) return;
      setChildren((c) => ({ ...c, [p.key]: { loading: false, items } }));
    } catch (e) {
      if (g !== gen.current) return;
      setChildren((c) => ({ ...c, [p.key]: { ...c[p.key], loading: false, error: (e as Error).message } }));
    }
  }, []);

  const toggle = useCallback(
    (p: Pipeline) => {
      setExpanded((cur) => {
        const next = new Set(cur);
        if (next.has(p.key)) next.delete(p.key);
        else next.add(p.key);
        return next;
      });
      if (open.current.has(p.key)) open.current.delete(p.key);
      else {
        open.current.set(p.key, p);
        load(p, gen.current);
      }
    },
    [load],
  );

  const reset = useCallback(() => {
    gen.current++;
    open.current.clear();
    setExpanded(new Set());
    setChildren({});
  }, []);

  const reloadOpen = useCallback(() => {
    const g = gen.current;
    return Promise.all([...open.current.values()].map((p) => load(p, g)));
  }, [load]);

  return { expanded, children, toggle, reset, reloadOpen };
}
