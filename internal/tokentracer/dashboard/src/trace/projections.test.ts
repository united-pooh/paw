import { fixtureSnapshot, fixtureRange } from '../test/fixtures';
import {
  buildFlameTree,
  buildHeatmap,
  compactEvents,
  defaultFilters,
  flattenFlame,
  projectRows,
  sumBucketUsage,
} from './projections';

test('builds pixel-bounded heat buckets and preserves usage totals', () => {
  const buckets = buildHeatmap(fixtureSnapshot, fixtureSnapshot.timeline.rows, 64);
  expect(buckets).toHaveLength(3);
  expect(Math.max(...buckets.map((row) => row.cells.length))).toBeLessThanOrEqual(64);
  expect(sumBucketUsage(buckets)).toEqual(fixtureSnapshot.timeline.token_total);
});

test('builds a continuous run-stage-agent-call flame tree', () => {
  const root = buildFlameTree(fixtureSnapshot, 'tokens');
  expect(root.kind).toBe('run');
  expect(root.children.flatMap((stage) => stage.children).some((node) => node.kind === 'agent')).toBe(true);
  expect(flattenFlame(root).some((node) => node.kind === 'api_call')).toBe(true);
});

test('selection range annotates rather than removes rows', () => {
  const result = projectRows(fixtureSnapshot.timeline.rows, defaultFilters, fixtureRange);
  expect(result).toHaveLength(fixtureSnapshot.timeline.rows.length);
  expect(result.some((row) => row.inRange === false)).toBe(true);
});

test('compacts consecutive cleanup events and reports the hidden count', () => {
  expect(compactEvents(fixtureSnapshot.events)).toEqual(
    expect.arrayContaining([expect.objectContaining({ hiddenCount: 2 })]),
  );
});
