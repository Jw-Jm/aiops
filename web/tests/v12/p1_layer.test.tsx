import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { LayerProvider, V12Drawer, V12Modal } from "../../src/components/v12/Layer";

function Layers({ onBottom, onTop }: { onBottom: () => void; onTop: () => void }) {
  return (
    <>
      <V12Modal open title="底层抽屉" onClose={onBottom}>底层内容</V12Modal>
      <V12Modal open title="顶层确认" onClose={onTop}>顶层内容</V12Modal>
    </>
  );
}

function FocusLayer() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        onClick={(event) => {
          event.currentTarget.focus();
          setOpen(true);
        }}
      >
        open layer
      </button>
      <V12Modal open={open} title="焦点层" onClose={() => setOpen(false)}>
        <button onClick={() => setOpen(false)}>close layer</button>
      </V12Modal>
    </>
  );
}

it("closes only the top nested layer on Escape", async () => {
  const onBottom = vi.fn();
  const onTop = vi.fn();
  render(
    <LayerProvider>
      <Layers onBottom={onBottom} onTop={onTop} />
    </LayerProvider>,
  );
  expect(await screen.findByText("顶层内容")).toBeInTheDocument();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(onTop).toHaveBeenCalledTimes(1);
  expect(onBottom).not.toHaveBeenCalled();
});

it("renders the portal skin and keeps a long drawer footer reachable", () => {
  render(
    <LayerProvider>
      <V12Drawer
        open
        title="长内容"
        onClose={() => {}}
        footer={<button>底部操作</button>}
      >
        <div>{"长内容 ".repeat(10)}</div>
      </V12Drawer>
    </LayerProvider>,
  );
  expect(document.querySelector(".v12-layer-root")).not.toBeNull();
  expect(screen.getByText("底部操作")).toBeInTheDocument();
});

it("restores focus to the trigger after a real portal layer closes", async () => {
  render(
    <LayerProvider>
      <FocusLayer />
    </LayerProvider>,
  );
  const trigger = screen.getByText("open layer");
  fireEvent.click(trigger);
  const close = await screen.findByText("close layer");
  fireEvent.click(close);
  await waitFor(() => expect(document.activeElement).toBe(trigger));
});
