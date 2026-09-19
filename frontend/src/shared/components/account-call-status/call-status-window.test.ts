import assert from "node:assert/strict";
import { test } from "node:test";

import { chartWindow, clampChartStart, startForHighlight } from "./call-status-window.ts";

test("fits a sparse series into the host without scrolling", () => {
  assert.deepEqual(chartWindow(6, 800), { visibleCount: 6, maxStart: 0 });
});

test("keeps point spacing when the series is denser than the host", () => {
  assert.deepEqual(chartWindow(200, 560, 28), { visibleCount: 20, maxStart: 180 });
});

test("clamps the pan start to the latest window", () => {
  assert.equal(clampChartStart(-4, 10), 0);
  assert.equal(clampChartStart(12, 10), 10);
  assert.equal(clampChartStart(3, 0), 0);
});

test("centers a highlighted call in the visible window", () => {
  assert.equal(startForHighlight(50, 20, 200), 40);
  assert.equal(startForHighlight(5, 20, 200), 0);
  assert.equal(startForHighlight(195, 20, 200), 180);
});
