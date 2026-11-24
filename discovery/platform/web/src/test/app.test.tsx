import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { ApiError, describeError, request, setToken } from "../api/client";
import { formatValue, relativeTime } from "../lib/format";
import { parseHash } from "../lib/hooks";
import { layeredLayout } from "../lib/layout";
import { ResourcePanel } from "../pages/ResourceDetail";
import type { Edge } from "../api/types";

type Handler = (url: URL, init?: RequestInit) => unknown;

function mockFetch(routes: Record<string, Handler>) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, "http://localhost");
      calls.push({ url: url.pathname + url.search, init });
      const key = Object.keys(routes).find((k) => {
        const [m, p] = k.split(" ");
        return m === (init?.method ?? "GET") && new RegExp(`^${p}$`).test(url.pathname);
      });
      if (!key) return new Response(JSON.stringify({ code: "not_found", status: 404, title: "Not Found", requestId: "r1", type: "x" }), { status: 404 });
      const body = routes[key]!(url, init);
      if (body instanceof Response) return body;
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  return calls;
}

const me = { subject: "demo-admin", tenantId: "demo", role: "admin", permissions: ["connections:read", "topology:read", "metrics:read", "discovery:run", "connections:write", "metrics:collect"], authMode: "dev", environment: "development", version: "dev" };
const now = new Date().toISOString();
const resource = {
  id: "res_000000000000000000000001", connectionId: "con_1", provider: "fixture", scope: "sim-acct-0001", externalId: "i-east-web-01",
  type: "compute.instance", nativeType: "sim:compute:instance", name: "i-east-web-01", region: "sim-east-1",
  labels: { app: "storefront", simulated: "true" }, properties: { vcpu: 2 }, status: "active", firstSeenAt: now, lastSeenAt: now,
  lastJobId: "job_1234567890123456", freshness: "fresh", ageSeconds: 3, metricCapabilities: ["cpu.utilization"],
};

beforeEach(() => {
  window.location.hash = "";
  try {
    sessionStorage.clear();
  } catch {
    /* ignore */
  }
  setToken(null);
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("format", () => {
  it("formats units", () => {
    expect(formatValue(42.123, "%")).toBe("42.1 %");
    expect(formatValue(1536, "By")).toBe("1.5 KiB");
    expect(formatValue(2.5e6, "By/s")).toBe("2.4 MiB/s");
    expect(formatValue(120, "{request}/s")).toBe("120 requests/s");
    expect(formatValue(0.25, "{core}")).toBe("0.250 cores");
    expect(formatValue(Number.NaN, "%")).toBe("—");
    expect(relativeTime(new Date(Date.now() - 120e3).toISOString())).toBe("2m ago");
  });
  it("parses hash routes", () => {
    const r = parseHash("#/resources/res_1?type=compute.instance");
    expect(r.section).toBe("resources");
    expect(r.id).toBe("res_1");
    expect(r.params.get("type")).toBe("compute.instance");
    expect(parseHash("").section).toBe("overview");
  });
});

describe("layeredLayout", () => {
  it("places nodes by containment depth and ignores dependency edges", () => {
    const edges: Edge[] = [
      { id: "1", sourceId: "vpc", targetId: "subnet", type: "contains" },
      { id: "2", sourceId: "subnet", targetId: "vm", type: "contains" },
      { id: "3", sourceId: "vm", targetId: "db", type: "depends_on" },
      { id: "4", sourceId: "vpc", targetId: "vm", type: "contains" },
    ];
    const l = layeredLayout(["db", "vm", "subnet", "vpc"], edges, (id) => id);
    expect(l.nodes.get("vpc")!.layer).toBe(0);
    expect(l.nodes.get("db")!.layer).toBe(0); // no containment parent
    expect(l.nodes.get("subnet")!.layer).toBe(1);
    expect(l.nodes.get("vm")!.layer).toBe(2); // longest path, not shortest
    expect(l.layers).toBe(3);
  });
});

describe("api client", () => {
  it("sends the bearer token and surfaces RFC 9457 problems", async () => {
    setToken("dev-admin");
    const calls = mockFetch({
      "GET /api/v1/x": () =>
        new Response(JSON.stringify({ type: "urn:x", title: "Bad Request", status: 400, code: "validation_failed", requestId: "req_9", errors: [{ field: "name", message: "is required" }] }), { status: 400 }),
    });
    const err = await request("GET", "/api/v1/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).problem?.code).toBe("validation_failed");
    expect(describeError(err)).toContain("name: is required");
    expect(describeError(err)).toContain("req_9");
    expect((calls[0]!.init?.headers as Record<string, string>).Authorization).toBe("Bearer dev-admin");
  });
});

describe("App", () => {
  it("signs in with a dev token and shows the overview with a simulated-data notice", async () => {
    mockFetch({
      "GET /api/v1/me": (_u, init) =>
        (init?.headers as Record<string, string>).Authorization === "Bearer dev-admin"
          ? me
          : new Response(JSON.stringify({ code: "unauthenticated", status: 401 }), { status: 401 }),
      "GET /api/v1/topology/summary": () => ({ resources: 32, activeResources: 31, missingResources: 1, relationships: 64, byType: [{ key: "compute.instance", count: 10 }], byProvider: [], byRegion: [], relationshipTypes: [], lastSeenAt: now }),
      "GET /api/v1/connections": () => ({ items: [{ id: "con_1", name: "Demo", provider: "fixture", simulated: true, config: {}, credential: { configured: false }, metricsSource: "simulated", discoveryIntervalMinutes: 0, createdBy: "seed", createdAt: now }] }),
      "GET /api/v1/discovery-jobs": () => ({ items: [] }),
    });
    render(<App />);
    expect(await screen.findByText(/Development sign-in/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { name: "Overview" })).toBeInTheDocument();
    expect(await screen.findByText("31")).toBeInTheDocument();
    expect(screen.getByText(/generated locally and are not\s+real measurements/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "compute.instance" })).toHaveAttribute("href", "#/resources?type=compute.instance");
    expect(screen.getByText(/tenant/)).toHaveTextContent("tenant demo");
  });

  it("shows an error when the token is rejected", async () => {
    mockFetch({ "GET /api/v1/me": () => new Response(JSON.stringify({ code: "unauthenticated", status: 401, detail: "missing or invalid bearer token", requestId: "r" }), { status: 401 }) });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("missing or invalid bearer token");
  });
});

describe("ResourcePanel", () => {
  it("renders attributes, relationships and simulated metrics with units", async () => {
    setToken("dev-admin");
    mockFetch({
      "GET /api/v1/resources/res_000000000000000000000001": () => resource,
      "GET /api/v1/relationships": () => ({ items: [{ id: "rel_1", sourceId: "res_lb", targetId: resource.id, type: "routes_to", status: "active", sourceName: "lb-east-web", sourceType: "network.load_balancer", targetName: resource.name, targetType: resource.type, lastSeenAt: now }] }),
      "GET /api/v1/resources/res_000000000000000000000001/metrics": () => ({
        items: [{ name: "cpu.utilization", unit: "%", description: "CPU utilization", kind: "gauge", resourceTypes: ["compute.instance"], latest: { resourceId: resource.id, metric: "cpu.utilization", ts: now, value: 37.25, source: "simulated", collector: "simulated-generator" } }],
      }),
      "GET /api/v1/resources/res_000000000000000000000001/metrics/cpu.utilization": () => ({
        resourceId: resource.id, metric: "cpu.utilization", unit: "%", description: "", from: now, to: now, stepSeconds: 60, sources: ["simulated"], simulated: true,
        points: [0, 1, 2].map((i) => ({ ts: new Date(Date.now() - (3 - i) * 60e3).toISOString(), avg: 30 + i, min: 29 + i, max: 31 + i, count: 1 })),
      }),
    });
    render(<ResourcePanel id={resource.id} />);
    expect(await screen.findByRole("link", { name: "i-east-web-01" })).toBeInTheDocument();
    expect(screen.getByText("i-east-web-01", { selector: "code" })).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "lb-east-web" })).toBeInTheDocument();
    expect(await screen.findByText("37.3 %")).toBeInTheDocument();
    expect(screen.getAllByText("simulated").length).toBeGreaterThanOrEqual(2);
    const chart = await screen.findByRole("img");
    await waitFor(() => expect(within(chart).getByText(/cpu.utilization: 3 points/)).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Show data" }));
    expect(screen.getAllByRole("row")).toHaveLength(4);
  });
});

// ------------------------------------------------------------ new UX surfaces

const catalog = {
  domains: [
    { name: "compute", displayName: "Compute", description: "VMs", order: 10 },
    { name: "database", displayName: "Databases", description: "Any engine", order: 30 },
  ],
  resourceTypes: [
    { type: "compute.instance", domain: "compute", displayName: "Instance", description: "", icon: "server", keyMetric: "cpu.utilization" },
    { type: "database.instance", domain: "database", displayName: "Database server", description: "", icon: "database", keyMetric: "db.connections" },
    { type: "database.tablespace", domain: "database", displayName: "Tablespace", description: "", icon: "tablespace" },
  ],
  metricDefinitions: [],
  modules: [],
};

describe("Explorer", () => {
  it("renders facets with counts and turns a facet click into a shareable URL filter", async () => {
    setToken("dev-admin");
    const calls = mockFetch({
      "GET /api/v1/catalog": () => catalog,
      "GET /api/v1/resources": () => ({ items: [{ ...resource, domain: "compute" }] }),
      "GET /api/v1/resources/facets": () => ({
        facets: { domain: [{ value: "compute", count: 11 }, { value: "database", count: 10 }], type: [{ value: "compute.instance", count: 11 }], engine: [{ value: "oracle", count: 6 }], status: [{ value: "active", count: 21 }] },
      }),
    });
    const { Resources } = await import("../pages/Resources");
    const { AppProvider } = await import("../lib/context");
    render(
      <AppProvider>
        <Resources params={new URLSearchParams("domain=database")} />
      </AppProvider>,
    );
    expect(await screen.findByRole("link", { name: "i-east-web-01" })).toBeInTheDocument();
    const domain = screen.getByRole("group", { name: "Domain" });
    expect(await within(domain).findByText("Databases")).toBeInTheDocument();
    expect(within(domain).getByRole("checkbox", { name: /Databases/ })).toBeChecked();
    expect(screen.getByRole("button", { name: "Remove filter domain database" })).toBeInTheDocument();
    expect(calls.some((c) => c.url.startsWith("/api/v1/resources/facets?domain=database"))).toBe(true);
    await userEvent.click(within(screen.getByRole("group", { name: "Engine" })).getByRole("checkbox", { name: /oracle/ }));
    await waitFor(() => expect(window.location.hash).toBe("#/resources?domain=database&engine=oracle"));
  });
});

describe("CommandPalette", () => {
  it("searches resources and pages and navigates with the keyboard", async () => {
    setToken("dev-admin");
    mockFetch({
      "GET /api/v1/catalog": () => catalog,
      "GET /api/v1/resources": (u) => ({ items: u.searchParams.get("q") === "web" ? [resource] : [] }),
    });
    const { CommandPalette } = await import("../components/CommandPalette");
    const { AppProvider } = await import("../lib/context");
    const onClose = vi.fn();
    render(
      <AppProvider>
        <CommandPalette open onClose={onClose} commands={[{ id: "x", label: "Go to Databases", icon: "database", run: () => {} }]} />
      </AppProvider>,
    );
    const box = screen.getByRole("combobox", { name: "Search pages and resources" });
    expect(box).toHaveFocus();
    await userEvent.type(box, "web");
    const option = await screen.findByRole("option", { name: /i-east-web-01/ });
    expect(option).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{Enter}");
    expect(window.location.hash).toBe(`#/resources/${resource.id}`);
    expect(onClose).toHaveBeenCalled();
  });
});

describe("Connections form", () => {
  it("renders provider-declared fields, including engine options, and sends typed config", async () => {
    setToken("dev-admin");
    let sent: Record<string, unknown> | undefined;
    mockFetch({
      "GET /api/v1/connections": () => ({ items: [] }),
      "GET /api/v1/providers": () => ({
        items: [
          {
            name: "database", displayName: "Database server", description: "Any engine", simulated: false, requiresCredential: true, metricsSource: "measured", resourceTypes: [],
            configFields: [
              { name: "engine", type: "string", required: true, description: "Database engine.", options: [{ value: "oracle", label: "Oracle Database", description: "SELECT_CATALOG_ROLE" }, { value: "postgresql", label: "PostgreSQL" }] },
              { name: "host", type: "string", required: true, description: "Host", placeholder: "db1.internal.example" },
              { name: "port", type: "integer", required: false, description: "Port" },
            ],
          },
        ],
      }),
      "POST /api/v1/connections": (_u, init) => {
        sent = JSON.parse(String(init?.body)) as Record<string, unknown>;
        return { id: "con_2", name: sent.name, provider: "database", simulated: false, config: {}, credential: { configured: true }, metricsSource: "measured", discoveryIntervalMinutes: 0, createdBy: "x", createdAt: now };
      },
    });
    const { Connections } = await import("../pages/Connections");
    render(<Connections me={me} params={new URLSearchParams("provider=database")} />);
    const engine = await screen.findByRole("combobox", { name: /engine/ });
    expect(within(engine).getByRole("option", { name: "Oracle Database" })).toBeInTheDocument();
    expect(screen.getByText(/SELECT_CATALOG_ROLE/)).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "ERP");
    await userEvent.type(screen.getByRole("textbox", { name: /host/ }), "db1.internal.example");
    await userEvent.type(screen.getByRole("spinbutton", { name: /port/ }), "1521");
    await userEvent.type(screen.getByPlaceholderText(/env:OBS_SECRET_/), "env:OBS_SECRET_ERP");
    await userEvent.click(screen.getByRole("button", { name: "Create connection" }));
    await waitFor(() => expect(sent).toBeDefined());
    expect(sent).toMatchObject({ provider: "database", credentialRef: "env:OBS_SECRET_ERP", config: { engine: "oracle", host: "db1.internal.example", port: 1521 } });
  });
});

describe("Databases", () => {
  it("shows every engine in one fleet table and never invents missing values", async () => {
    setToken("dev-admin");
    const sample = (metric: string, value: number, source = "measured") => ({ resourceId: "r", metric, ts: now, value, source, collector: "c" });
    const def = (name: string, unit: string) => ({ name, unit, description: "", kind: "gauge", resourceTypes: [] });
    mockFetch({
      "GET /api/v1/catalog": () => catalog,
      "GET /api/v1/resources/facets": () => ({ facets: { engine: [{ value: "oracle", count: 6 }, { value: "postgresql", count: 4 }], type: [{ value: "database.instance", count: 2 }] } }),
      "GET /api/v1/fleet": (u) =>
        u.searchParams.get("type") === "database.instance"
          ? {
              metrics: [def("db.connections", "{connection}"), def("cpu.utilization", "%")],
              items: [
                { ...resource, id: "res_ora", type: "database.instance", name: "Oracle Database ERPCDB1", labels: { engine: "oracle" }, properties: { engineVersion: "19c" }, latest: { "db.connections": sample("db.connections", 42), "cpu.utilization": sample("cpu.utilization", 41) } },
                { ...resource, id: "res_pg", type: "database.instance", name: "PostgreSQL 127.0.0.1", labels: { engine: "postgresql" }, properties: {}, latest: { "db.connections": sample("db.connections", 3) } },
              ],
            }
          : { metrics: [], items: [{ ...resource, id: "res_ts", type: "database.tablespace", name: "ERP_DATA", labels: { engine: "oracle" }, latest: { "db.storage.utilization": sample("db.storage.utilization", 80.5) } }] },
    });
    const { Databases } = await import("../pages/Databases");
    const { AppProvider } = await import("../lib/context");
    render(
      <AppProvider>
        <Databases />
      </AppProvider>,
    );
    const ora = (await screen.findByRole("link", { name: "Oracle Database ERPCDB1" })).closest("tr")!;
    expect(within(ora).getByText("41.0 %")).toBeInTheDocument();
    const pg = screen.getByRole("link", { name: "PostgreSQL 127.0.0.1" }).closest("tr")!;
    expect(within(pg).getByText("—")).toBeInTheDocument(); // PostgreSQL reports no host CPU
    expect(within(pg).getByText("measured")).toBeInTheDocument();
    expect(await screen.findByRole("meter", { name: "ERP_DATA utilization" })).toHaveAttribute("aria-valuenow", "80.5");
    expect(screen.getByRole("radio", { name: /Oracle Database/ })).toBeInTheDocument();
  });
});
