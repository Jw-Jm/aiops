import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { Guard } from "../src/auth/guards";

const auth = vi.hoisted(() => ({
  authMode: "native",
  loading: false,
  loggedOut: false,
  session: { roles: [] as string[] },
  logout: vi.fn(),
}));
vi.mock("../src/auth/AuthProvider", () => ({ useAuth: () => auth }));
vi.mock("../src/auth/MyAccount", () => ({
  MyAccount: () => <div>own-account-view</div>,
}));
beforeEach(() => {
  auth.session.roles = [];
});
it.each(["operator", "platform_admin"] as const)(
  "keeps %s routes denied while allowing the own account dialog",
  (role) => {
    auth.session.roles = [role === "operator" ? "platform_admin" : "operator"];
    render(
      <MemoryRouter>
        <Routes>
          <Route element={<Guard role={role} />}>
            <Route path="/" element={<div>protected-business</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText("当前身份没有此功能权限")).toBeInTheDocument();
    expect(screen.queryByText("protected-business")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "查看当前身份" }));
    expect(screen.getByText("own-account-view")).toBeInTheDocument();
  },
);
it("allows a native account without grants to reach its own security page", () => {
  render(
    <MemoryRouter>
      <Routes>
        <Route element={<Guard />}>
          <Route path="/" element={<div>own-security-page</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
  expect(screen.getByText("own-security-page")).toBeInTheDocument();
});
it("does not let zero grants reach a role-protected page", () => {
  render(
    <MemoryRouter>
      <Routes>
        <Route element={<Guard />}>
          <Route element={<Guard role="operator" />}>
            <Route path="/" element={<div>protected-business</div>} />
          </Route>
        </Route>
      </Routes>
    </MemoryRouter>,
  );
  expect(screen.queryByText("protected-business")).toBeNull();
  expect(screen.getByText("需要运维操作员权限")).toBeInTheDocument();
});
