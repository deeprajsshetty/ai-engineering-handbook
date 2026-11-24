// Types mirror contracts/openapi.yaml. Unknown fields are ignored, per the
// API compatibility rules.

export type Status = "active" | "missing";
export type Freshness = "fresh" | "aging" | "stale";
export type JobStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";
export type RelationType =
  | "contains"
  | "composed_of"
  | "member_of"
  | "uses"
  | "depends_on"
  | "routes_to"
  | "connects_to";
export type Source = "simulated" | "measured";

export interface List<T> {
  items: T[];
  nextCursor?: string;
}

export interface Me {
  subject: string;
  tenantId: string;
  role: string;
  permissions: string[];
  authMode: string;
  environment: string;
  version: string;
}

export interface ConfigOption {
  value: string;
  label: string;
  description?: string;
}

export interface ConfigField {
  name: string;
  type: "string" | "integer" | "boolean" | "stringList" | string;
  required: boolean;
  default?: unknown;
  description: string;
  placeholder?: string;
  options?: ConfigOption[];
}

export interface Provider {
  name: string;
  displayName: string;
  description: string;
  simulated: boolean;
  requiresCredential: boolean;
  metricsSource: string;
  configFields: ConfigField[];
  resourceTypes: string[];
}

export interface Counts {
  added: number;
  changed: number;
  unchanged: number;
  reactivated: number;
  missing: number;
  edgesAdded: number;
  edgesUnchanged: number;
  edgesReactivated: number;
  edgesMissing: number;
}

export interface Job {
  id: string;
  connectionId: string;
  status: JobStatus;
  trigger: string;
  requestedBy: string;
  attempt: number;
  cancelRequested: boolean;
  progress: { phase: string; pages: number; resources: number; relationships: number; warnings: number };
  counts: Counts;
  complete: boolean;
  error?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface JobEvent {
  seq: number;
  at: string;
  level: "info" | "warn" | "error";
  message: string;
  details?: Record<string, unknown>;
}

export interface Connection {
  id: string;
  name: string;
  provider: string;
  config: Record<string, unknown>;
  discoveryIntervalMinutes: number;
  createdBy: string;
  createdAt: string;
  credential: { configured: boolean; ref?: string };
  simulated: boolean;
  metricsSource: string;
  lastJob?: Job;
}

export interface Resource {
  id: string;
  connectionId: string;
  provider: string;
  scope: string;
  externalId: string;
  type: string;
  domain: string;
  nativeType: string;
  name: string;
  region: string;
  labels: Record<string, string>;
  properties: Record<string, unknown>;
  status: Status;
  firstSeenAt: string;
  lastSeenAt: string;
  missingSince?: string;
  lastJobId: string;
  freshness: Freshness;
  ageSeconds: number;
  metricCapabilities: string[];
}

export interface Edge {
  id: string;
  sourceId: string;
  targetId: string;
  type: RelationType;
}

export interface Relationship extends Edge {
  status: Status;
  sourceName: string;
  sourceType: string;
  targetName: string;
  targetType: string;
  lastSeenAt: string;
}

export interface Graph {
  nodes: Resource[];
  edges: Edge[];
  truncated: boolean;
  cycleBackEdges: { acyclicTypes: number; dependencyTypes: number };
}

export interface Subgraph {
  rootId: string;
  nodes: Resource[];
  edges: Edge[];
  truncated: boolean;
}

export interface CountBy {
  key: string;
  count: number;
}

export interface Summary {
  resources: number;
  activeResources: number;
  missingResources: number;
  relationships: number;
  byType: CountBy[];
  byProvider: CountBy[];
  byRegion: CountBy[];
  relationshipTypes: CountBy[];
  lastSeenAt?: string;
}

export interface Sample {
  resourceId: string;
  metric: string;
  ts: string;
  value: number;
  source: Source;
  collector: string;
}

export interface MetricDefinition {
  name: string;
  unit: string;
  description: string;
  kind: string;
  resourceTypes: string[];
}

export interface ResourceMetric extends MetricDefinition {
  latest?: Sample;
}

export interface Point {
  ts: string;
  avg: number;
  min: number;
  max: number;
  count: number;
}

export interface Series {
  resourceId: string;
  metric: string;
  unit: string;
  description: string;
  from: string;
  to: string;
  stepSeconds: number;
  sources: Source[];
  simulated: boolean;
  points: Point[];
}

export interface CollectionRun {
  id: string;
  connectionId: string;
  trigger: string;
  status: "running" | "succeeded" | "failed";
  collector: string;
  resources: number;
  samples: number;
  error?: string;
  startedAt: string;
  finishedAt?: string;
}

export interface Change {
  id: number;
  jobId: string;
  resourceId: string;
  name: string;
  type: string;
  kind: "added" | "changed" | "reactivated" | "missing";
  at: string;
}

export interface FieldError {
  field: string;
  message: string;
}

export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
  code: string;
  requestId: string;
  errors?: FieldError[];
  extra?: Record<string, unknown>;
}

export interface Domain {
  name: string;
  displayName: string;
  description: string;
  order: number;
}

export interface ResourceTypeInfo {
  type: string;
  domain: string;
  displayName: string;
  description: string;
  icon: string;
  keyMetric?: string;
  keyProperties?: string[];
}

export interface ModuleInfo {
  name: string;
  description: string;
  providers: string[];
}

export interface Catalog {
  domains: Domain[];
  resourceTypes: ResourceTypeInfo[];
  metricDefinitions: MetricDefinition[];
  modules: ModuleInfo[];
}

export interface FacetCount {
  value: string;
  count: number;
}

export interface Facets {
  facets: Record<string, FacetCount[]>;
}

export interface FleetRow extends Resource {
  latest: Record<string, Sample>;
}

export interface Fleet {
  metrics: MetricDefinition[];
  items: FleetRow[];
  nextCursor?: string;
}
