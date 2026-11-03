import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import zhCN from "antd/locale/zh_CN";
import { beforeEach, expect, it, vi } from "vitest";
import { MyAccount } from "../src/auth/MyAccount";
import { LayerProvider } from "../src/components/v12/Layer";
import { ApiError } from "../src/api/client";

const auth = vi.hoisted(() => ({
  changeNativePassword: vi.fn(),
  nativeSession: {
    loginName: "test.user",
    displayName: "测试用户",
    subject: "user:opaque-id",
    authorization: { grants: [{ role: "platform_admin" }] },
    authorizedClusters: [],
  },
}));
vi.mock("../src/auth/AuthProvider", () => ({ useAuth: () => auth }));
beforeEach(() => {
  auth.changeNativePassword.mockReset();
});
function mount() {
  const close = vi.fn(),
    logout = vi.fn();
  render(
    <ConfigProvider locale={zhCN} theme={{ token: { motion: false } }}>
      <LayerProvider>
        <MyAccount onClose={close} onLogout={logout} />
      </LayerProvider>
    </ConfigProvider>,
  );
  return { close, logout };
}
async function fill() {
  fireEvent.click(screen.getByRole("button", { name: "修改密码" }));
  fireEvent.change(await screen.findByLabelText("当前密码"), {
    target: { value: "original-password" },
  });
  fireEvent.change(screen.getByLabelText("密码", { exact: true }), {
    target: { value: "replacement-password" },
  });
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: "replacement-password" },
  });
}
it("displays server-owned profile and independent roles instead of parsing subject", async () => {
  const { logout } = mount();
  await waitFor(() => expect(screen.getByText("test.user")).toBeVisible());
  expect(screen.queryByText("user:opaque-id")).toBeNull();
  expect(screen.getByText("未授予运维数据权限")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
  expect(logout).toHaveBeenCalledOnce();
});
it("prevents duplicate submit and closing until password rotation completes", async () => {
  let release!: () => void;
  auth.changeNativePassword.mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        release = resolve;
      }),
  );
  const { close } = mount();
  await fill();
  const form = screen.getByLabelText("当前密码").closest("form")!;
  fireEvent.submit(form);
  await waitFor(() => expect(auth.changeNativePassword).toHaveBeenCalledOnce());
  expect(screen.getByRole("button", { name: /取\s*消/ })).toBeDisabled();
  expect(screen.getByLabelText("当前密码")).toBeDisabled();
  await act(async () => {
    fireEvent.submit(form);
  });
  fireEvent.keyDown(window, { key: "Escape" });
  expect(close).not.toHaveBeenCalled();
  expect(auth.changeNativePassword).toHaveBeenCalledOnce();
  await act(async () => {
    release();
  });
  await waitFor(() => expect(screen.getByText("test.user")).toBeVisible());
});
it("retains the current account after a wrong-password rejection", async () => {
  auth.changeNativePassword.mockRejectedValue(
    new ApiError(401, "CURRENT_PASSWORD_INVALID", "wrong"),
  );
  const { close } = mount();
  await fill();
  fireEvent.click(screen.getByRole("button", { name: /保存密码/ }));
  await waitFor(() =>
    expect(screen.getByText("当前密码不正确。")).toBeVisible(),
  );
  expect(close).not.toHaveBeenCalled();
  expect(screen.getByLabelText("当前密码")).toBeEnabled();
});
