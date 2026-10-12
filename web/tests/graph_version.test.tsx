// Explicit diagram transport fixture. Real graph deployment is checked separately.
import { expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { GraphView } from "../src/features/graph/GraphView";
import { resource } from "./fixtures";
vi.mock("cytoscape", () => ({
  default: () => ({ on() {}, destroy() {}, edges: () => ({ forEach() {} }) }),
}));
function graph(generation: number) {
  return {
    schemaVersion: "resource-graph/v2",
    graphRevision: { ownerEpoch: 1, graphGeneration: generation },
    nodes: [resource],
    edges: [],
    budgets: { maxDepth: 2 },
    freshness: "fresh",
    warnings: [],
    degradedSources: [],
  };
}
it("observes versions in list mode, rejects stale nodes and resets the baseline for another scope", () => {
  const view = render(
    <MemoryRouter>
      <GraphView value={graph(1)} scopeKey="scope-A" />
    </MemoryRouter>,
  );
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "图谱展示方式" }));
  fireEvent.click(screen.getByText("列表（键盘可达）"));
  view.rerender(
    <MemoryRouter>
      <GraphView value={graph(3)} scopeKey="scope-A" />
    </MemoryRouter>,
  );
  view.rerender(
    <MemoryRouter>
      <GraphView value={graph(2)} scopeKey="scope-A" />
    </MemoryRouter>,
  );
  expect(screen.getByText("收到过期图谱版本，请重新查询")).toBeVisible();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  view.rerender(
    <MemoryRouter>
      <GraphView value={graph(1)} scopeKey="scope-B" />
    </MemoryRouter>,
  );
  expect(
    screen.queryByText("收到过期图谱版本，请重新查询"),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: resource.name })).toBeVisible();
});
