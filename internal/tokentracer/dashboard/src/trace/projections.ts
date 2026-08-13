import type {
  TimelineMarker,
  TimelineRow,
  TraceEvent,
  TraceSnapshot,
  Usage,
} from './types';
import type { TimeRange } from './types';
import { tokenTotal } from './format';

export interface TraceFilters {
  scope: 'all' | 'stage' | 'agent';
  model: string | null;
  errorsOnly: boolean;
}

export interface ProjectedRow extends TimelineRow {
  startMS: number;
  endMS: number;
  throughput: number;
  inRange: boolean;
}

export interface HeatCell {
  rowID: string;
  column: number;
  startMS: number;
  endMS: number;
  usage: Usage;
  tokenTotal: number;
  status: string;
}

export interface HeatRow {
  rowID: string;
  label: string;
  cells: HeatCell[];
}

export interface FlameNode {
  id: string;
  rowID?: string;
  kind: 'run' | 'stage' | 'agent' | 'api_call' | 'event_cluster';
  label: string;
  status: string;
  startMS: number;
  endMS: number;
  durationMS: number;
  tokenTotal: number;
  usage: Usage;
  children: FlameNode[];
}

export interface CompactedEvent extends TraceEvent {
  hiddenCount: number;
  relatedRowID?: string;
}

export const defaultFilters: TraceFilters = {
  scope: 'all',
  model: null,
  errorsOnly: false,
};

const compactableEventTypes = ['cleanup', 'cascade'];

function parseMS(value: string): number {
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function clampInterval(startMS: number, endMS: number): [number, number] {
  if (endMS < startMS) {
    return [startMS, startMS];
  }
  return [startMS, endMS];
}

function usageFromParts(input: number, cacheRead: number, cacheCreation: number, output: number): Usage {
  const totalContext = input + cacheRead + cacheCreation;
  return {
    input,
    cache_read: cacheRead,
    cache_creation: cacheCreation,
    output,
    total_context: totalContext,
    cache_hit_rate: totalContext > 0 ? Math.round((cacheRead / totalContext) * 1000) / 10 : 0,
  };
}

export function addUsage(a: Usage, b: Usage): Usage {
  return usageFromParts(
    a.input + b.input,
    a.cache_read + b.cache_read,
    a.cache_creation + b.cache_creation,
    a.output + b.output,
  );
}

export function projectRows(rows: TimelineRow[], filters: TraceFilters, range: TimeRange | null): ProjectedRow[] {
  return rows
    .filter((row) => {
      if (filters.scope === 'stage' && row.kind !== 'stage') {
        return false;
      }
      if (filters.scope === 'agent' && row.kind !== 'agent') {
        return false;
      }
      if (filters.model !== null && row.model !== filters.model) {
        return false;
      }
      if (filters.errorsOnly && row.status !== 'failed') {
        return false;
      }
      return true;
    })
    .map((row) => {
      const [startMS, endMS] = clampInterval(parseMS(row.start_time), parseMS(row.end_time));
      const durationSec = Math.max(row.duration_ms / 1000, 0.001);
      return {
        ...row,
        startMS,
        endMS,
        throughput: row.token_grand_total / durationSec,
        inRange: range === null || (endMS >= range.startMS && startMS <= range.endMS),
      } as ProjectedRow;
    });
}

function hasChildren(row: TimelineRow, rows: TimelineRow[]): boolean {
  if (row.kind !== 'stage') {
    return false;
  }
  return rows.some((other) => other.kind === 'agent' && (other.parent_id === row.id || (row.stage_id !== undefined && other.stage_id === row.stage_id)));
}

function heatmapDomain(snapshot: TraceSnapshot, rows: TimelineRow[]): [number, number] {
  const start = snapshot.timeline.start_time !== undefined ? parseMS(snapshot.timeline.start_time) : 0;
  const end = snapshot.timeline.end_time !== undefined ? parseMS(snapshot.timeline.end_time) : 0;
  if (end > start) {
    return [start, end];
  }
  let min = Infinity;
  let max = -Infinity;
  for (const row of rows) {
    const [rowStart, rowEnd] = clampInterval(parseMS(row.start_time), parseMS(row.end_time));
    min = Math.min(min, rowStart);
    max = Math.max(max, rowEnd);
  }
  if (!Number.isFinite(min)) {
    return [0, 1];
  }
  return max > min ? [min, max] : [min, min + 1];
}

function markerTimeMS(marker: TimelineMarker, fallback: number): number {
  const parsed = parseMS(marker.time);
  return parsed > 0 ? parsed : fallback;
}

export function buildHeatmap(snapshot: TraceSnapshot, rows: TimelineRow[], columns: number): HeatRow[] {
  const columnCount = Math.max(1, Math.floor(columns));
  const [domainStart, domainEnd] = heatmapDomain(snapshot, rows);
  const bucketWidth = Math.max((domainEnd - domainStart) / columnCount, 1);
  const result: HeatRow[] = [];

  for (const row of rows) {
    if (row.kind === 'stage' && hasChildren(row, rows)) {
      continue;
    }
    const [rowStart, rowEnd] = clampInterval(parseMS(row.start_time), parseMS(row.end_time));
    const midpoint = rowStart + (rowEnd - rowStart) / 2;
    const usageMarkers = (row.markers ?? []).filter((marker) => marker.type === 'api_call' && marker.usage !== undefined);
    const cells = new Map<number, HeatCell>();

    if (usageMarkers.length > 0) {
      for (const marker of usageMarkers) {
        const time = markerTimeMS(marker, midpoint);
        const column = Math.min(columnCount - 1, Math.max(0, Math.floor((time - domainStart) / bucketWidth)));
        const existing = cells.get(column);
        const usage = marker.usage!;
        if (existing !== undefined) {
          existing.usage = addUsage(existing.usage, usage);
          existing.tokenTotal += tokenTotal(usage);
        } else {
          cells.set(column, {
            rowID: row.id,
            column,
            startMS: domainStart + column * bucketWidth,
            endMS: domainStart + (column + 1) * bucketWidth,
            usage,
            tokenTotal: tokenTotal(usage),
            status: row.status,
          });
        }
      }
    } else {
      const column = Math.min(columnCount - 1, Math.max(0, Math.floor((midpoint - domainStart) / bucketWidth)));
      cells.set(column, {
        rowID: row.id,
        column,
        startMS: domainStart + column * bucketWidth,
        endMS: domainStart + (column + 1) * bucketWidth,
        usage: row.usage,
        tokenTotal: row.token_grand_total,
        status: row.status,
      });
    }

    result.push({
      rowID: row.id,
      label: row.display_name ?? row.name,
      cells: Array.from(cells.values()).sort((a, b) => a.column - b.column),
    });
  }
  return result;
}

export function sumBucketUsage(rows: HeatRow[]): Usage {
  let total = usageFromParts(0, 0, 0, 0);
  for (const row of rows) {
    for (const cell of row.cells) {
      total = addUsage(total, cell.usage);
    }
  }
  return total;
}

function rowTimes(row: TimelineRow): [number, number] {
  return clampInterval(parseMS(row.start_time), parseMS(row.end_time));
}

function emptyUsage(): Usage {
  return usageFromParts(0, 0, 0, 0);
}

function eventRowIdentity(event: TraceEvent): { stageID?: string; agentID?: string } {
  const data = event.data ?? {};
  const stageID = typeof data.stage_id === 'string' ? data.stage_id : undefined;
  const agentID = typeof data.agent_id === 'string' ? data.agent_id : undefined;
  return { stageID, agentID };
}

export function buildFlameTree(snapshot: TraceSnapshot, mode: 'tokens' | 'duration'): FlameNode {
  const rows = snapshot.timeline.rows;
  const runStart = parseMS(snapshot.timeline.start_time ?? snapshot.pipeline.start_time);
  const runEnd = parseMS(snapshot.timeline.end_time ?? snapshot.pipeline.end_time ?? snapshot.timeline.start_time ?? snapshot.pipeline.start_time);
  const [startMS, endMS] = clampInterval(runStart, runEnd);

  const root: FlameNode = {
    id: 'run',
    kind: 'run',
    label: snapshot.pipeline.name,
    status: snapshot.pipeline.status,
    startMS,
    endMS,
    durationMS: Math.max(0, endMS - startMS),
    tokenTotal: snapshot.timeline.token_grand_total,
    usage: snapshot.timeline.token_total,
    children: [],
  };

  const stageRows = rows.filter((row) => row.kind === 'stage');
  const agentRows = rows.filter((row) => row.kind === 'agent');

  for (const stageRow of stageRows) {
    const [stageStart, stageEnd] = rowTimes(stageRow);
    const stageNode: FlameNode = {
      id: stageRow.id,
      rowID: stageRow.id,
      kind: 'stage',
      label: stageRow.name,
      status: stageRow.status,
      startMS: stageStart,
      endMS: stageEnd,
      durationMS: stageRow.duration_ms,
      tokenTotal: stageRow.token_grand_total,
      usage: stageRow.usage,
      children: [],
    };
    root.children.push(stageNode);

    const stageAgents = agentRows.filter(
      (row) => row.parent_id === stageRow.id || (stageRow.stage_id !== undefined && row.stage_id === stageRow.stage_id),
    );
    for (const agentRow of stageAgents) {
      stageNode.children.push(agentNode(agentRow, rows));
    }
  }

  const unattached = agentRows.filter(
    (row) => !root.children.some((stage) => stage.children.some((agent) => agent.rowID === row.id)),
  );
  for (const row of unattached) {
    root.children.push(agentNode(row, rows));
  }

  attachEventClusters(root, snapshot.events);
  return root;
}

function agentNode(row: TimelineRow, _rows: TimelineRow[]): FlameNode {
  const [startMS, endMS] = rowTimes(row);
  const node: FlameNode = {
    id: row.id,
    rowID: row.id,
    kind: 'agent',
    label: row.display_name ?? row.name,
    status: row.status,
    startMS,
    endMS,
    durationMS: row.duration_ms,
    tokenTotal: row.token_grand_total,
    usage: row.usage,
    children: [],
  };
  let apiIndex = 0;
  for (const marker of row.markers ?? []) {
    if (marker.type !== 'api_call') {
      continue;
    }
    const markerTime = markerTimeMS(marker, startMS);
    node.children.push({
      id: `${row.id}:api:${apiIndex++}`,
      rowID: row.id,
      kind: 'api_call',
      label: marker.detail !== undefined && marker.detail !== '' ? marker.detail : marker.label,
      status: marker.status ?? row.status,
      startMS: markerTime,
      endMS: markerTime,
      durationMS: 0,
      tokenTotal: marker.usage !== undefined ? tokenTotal(marker.usage) : 0,
      usage: marker.usage ?? emptyUsage(),
      children: [],
    });
  }
  return node;
}

function attachEventClusters(root: FlameNode, events: TraceEvent[]): void {
  const nonApi = events.filter((event) => event.type !== 'api_call');
  let clusterIndex = 0;
  let i = 0;
  while (i < nonApi.length) {
    const event = nonApi[i];
    const identity = eventRowIdentity(event);
    let j = i + 1;
    while (j < nonApi.length && nonApi[j].type === event.type && sameIdentity(eventRowIdentity(nonApi[j]), identity)) {
      j++;
    }
    const clusterEvents = nonApi.slice(i, j);
    const target = findEventTarget(root, identity);
    const usage = clusterEvents.reduce<Usage>((acc, ev) => {
      const data = ev.data ?? {};
      if (typeof data.usage === 'object' && data.usage !== null) {
        const u = data.usage as Partial<Usage>;
        return addUsage(acc, usageFromParts(u.input ?? 0, u.cache_read ?? 0, u.cache_creation ?? 0, u.output ?? 0));
      }
      return acc;
    }, emptyUsage());
    target.children.push({
      id: `events:${clusterIndex++}`,
      kind: 'event_cluster',
      label: `${event.type} × ${clusterEvents.length}`,
      status: event.type.includes('error') || event.type.includes('failed') ? 'failed' : 'completed',
      startMS: 0,
      endMS: 0,
      durationMS: 0,
      tokenTotal: tokenTotal(usage),
      usage,
      children: [],
    });
    i = j;
  }
}

function sameIdentity(a: { stageID?: string; agentID?: string }, b: { stageID?: string; agentID?: string }): boolean {
  return a.stageID === b.stageID && a.agentID === b.agentID;
}

function findEventTarget(root: FlameNode, identity: { stageID?: string; agentID?: string }): FlameNode {
  for (const stage of root.children) {
    if (stage.kind !== 'stage') {
      continue;
    }
    for (const agent of stage.children) {
      if (agent.kind !== 'agent') {
        continue;
      }
      const row = agent;
      const rowStageID = stage.rowID !== undefined ? stage.rowID.replace(/^stage:/, '') : undefined;
      const rowAgentID = row.rowID !== undefined ? row.rowID.split(':')[2] : undefined;
      if (identity.agentID !== undefined && identity.agentID === rowAgentID && identity.stageID === rowStageID) {
        return agent;
      }
    }
    const stageID = stage.rowID !== undefined ? stage.rowID.replace(/^stage:/, '') : undefined;
    if (identity.stageID !== undefined && identity.stageID === stageID && identity.agentID === undefined) {
      return stage;
    }
  }
  return root;
}

export function flattenFlame(root: FlameNode): FlameNode[] {
  const result: FlameNode[] = [];
  const stack: FlameNode[] = [root];
  while (stack.length > 0) {
    const node = stack.pop()!;
    result.push(node);
    for (let i = node.children.length - 1; i >= 0; i--) {
      stack.push(node.children[i]);
    }
  }
  return result;
}

export function compactEvents(events: TraceEvent[]): CompactedEvent[] {
  const result: CompactedEvent[] = [];
  for (const event of events) {
    const isCompactable = compactableEventTypes.some((type) => event.type === type || event.type.includes(type));
    const previous = result.length > 0 ? result[result.length - 1] : null;
    if (isCompactable && previous !== null && previous.type === event.type) {
      previous.hiddenCount += 1;
      continue;
    }
    const identity = eventRowIdentity(event);
    let relatedRowID: string | undefined;
    if (identity.agentID !== undefined && identity.stageID !== undefined) {
      relatedRowID = `agent:${identity.stageID}:${identity.agentID}:0`;
    } else if (identity.stageID !== undefined) {
      relatedRowID = `stage:${identity.stageID}`;
    }
    result.push({ ...event, hiddenCount: 0, relatedRowID });
  }
  return result;
}
