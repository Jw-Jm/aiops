import { beforeEach, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { Login } from "../src/pages/Login";
import { FirstPassword } from "../src/pages/FirstPassword";
import {
  ApiError,
  requestOptions,
  setNativeCredentials,
  clearCredentials,
} from "../src/api/client";

const auth = vi.hoisted(() => ({
  nativeLogin: vi.fn(),
  changeNativePassword: vi.fn(),
  logout: vi.fn(),
  nativeSession: null as null | {
    mode: "full" | "first_password_only";
    revocation: { credentialVersion: number; sessionVersion: number };
  },
  session: null,
  authMode: "native" as const,
  loading: false,
  error: null,
}));
vi.mock("../src/auth/AuthProvider", () => ({
  useAuth: () => auth,
}));

beforeEach(() => {
  vi.clearAllMocks();
  auth.nativeLogin.mockReset();
  auth.changeNativePassword.mockReset();
  auth.logout.mockReset();
  auth.nativeSession = null;
  auth.session = null;
  clearCredentials();
});

function Location() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
}

it("logs in with the native account and navigates to the requested page", async () => {
  auth.nativeLogin.mockResolvedValue("/overview?clusterUid=uid-a");
  render(
    <MemoryRouter initialEntries={["/login"]}>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="*" element={<Location />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(screen.getByRole("heading", { name: /从运行状态/ })).toBeVisible();
  expect(
    screen.getByRole("heading", { name: "登录智能运维平台" }),
  ).toBeVisible();
  expect(screen.queryByText(/演示账号/)).toBeNull();
  fireEvent.change(screen.getByLabelText("用户名"), {
    target: { value: "root.admin" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "initial-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /登\s*录/ }));
  await waitFor(() => expect(auth.nativeLogin).toHaveBeenCalled());
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("/overview"),
  );
  expect(screen.queryByRole("button", { name: /OIDC/ })).toBeNull();
});

it("shows a constant failure for rejected native credentials", async () => {
  auth.nativeLogin.mockRejectedValue(
    new ApiError(401, "UNAUTHENTICATED", "login credentials were not accepted"),
  );
  render(
    <MemoryRouter initialEntries={["/login"]}>
      <Login />
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("用户名"), {
    target: { value: "root.admin" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "wrong-password-value" },
  });
  fireEvent.click(screen.getByRole("button", { name: /登\s*录/ }));
  await waitFor(() => expect(auth.nativeLogin).toHaveBeenCalledTimes(1));
  expect(await screen.findByText(/登录名或密码不正确/)).toBeVisible();
  expect(auth.nativeLogin).toHaveBeenCalledTimes(1);
});

it("requires the current password during first password setup and rotates the session", async () => {
  auth.nativeSession = {
    mode: "first_password_only",
    revocation: { credentialVersion: 1, sessionVersion: 1 },
  };
  auth.changeNativePassword.mockResolvedValue(undefined);
  render(
    <MemoryRouter initialEntries={["/auth/first-password"]}>
      <Routes>
        <Route path="/auth/first-password" element={<FirstPassword />} />
        <Route path="*" element={<Location />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(screen.getByLabelText("当前密码")).toBeVisible();
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "initial-password" },
  });
  fireEvent.change(screen.getByLabelText("新密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.change(screen.getByLabelText("确认新密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /设置密码并继续/ }));
  await waitFor(() => expect(auth.changeNativePassword).toHaveBeenCalled());
  expect(auth.changeNativePassword).toHaveBeenCalledWith(
    "initial-password",
    "replacement-password",
  );
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("/access/sources"),
  );
});

it("sends native requests without bearer material and attaches CSRF only to mutations", () => {
  setNativeCredentials("tenant-id", "csrf-token");
  const read = requestOptions();
  const write = requestOptions(undefined, true);
  const headers = (init: RequestInit) => init.headers as Record<string, string>;
  expect(headers(read).Authorization).toBeUndefined();
  expect(headers(read)["X-CSRF-Token"]).toBeUndefined();
  expect(headers(write)["X-CSRF-Token"]).toBe("csrf-token");
  expect(headers(write).Authorization).toBeUndefined();
  expect(write.credentials).toBe("same-origin");
});
