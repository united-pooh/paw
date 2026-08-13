export interface Usage {
  input: number;
  output: number;
  cache_read: number;
  cache_creation: number;
  total_context: number;
  cache_hit_rate: number;
}

export interface TimelineMarker {
  type: string;
  time: string;
  label: string;
  detail?: string;
  status?: string;
  usage?: Usage;
}

export interface TimelineRow {
  id: string;
  kind: string;
  stage_id?: string;
  stage_name?: string;
  agent_id?: string;
  parent_id?: string;
  name: string;
  display_name?: string;
  role?: string;
  provider?: string;
  model?: string;
  session_id?: string;
  invocation_index?: number;
  start_time: string;
  end_time: string;
  duration_ms: number;
  status: string;
  error?: string;
  usage: Usage;
  token_grand_total: number;
  token_share: number;
  calls: number;
  critical?: boolean;
  bottleneck?: boolean;
  markers?: TimelineMarker[];
}

export interface TraceEvent {
  seq: number;
  type: string;
  timestamp: string;
  data?: Record<string, unknown>;
}

export interface TraceSnapshot {
  pipeline: {
    id: string;
    name: string;
    start_time: string;
    end_time?: string;
    total: Usage;
    calls: number;
    status: string;
  };
  run_id: string;
  session_id?: string;
  workspace?: string;
  server_url?: string;
  timeline: {
    start_time?: string;
    end_time?: string;
    generated_at?: string;
    duration_ms: number;
    max_concurrency: number;
    overlap_ms: number;
    token_total: Usage;
    token_grand_total: number;
    error?: string;
    critical_path?: string[];
    bottleneck_id?: string;
    rows: TimelineRow[];
  };
  events: TraceEvent[];
}

export type PanelID = 'calls' | 'heatmap' | 'flame' | 'inspector' | 'events';
export type TimeRange = { startMS: number; endMS: number };
export type TokenPart = 'input' | 'cache_read' | 'cache_creation' | 'output';
