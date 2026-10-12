import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { Guard, hasRole } from "../src/auth/guards";
import { safeReturnTo } from "../src/auth/oidc";
import {
  checked,
  setCredentials,
  requestOptions,
  ApiError,
} from "../src/api/client";
const auth = vi.hoisted(() => ({
  loading: false,
  user: {},
  session: { roles: ["operator"] as string[] },
  error: null,
}));
vi.mock("../src/auth/AuthProvider", () => ({ useAuth: () => auth }));
describe("verified session boundaries", () => {
  it("does not inherit operator from platform_admin", () => {
    expect(hasRole(["platform_admin"], "operator")).toBe(false);
    expect(hasRole(["operator"], "platform_admin")).toBe(false);
  });
  it("blocks operator admin route", () => {
    render(
      <MemoryRouter>
        <Routes>
          <Route element={<Guard role="platform_admin" />}>
            <Route path="/" element={<div>admin-secret-view</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText("没有此角色权限")).toBeInTheDocument();
    expect(screen.queryByText("admin-secret-view")).not.toBeInTheDocument();
  });
  it("rejects open return URL and preserves local deep links", () => {
    expect(safeReturnTo("//evil.test")).toBe("/overview");
    expect(safeReturnTo("https://evil.test")).toBe("/overview");
    expect(safeReturnTo("/incidents/i?tab=action")).toBe(
      "/incidents/i?tab=action",
    );
  });
  it("keeps tokens out of browser storage and binds tenant header", () => {
    setCredentials("test-token", "test-tenant");
    expect(new Headers(requestOptions().headers).get("X-Tenant-ID")).toBe(
      "test-tenant",
    );
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.getItem("access_token")).toBeNull();
  });
  it("expires memory credential on backend 401; distinguishes 403", async () => {
    setCredentials("test-token");
    await expect(
      checked(
        Promise.resolve({
          data: {
            code: "UNAUTHENTICATED",
            message: "expired",
            requestId: "request-test",
          },
          status: 401,
          headers: new Headers(),
        }),
      ),
    ).rejects.toMatchObject({ status: 401, requestId: "request-test" });
    expect(() => requestOptions()).toThrow(ApiError);
    setCredentials("test-token");
    await expect(
      checked(
        Promise.resolve({
          data: { code: "FORBIDDEN", message: "scope revoked" },
          status: 403,
          headers: new Headers(),
        }),
      ),
    ).rejects.toMatchObject({ status: 403 });
  });
});
