import { useCallback, useEffect, useRef, useState } from "react";

export interface AsyncState<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean;
  reload: () => void;
}

/**
 * useAsync runs fn when deps change and, if intervalMs is set, polls it.
 * Results of superseded requests are ignored. The previous data stays
 * visible while a refresh is in flight. Polling pauses while the document is
 * hidden to avoid background load.
 */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[], intervalMs?: number): AsyncState<T> {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<{ data?: T; error?: unknown; key?: string }>({});
  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  });
  const reload = useCallback(() => setTick((t) => t + 1), []);
  const key = JSON.stringify([...deps, tick]);

  useEffect(() => {
    let live = true;
    fnRef
      .current()
      .then((d) => {
        if (live) setState({ data: d, error: undefined, key });
      })
      .catch((e: unknown) => {
        if (live) setState((s) => ({ data: s.data, error: e, key }));
      });
    return () => {
      live = false;
    };
  }, [key]);

  useEffect(() => {
    if (!intervalMs) return;
    const id = setInterval(() => {
      if (typeof document === "undefined" || document.visibilityState !== "hidden") setTick((t) => t + 1);
    }, intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);

  return { data: state.data, error: state.error, loading: state.key !== key, reload };
}

/** Route is the parsed location hash: #/section/id?query */
export interface Route {
  section: string;
  id?: string;
  params: URLSearchParams;
}

export function parseHash(hash: string): Route {
  const raw = hash.replace(/^#\/?/, "");
  const [path = "", query = ""] = raw.split("?");
  const [section = "overview", id] = path.split("/").filter(Boolean);
  return { section, id, params: new URLSearchParams(query) };
}

/** useRoute is a minimal hash router; no dependency is needed for five views. */
export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseHash(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parseHash(window.location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function href(section: string, id?: string, params?: Record<string, string | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params ?? {})) if (v) q.set(k, v);
  const s = q.toString();
  return `#/${section}${id ? `/${id}` : ""}${s ? `?${s}` : ""}`;
}
