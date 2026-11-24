import { useEffect, useMemo, useState } from "react";
import { api, ApiError, getToken, setToken } from "./api/client";
import type { Me } from "./api/types";
import { href, useRoute } from "./lib/hooks";
import { AppProvider, TIME_RANGES, useApp, type ThemePref } from "./lib/context";
import { Loading } from "./components/ui";
import { Icon } from "./components/Icon";
import { CommandPalette, type PaletteCommand } from "./components/CommandPalette";
import { SignIn } from "./pages/SignIn";
import { Overview } from "./pages/Overview";
import { Connections } from "./pages/Connections";
import { Jobs } from "./pages/Jobs";
import { Topology } from "./pages/Topology";
import { Resources } from "./pages/Resources";
import { ResourceDetail } from "./pages/ResourceDetail";
import { Databases } from "./pages/Databases";

const NAV = [
  {
    group: "Observe",
    items: [
      { section: "overview", label: "Overview", icon: "overview" },
      { section: "resources", label: "Explorer", icon: "explorer" },
      { section: "topology", label: "Topology map", icon: "topology" },
      { section: "databases", label: "Databases", icon: "database" },
    ],
  },
  {
    group: "Configure",
    items: [
      { section: "connections", label: "Connections", icon: "connections" },
      { section: "jobs", label: "Discovery jobs", icon: "jobs" },
    ],
  },
];

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [checking, setChecking] = useState(() => getToken() !== null);

  useEffect(() => {
    if (!getToken()) return;
    api
      .me()
      .then(setMe)
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) setToken(null);
      })
      .finally(() => setChecking(false));
  }, []);

  if (checking) return <Loading label="Checking session" />;
  if (!me) return <SignIn onSignedIn={setMe} />;
  return (
    <AppProvider>
      <Shell
        me={me}
        onSignOut={() => {
          setToken(null);
          setMe(null);
        }}
      />
    </AppProvider>
  );
}

const THEMES: { value: ThemePref; icon: string; label: string }[] = [
  { value: "system", icon: "monitor", label: "System theme" },
  { value: "light", icon: "sun", label: "Light theme" },
  { value: "dark", icon: "moon", label: "Dark theme" },
];

function Shell({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const route = useRoute();
  const { range, setRange, theme, setTheme } = useApp();
  const [palette, setPalette] = useState(false);

  // Ctrl/Cmd+K anywhere, or "/" outside text fields, opens the palette.
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      if ((e.key === "k" || e.key === "K") && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setPalette((p) => !p);
      } else if (e.key === "/" && !typing) {
        e.preventDefault();
        setPalette(true);
      }
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);

  const commands = useMemo<PaletteCommand[]>(
    () => [
      ...NAV.flatMap((g) =>
        g.items.map((n) => ({ id: `nav-${n.section}`, label: `Go to ${n.label}`, icon: n.icon, run: () => (window.location.hash = href(n.section)) })),
      ),
      { id: "new-conn", label: "Add a connection", hint: "cloud, Kubernetes or database", icon: "connections", run: () => (window.location.hash = href("connections")) },
      { id: "add-db", label: "Add a database (PostgreSQL, Oracle)", icon: "database", run: () => (window.location.hash = href("connections", undefined, { provider: "database" })) },
      ...TIME_RANGES.map((r) => ({ id: `range-${r.key}`, label: `Time range: ${r.label}`, icon: "clock", run: () => setRange(r.key) })),
      ...THEMES.map((t) => ({ id: `theme-${t.value}`, label: `Use ${t.label.toLowerCase()}`, icon: t.icon, run: () => setTheme(t.value) })),
    ],
    [setRange, setTheme],
  );

  let page;
  switch (route.section) {
    case "connections":
      page = <Connections me={me} params={route.params} />;
      break;
    case "jobs":
      page = <Jobs id={route.id} me={me} />;
      break;
    case "topology":
      page = <Topology params={route.params} />;
      break;
    case "resources":
      page = route.id ? <ResourceDetail id={route.id} /> : <Resources params={route.params} />;
      break;
    case "databases":
      page = <Databases />;
      break;
    default:
      page = <Overview />;
  }
  const nextTheme = THEMES[(THEMES.findIndex((t) => t.value === theme) + 1) % THEMES.length]!;
  const currentTheme = THEMES.find((t) => t.value === theme) ?? THEMES[0]!;

  return (
    <div className="shell">
      <a className="skip" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <a className="brand" href={href("overview")}>
          <span className="brand-mark" aria-hidden="true" />
          Observability Platform
        </a>
        <button type="button" className="search-trigger" onClick={() => setPalette(true)} aria-label="Search (Ctrl+K)">
          <Icon name="search" size={16} />
          <span>Search resources, pages…</span>
          <kbd>⌘K</kbd>
        </button>
        <span className="spacer" />
        <label className="time-picker" title="Time range for all charts">
          <Icon name="clock" size={16} />
          <span className="sr-only">Time range</span>
          <select value={range.key} onChange={(e) => setRange(e.target.value)}>
            {TIME_RANGES.map((r) => (
              <option key={r.key} value={r.key}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="icon-btn"
          onClick={() => setTheme(nextTheme.value)}
          aria-label={`${currentTheme.label}; switch to ${nextTheme.label.toLowerCase()}`}
          title={`${currentTheme.label} (click for ${nextTheme.label.toLowerCase()})`}
        >
          <Icon name={currentTheme.icon} />
        </button>
        <span className="badge badge-warn" title="Server is running with development authentication">
          {me.authMode === "dev" ? "dev auth" : me.authMode} · {me.environment}
        </span>
        <details className="user-menu">
          <summary aria-label="Account">{me.subject.slice(0, 1).toUpperCase()}</summary>
          <div className="user-menu-pop">
            <div>
              <strong>{me.subject}</strong>
            </div>
            <div className="muted small">
              tenant {me.tenantId} · {me.role}
            </div>
            <button type="button" className="btn btn-small" onClick={onSignOut}>
              Sign out
            </button>
          </div>
        </details>
      </header>
      <nav className="sidenav" aria-label="Main">
        {NAV.map((g) => (
          <div key={g.group} className="nav-group">
            <div className="nav-group-label">{g.group}</div>
            {g.items.map((n) => (
              <a key={n.section} href={href(n.section)} aria-current={route.section === n.section ? "page" : undefined}>
                <Icon name={n.icon} />
                <span>{n.label}</span>
              </a>
            ))}
          </div>
        ))}
      </nav>
      <main id="main" className="main" tabIndex={-1}>
        {page}
      </main>
      <CommandPalette open={palette} onClose={() => setPalette(false)} commands={commands} />
    </div>
  );
}
