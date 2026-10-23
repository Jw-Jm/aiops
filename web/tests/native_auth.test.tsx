import { beforeEach, expect, it, vi } from "vitest";
import { ConfigProvider } from "antd";
import zhCN from "antd/locale/zh_CN";
import {
  fireEvent,
  render as renderReact,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { Login } from "../src/pages/Login";
import { FirstPassword } from "../src/pages/FirstPassword";
import { LayerProvider } from "../src/components/v12/Layer";
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

// jsdom cannot complete CSS transitions; browser checks retain production motion.
const render = (ui: Parameters<typeof renderReact>[0]) =>
  renderReact(
    <ConfigProvider locale={zhCN} theme={{ token: { motion: false } }}>
      {ui}
    </ConfigProvider>,
  );

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
  await waitFor(() => expect(screen.getByLabelText("当前密码")).toBeVisible());
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "initial-password" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /保存密码/ }));
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

it("cancels the first-password dialog through server logout without submitting a password", async () => {
  auth.nativeSession = {
    mode: "first_password_only",
    revocation: { credentialVersion: 1, sessionVersion: 1 },
  };
  auth.logout.mockResolvedValue({ status: "confirmed" });
  render(
    <MemoryRouter>
      <LayerProvider>
        <FirstPassword />
      </LayerProvider>
    </MemoryRouter>,
  );
  await waitFor(() =>
    expect(
      screen.getByRole("dialog", { name: "首次登录：修改密码" }),
    ).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  await waitFor(() => expect(auth.logout).toHaveBeenCalledTimes(1));
  expect(auth.changeNativePassword).not.toHaveBeenCalled();
});

it("validates matching passwords and prevents closing or duplicate submission while the real request is pending", async () => {
  auth.nativeSession = {
    mode: "first_password_only",
    revocation: { credentialVersion: 1, sessionVersion: 1 },
  };
  let finish!: () => void;
  auth.changeNativePassword.mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  render(
    <MemoryRouter initialEntries={["/auth/first-password"]}>
      <LayerProvider>
        <Routes>
          <Route path="/auth/first-password" element={<FirstPassword />} />
          <Route path="*" element={<Location />} />
        </Routes>
      </LayerProvider>
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("当前密码"), {
    target: { value: "initial-password" },
  });
  fireEvent.change(screen.getByLabelText("密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: "different-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存密码" }));
  await waitFor(() =>
    expect(screen.getByText("两次输入的新密码不一致")).toBeVisible(),
  );
  expect(auth.changeNativePassword).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: "replacement-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存密码" }));
  await waitFor(() =>
    expect(auth.changeNativePassword).toHaveBeenCalledTimes(1),
  );
  expect(screen.getByRole("button", { name: "取消" })).toBeDisabled();
  expect(screen.getByLabelText("当前密码")).toBeDisabled();
  expect(screen.getByRole("button", { name: "关闭" })).toBeDisabled();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(auth.logout).not.toHaveBeenCalled();
  finish();
  await waitFor(() =>
    expect(screen.getByTestId("location")).toHaveTextContent("/access/sources"),
  );
});

it.each(["close", "escape"])(
  "revokes the first-password session through %s",
  async (action) => {
    auth.nativeSession = {
      mode: "first_password_only",
      revocation: { credentialVersion: 1, sessionVersion: 1 },
    };
    auth.logout.mockResolvedValue({ status: "confirmed" });
    render(
      <MemoryRouter>
        <LayerProvider>
          <FirstPassword />
        </LayerProvider>
      </MemoryRouter>,
    );
    if (action === "close")
      fireEvent.click(screen.getByRole("button", { name: "关闭" }));
    else fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(auth.logout).toHaveBeenCalledTimes(1));
    expect(auth.changeNativePassword).not.toHaveBeenCalled();
  },
);
