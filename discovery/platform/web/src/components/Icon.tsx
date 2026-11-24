import { useApp } from "../lib/context";

// Small stroke icons (24-unit grid, currentColor) so domains are recognisable
// at a glance. Unknown icon keys fall back to the domain icon, then "other",
// so a type added by a new backend module renders without UI changes.
const PATHS: Record<string, string> = {
  compute: "M4 5h16v10H4zM8 19h8M12 15v4",
  server: "M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01",
  containers: "M12 3l8 4.5v9L12 21l-8-4.5v-9zM12 12l8-4.5M12 12v9M12 12L4 7.5",
  cluster: "M12 3l8 4.5v9L12 21l-8-4.5v-9zM12 12l8-4.5M12 12v9M12 12L4 7.5",
  node: "M5 5h14v14H5zM9 9h6v6H9z",
  pod: "M12 4l7 4v8l-7 4-7-4V8z",
  workload: "M4 6h16M4 12h16M4 18h10",
  namespace: "M4 4h16v16H4zM4 9h16",
  service: "M5 12h14M12 5l7 7-7 7",
  database: "M12 4c4.4 0 8 1.3 8 3s-3.6 3-8 3-8-1.3-8-3 3.6-3 8-3zM4 7v10c0 1.7 3.6 3 8 3s8-1.3 8-3V7M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  "database-logical": "M12 5c3.3 0 6 1 6 2.2S15.3 9.5 12 9.5 6 8.5 6 7.2 8.7 5 12 5zM6 7.2v9.6c0 1.2 2.7 2.2 6 2.2s6-1 6-2.2V7.2",
  tablespace: "M4 6h16v12H4zM4 10h16M4 14h16M10 6v12",
  network: "M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c2.5 2.5 3.5 5.5 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-5.5-3.5-9s1-6.5 3.5-9z",
  subnet: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
  "load-balancer": "M12 4v5M12 9l-6 6M12 9l6 6M12 9v6M4 17h4v3H4zM10 17h4v3h-4zM16 17h4v3h-4z",
  storage: "M4 7l8-4 8 4-8 4zM4 12l8 4 8-4M4 17l8 4 8-4",
  bucket: "M5 6h14l-2 14H7zM4 6h16",
  cache: "M13 3L5 14h6l-1 7 8-11h-6z",
  serverless: "M7 4h4l-3 8h4l-5 8 1-6H6z",
  function: "M9 4c-2 0-3 1-3 3v3l-2 2 2 2v3c0 2 1 3 3 3M15 4c2 0 3 1 3 3v3l2 2-2 2v3c0 2-1 3-3 3",
  other: "M12 3a9 9 0 100 18 9 9 0 000-18zM12 8v5M12 16h.01",
  // UI icons
  search: "M11 4a7 7 0 100 14 7 7 0 000-14zM20 20l-4-4",
  overview: "M4 4h7v7H4zM13 4h7v4h-7zM13 10h7v10h-7zM4 13h7v7H4z",
  explorer: "M4 6h16M7 12h10M10 18h4",
  topology: "M6 6a2 2 0 100 .01M18 6a2 2 0 100 .01M12 18a2 2 0 100 .01M7.5 7.5l3.5 9M16.5 7.5l-3.5 9M8 6h8",
  connections: "M10 14a4 4 0 005.7 0l3-3a4 4 0 00-5.7-5.7l-1 1M14 10a4 4 0 00-5.7 0l-3 3a4 4 0 005.7 5.7l1-1",
  jobs: "M4 12a8 8 0 1016 0 8 8 0 00-16 0zM12 8v4l3 2",
  sun: "M12 8a4 4 0 100 8 4 4 0 000-8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
  moon: "M20 14.5A8 8 0 019.5 4 8 8 0 1020 14.5z",
  monitor: "M4 5h16v11H4zM9 20h6M12 16v4",
  clock: "M12 4a8 8 0 100 16 8 8 0 000-16zM12 8v4l3 2",
  close: "M6 6l12 12M18 6L6 18",
};

export function Icon({ name, size = 18, title }: { name: string; size?: number; title?: string }) {
  const d = PATHS[name] ?? PATHS.other;
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={title ? undefined : true}
      role={title ? "img" : undefined}
    >
      {title && <title>{title}</title>}
      <path d={d} />
    </svg>
  );
}

/** TypeIcon shows the catalog icon for a resource type, tinted by domain. */
export function TypeIcon({ type, size = 18 }: { type: string; size?: number }) {
  const { typeInfo, domainName } = useApp();
  const t = typeInfo(type);
  const name = PATHS[t.icon] ? t.icon : PATHS[t.domain] ? t.domain : "other";
  return (
    <span className={`type-icon dom-${t.domain}`} title={`${t.displayName} · ${domainName(t.domain)}`}>
      <Icon name={name} size={size} />
    </span>
  );
}
