import { afterEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import {
  administrativeWrite,
  useNativeWriteProof,
} from "../src/auth/useNativeWriteProof";
const session = vi.hoisted(() => ({
  sessionRef: "session:a",
  revocation: { sessionVersion: 1 },
}));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => ({ authMode: "native", nativeSession: session }),
}));
vi.mock("../src/auth/NativeStepUpControl", () => ({
  NativeStepUpControl: ({
    onReady,
  }: {
    onReady: (expires: string, id: string) => void;
  }) => (
    <button
      onClick={() =>
        onReady(new Date(Date.now() + 5000).toISOString(), "bound-challenge")
      }
    >
      fixture verify
    </button>
  ),
}));
function Fixture({ name }: { name: string }) {
  const proof = useNativeWriteProof(
    administrativeWrite(
      "registry_administration",
      "POST",
      "/api/v1/admin/registry-drafts",
      { logicalName: name },
    ),
    false,
  );
  return (
    <>
      {proof.verification}
      <button disabled={!proof.ready} onClick={() => proof.options()}>
        write
      </button>
      <button
        onClick={() => {
          try {
            proof.options();
          } catch {
            document.title = "rejected";
          }
        }}
      >
        attempt
      </button>
    </>
  );
}
afterEach(() => {
  vi.useRealTimers();
  document.title = "";
  session.revocation.sessionVersion = 1;
});
it("invalidates proof after body/session changes and rejects expired submission even before repaint", () => {
  vi.useFakeTimers();
  const view = render(<Fixture name="first" />);
  expect(screen.getByRole("button", { name: "write" })).toBeDisabled();
  fireEvent.click(screen.getByText("fixture verify"));
  expect(screen.getByRole("button", { name: "write" })).toBeEnabled();
  view.rerender(<Fixture name="changed" />);
  expect(screen.getByRole("button", { name: "write" })).toBeDisabled();
  fireEvent.click(screen.getByText("fixture verify"));
  session.revocation.sessionVersion++;
  view.rerender(<Fixture name="changed" />);
  expect(screen.getByRole("button", { name: "write" })).toBeDisabled();
  fireEvent.click(screen.getByText("fixture verify"));
  vi.setSystemTime(new Date(Date.now() + 6000));
  fireEvent.click(screen.getByText("attempt"));
  expect(document.title).toBe("rejected");
});
