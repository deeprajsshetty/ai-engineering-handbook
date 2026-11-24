import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Catalog, ResourceTypeInfo } from "../api/types";

// ---------------------------------------------------------------- storage

/** Per-viewer conveniences only; storage may be unavailable (private mode). */
function load(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function save(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* ignore */
  }
}

// ------------------------------------------------------------- time range

export const TIME_RANGES = [
  { key: "15m", label: "Last 15 minutes", short: "15m", ms: 15 * 60e3 },
  { key: "1h", label: "Last 1 hour", short: "1h", ms: 3600e3 },
  { key: "6h", label: "Last 6 hours", short: "6h", ms: 6 * 3600e3 },
  { key: "24h", label: "Last 24 hours", short: "24h", ms: 24 * 3600e3 },
  { key: "7d", label: "Last 7 days", short: "7d", ms: 7 * 24 * 3600e3 },
] as const;
export type TimeRange = (typeof TIME_RANGES)[number];

// ------------------------------------------------------------------ theme

export type ThemePref = "system" | "light" | "dark";

function applyTheme(t: ThemePref) {
  if (typeof document === "undefined") return;
  if (t === "system") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", t);
}

// ---------------------------------------------------------------- context

interface AppContextValue {
  catalog?: Catalog;
  typeInfo: (type: string) => ResourceTypeInfo;
  domainName: (domain: string) => string;
  range: TimeRange;
  setRange: (key: string) => void;
  theme: ThemePref;
  setTheme: (t: ThemePref) => void;
}

const fallbackType = (type: string): ResourceTypeInfo => ({
  type,
  domain: "other",
  displayName: type,
  description: "",
  icon: "other",
});

const AppContext = createContext<AppContextValue>({
  typeInfo: fallbackType,
  domainName: (d) => d,
  range: TIME_RANGES[1],
  setRange: () => {},
  theme: "system",
  setTheme: () => {},
});

export function AppProvider({ children }: { children: ReactNode }) {
  const [catalog, setCatalog] = useState<Catalog>();
  const [rangeKey, setRangeKey] = useState(() => load("obs.range") ?? "1h");
  const [theme, setThemeState] = useState<ThemePref>(() => (load("obs.theme") as ThemePref | null) ?? "system");

  useEffect(() => {
    api.catalog().then(setCatalog, () => setCatalog(undefined));
  }, []);
  useEffect(() => applyTheme(theme), [theme]);

  const setRange = useCallback((k: string) => {
    setRangeKey(k);
    save("obs.range", k);
  }, []);
  const setTheme = useCallback((t: ThemePref) => {
    setThemeState(t);
    save("obs.theme", t);
  }, []);

  const value = useMemo<AppContextValue>(() => {
    const types = new Map(catalog?.resourceTypes.map((t) => [t.type, t]));
    const domains = new Map(catalog?.domains.map((d) => [d.name, d.displayName]));
    return {
      catalog,
      typeInfo: (t) => types.get(t) ?? fallbackType(t),
      domainName: (d) => domains.get(d) ?? d,
      range: TIME_RANGES.find((r) => r.key === rangeKey) ?? TIME_RANGES[1],
      setRange,
      theme,
      setTheme,
    };
  }, [catalog, rangeKey, setRange, theme, setTheme]);

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useApp(): AppContextValue {
  return useContext(AppContext);
}
