import { useEffect, useId, useRef, useState } from "react";
import { Alert, Button, Input, Select, Space, Table, Tabs, Tag } from "antd";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useAuth } from "../../auth/AuthProvider";
import {
  administrativeWrite,
  useNativeWriteProof,
} from "../../auth/useNativeWriteProof";
import { useClusterScope, useConfigDraft } from "../../app/ClusterContext";
import { checked, requestOptions } from "../../api/client";
import {
  getNativeUsers,
  createNativeUser,
  updateNativeUser,
  resetNativeUserPassword,
  revokeNativeUserSessions,
  createRoleBinding,
  setOperatorRoleBindingStatus,
} from "../../api/generated/platform";
import type {
  NativeIdentityAccountViewV1 as Account,
  NativeIdentityRoleGrantViewV1 as Grant,
} from "../../api/generated/models";
import {
  PageHeader,
  Surface,
  Toolbar,
  Field,
} from "../../components/v12/primitives";
import { V12Drawer, V12Modal } from "../../components/v12/Layer";
import { QueryState } from "../../components/QueryState";
import { Details } from "../../components/Details";

import { SubjectMappingsPanel } from "./SubjectMappingsPanel";

type Operation =
  | "create"
  | "edit"
  | "status"
  | "password"
  | "sessions"
  | "grant"
  | "revoke";
type Edit = { operation: Operation; user?: Account; grant?: Grant };
const titles: Record<Operation, string> = {
  create: "创建内置账号",
  edit: "编辑用户",
  status: "更改用户登录状态",
  password: "重置用户密码",
  sessions: "撤销登录会话",
  grant: "配置用户授权",
  revoke: "撤销角色授权",
};

function UserOperation({
  edit,
  close,
  changed,
}: {
  edit: Edit;
  close: (uncertain?: boolean) => void;
  changed: () => void;
}) {
  const user = edit.user;
  const [name, setName] = useState(user?.displayName ?? "");
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [role, setRole] = useState<"operator" | "platform_admin">("operator");
  const [clusters, setClusters] = useState<string[]>([]);
  const [namespaces, setNamespaces] = useState("");
  const [reason, setReason] = useState("");
  const { configCandidates } = useClusterScope();
  const id = useId();
  const controller = useRef(new AbortController());
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    controller.current = new AbortController();
    return () => {
      active.current = false;
      controller.current.abort();
    };
  }, []);
  const path = "/api/v1/admin/users" + (user ? "/" + user.userId : "");
  let method = "POST",
    endpoint = path,
    body: Record<string, unknown> = {},
    valid = true;
  const namespaceList = namespaces
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
  const createBody = {
    loginName: login,
    displayName: name,
    initialPassword: password,
  };
  const updateBody =
    edit.operation === "edit"
      ? { displayName: name, expectedRevision: user?.revision ?? 0 }
      : {
          status:
            user?.status === "active"
              ? ("disabled" as const)
              : ("active" as const),
          expectedRevision: user?.revision ?? 0,
        };
  const passwordBody = {
    initialPassword: password,
    expectedRevision: user?.revision ?? 0,
    expectedCredentialVersion: user?.revocation.credentialVersion ?? 0,
  };
  const sessionBody = {
    expectedSessionVersion: user?.revocation.sessionVersion ?? 0,
    reason,
  };
  const grantBody = {
    userId: user?.userId ?? "",
    role,
    scope:
      role === "platform_admin"
        ? { clusterUids: [], namespaces: [] }
        : { clusterUids: clusters, namespaces: namespaceList },
    expectedAuthorizationVersion: user?.revocation.authorizationVersion ?? 0,
    reason,
  };
  const revokeBody = {
    userId: user?.userId ?? "",
    expectedRevision: user?.revocation.authorizationVersion ?? 0,
    status: "disabled" as const,
    reason,
  };
  switch (edit.operation) {
    case "create":
      body = createBody;
      valid =
        !!login.trim() &&
        !!name.trim() &&
        Array.from(password).length >= 12 &&
        Array.from(password).length <= 256 &&
        password === confirm;
      break;
    case "edit":
      method = "PATCH";
      body = updateBody;
      valid = !!name.trim();
      break;
    case "status":
      method = "PATCH";
      body = updateBody;
      break;
    case "password":
      endpoint += "/password:reset";
      body = passwordBody;
      valid =
        Array.from(password).length >= 12 &&
        Array.from(password).length <= 256 &&
        password === confirm;
      break;
    case "sessions":
      endpoint += "/sessions:revoke";
      body = sessionBody;
      valid = !!reason.trim();
      break;
    case "grant":
      endpoint = "/api/v1/admin/role-bindings";
      body = grantBody;
      valid =
        reason.trim().length >= 5 &&
        (role !== "operator" ||
          (clusters.length > 0 &&
            (!namespaceList.length || clusters.length === 1)));
      break;
    case "revoke":
      method = "PATCH";
      endpoint = `/api/v1/admin/role-bindings/${edit.grant!.bindingId}/status`;
      body = revokeBody;
      valid = reason.trim().length >= 5;
      break;
  }
  const proof = useNativeWriteProof(
    valid
      ? administrativeWrite("user_administration", method, endpoint, body)
      : undefined,
    false,
  );
  const submit = useMutation({
    mutationFn: async () => {
      if (!valid) throw new Error("请核对必填字段与密码确认。");
      const options = { ...proof.options(), signal: controller.current.signal };
      const current = () => active.current;
      switch (edit.operation) {
        case "create":
          return checked(createNativeUser(createBody, options), false, current);
        case "edit":
        case "status":
          return checked(
            updateNativeUser(user!.userId, updateBody, options),
            false,
            current,
          );
        case "password":
          return checked(
            resetNativeUserPassword(user!.userId, passwordBody, options),
            false,
            current,
          );
        case "sessions":
          return checked(
            revokeNativeUserSessions(user!.userId, sessionBody, options),
            false,
            current,
          );
        case "grant":
          return checked(createRoleBinding(grantBody, options), false, current);
        case "revoke":
          return checked(
            setOperatorRoleBindingStatus(
              edit.grant!.bindingId,
              revokeBody,
              options,
            ),
            false,
            current,
          );
      }
    },
    onSuccess: () => {
      changed();
      close();
    },
  });
  return (
    <V12Modal
      open
      title={titles[edit.operation]}
      onClose={() => close(submit.isPending)}
      footer={
        <Space>
          <Button onClick={() => close(submit.isPending)}>取消</Button>
          <Button
            type="primary"
            disabled={!valid || !proof.ready}
            loading={submit.isPending}
            onClick={() => submit.mutate()}
          >
            确认提交
          </Button>
        </Space>
      }
    >
      {user ? (
        <p>
          {user.loginName} · 资料版本 r{user.revision}
        </p>
      ) : null}
      <Space orientation="vertical" style={{ width: "100%" }}>
        {edit.operation === "create" ? (
          <Field label="用户名" htmlFor={id + "login"}>
            <Input
              id={id + "login"}
              value={login}
              onChange={(e) => setLogin(e.target.value)}
              autoComplete="off"
              maxLength={128}
            />
          </Field>
        ) : null}
        {["create", "edit"].includes(edit.operation) ? (
          <Field label="姓名" htmlFor={id + "name"}>
            <Input
              id={id + "name"}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={256}
            />
          </Field>
        ) : null}
        {["create", "password"].includes(edit.operation) ? (
          <>
            <Field
              label="密码"
              htmlFor={id + "password"}
              help="12～256 个字符；下次登录必须修改密码。"
            >
              <Input.Password
                id={id + "password"}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                maxLength={512}
              />
            </Field>
            <Field label="确认密码" htmlFor={id + "confirm"}>
              <Input.Password
                id={id + "confirm"}
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
                maxLength={512}
              />
            </Field>
          </>
        ) : null}
        {edit.operation === "status" ? (
          <Alert
            type="warning"
            title={
              user!.status === "active"
                ? "停用后撤销该用户的现有登录会话。"
                : "恢复登录不会新增角色或扩大集群范围。"
            }
          />
        ) : null}
        {edit.operation === "grant" ? (
          <>
            <Field label="角色">
              <Select
                aria-label="角色"
                value={role}
                onChange={setRole}
                options={[
                  { value: "operator", label: "运维操作员" },
                  { value: "platform_admin", label: "平台管理员" },
                ]}
              />
            </Field>
            {role === "operator" ? (
              <>
                <Field label="集群范围">
                  <Select
                    aria-label="集群范围"
                    mode="multiple"
                    value={clusters}
                    onChange={setClusters}
                    options={configCandidates.map((c) => ({
                      value: c.clusterUid,
                      label: c.displayName,
                    }))}
                  />
                </Field>
                <Field
                  label="命名空间"
                  htmlFor={id + "namespaces"}
                  help="使用英文逗号分隔；指定命名空间时只允许一个集群。"
                >
                  <Input
                    id={id + "namespaces"}
                    value={namespaces}
                    onChange={(e) => setNamespaces(e.target.value)}
                  />
                </Field>
              </>
            ) : (
              <Alert
                type="info"
                title="平台管理员与运维操作员分别授权；管理员角色不授予资源操作权限。"
              />
            )}
          </>
        ) : null}
        {["sessions", "grant", "revoke"].includes(edit.operation) ? (
          <Field label="操作原因" htmlFor={id + "reason"}>
            <Input.TextArea
              id={id + "reason"}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              maxLength={512}
            />
          </Field>
        ) : null}
        {edit.operation === "create" ? (
          <Alert type="info" title="创建账号不自动授予管理员或集群权限。" />
        ) : null}
        {edit.operation === "revoke" ? (
          <Alert
            type="warning"
            title={`撤销 ${edit.grant!.role} 角色；该用户的现有会话将失效。`}
          />
        ) : null}
        {proof.verification}
        <QueryState loading={false} error={submit.error} />
      </Space>
    </V12Modal>
  );
}

export function UsersPage() {
  const auth = useAuth();
  const [cursor, setCursor] = useState<string>();
  const [filter, setFilter] = useState("");
  const [tab, setTab] = useState("users");
  const [selected, setSelected] = useState<Account>();
  const [edit, setEdit] = useState<Edit>();
  const [uncertain, setUncertain] = useState(false);
  useConfigDraft(!!edit);
  const query = useQuery({
    queryKey: ["native-users", cursor],
    enabled: auth.authMode === "native",
    queryFn: ({ signal }) =>
      checked(getNativeUsers({ limit: 50, cursor }, requestOptions(signal))),
  });
  const users = query.data?.data ?? [];
  const rows = users.filter((u) =>
    (u.loginName + u.displayName).toLowerCase().includes(filter.toLowerCase()),
  );
  const grants = rows.flatMap((user) =>
    user.roleGrants.map((grant) => ({ ...grant, user })),
  );
  const refresh = () => {
    setSelected(undefined);
    void query.refetch();
  };
  if (auth.authMode !== "native")
    return <Alert type="warning" title="该页面需要原生账号运行模式。" />;
  return (
    <>
      <PageHeader
        title="用户与权限"
        description="由平台管理员创建账号，管理启停、密码和访问授权。"
        actions={
          <Button
            type="primary"
            onClick={() => setEdit({ operation: "create" })}
          >
            ＋ 创建用户
          </Button>
        }
      />
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: "users", label: "用户管理" },
          { key: "grants", label: "角色与授权" },
          { key: "mappings", label: "历史主体映射" },
        ]}
      />
      {uncertain ? (
        <Alert
          type="warning"
          closable
          onClose={() => setUncertain(false)}
          title="操作窗口已关闭；已提交请求可能仍在服务端完成，请刷新确认最终状态。"
        />
      ) : null}
      {tab === "mappings" ? (
        <SubjectMappingsPanel users={users} usersChanged={refresh} />
      ) : (
        <Surface title={tab === "users" ? "平台用户" : "角色授权"}>
          <Toolbar>
            <Input.Search
              aria-label="搜索本页用户"
              placeholder="用户名或姓名（本页）"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <Button onClick={refresh}>刷新</Button>
          </Toolbar>
          <QueryState
            loading={query.isPending}
            error={query.error}
            retry={refresh}
          >
            {tab === "users" ? (
              <Table
                rowKey="userId"
                dataSource={rows}
                pagination={false}
                columns={[
                  {
                    title: "用户名 / 姓名",
                    render: (_, u: Account) => (
                      <>
                        <strong>{u.displayName}</strong>
                        <div>{u.loginName}</div>
                      </>
                    ),
                  },
                  {
                    title: "登录状态",
                    render: (_, u: Account) => (
                      <Tag>{u.status === "active" ? "启用" : "停用"}</Tag>
                    ),
                  },
                  {
                    title: "角色",
                    render: (_, u: Account) =>
                      u.roleGrants
                        .map((g) =>
                          g.role === "operator" ? "运维操作员" : "平台管理员",
                        )
                        .join("、") || "尚未授权",
                  },
                  {
                    title: "密码状态",
                    render: (_, u: Account) =>
                      u.mustChangePassword ? "首次登录须修改" : "已设置",
                  },
                  {
                    title: "操作",
                    render: (_, u: Account) => (
                      <Space>
                        <Button type="link" onClick={() => setSelected(u)}>
                          详情
                        </Button>
                        <Button
                          type="link"
                          onClick={() =>
                            setEdit({ operation: "grant", user: u })
                          }
                        >
                          配置授权
                        </Button>
                        <Button
                          type="link"
                          onClick={() =>
                            setEdit({ operation: "status", user: u })
                          }
                        >
                          {u.status === "active" ? "停用" : "恢复"}
                        </Button>
                      </Space>
                    ),
                  },
                ]}
              />
            ) : (
              <Table
                rowKey="bindingId"
                dataSource={grants}
                pagination={false}
                columns={[
                  { title: "用户", render: (_, g) => g.user.loginName },
                  { title: "角色", dataIndex: "role" },
                  {
                    title: "集群与命名空间",
                    render: (_, g) => <Details value={g.scope} />,
                  },
                  {
                    title: "操作",
                    render: (_, g) => (
                      <Button
                        danger
                        onClick={() =>
                          setEdit({
                            operation: "revoke",
                            user: g.user,
                            grant: g,
                          })
                        }
                      >
                        撤销授权
                      </Button>
                    ),
                  },
                ]}
              />
            )}
            <p>账号与角色分别管理；创建账号不自动授予管理员或集群权限。</p>
            <Space>
              <Button disabled={!cursor} onClick={() => setCursor(undefined)}>
                首页
              </Button>
              <Button
                disabled={!query.data?.meta.nextCursor}
                onClick={() => setCursor(query.data?.meta.nextCursor)}
              >
                下一页
              </Button>
            </Space>
          </QueryState>
        </Surface>
      )}
      {selected ? (
        <V12Drawer open title="用户详情" onClose={() => setSelected(undefined)}>
          <Details
            value={{
              用户名: selected.loginName,
              姓名: selected.displayName,
              账号类型: "平台内置账号",
              平台访问: selected.status,
              密码状态: selected.mustChangePassword
                ? "首次登录须修改"
                : "已设置；不可查看原密码",
              资料版本: selected.revision,
            }}
          />
          <Space wrap>
            {(["edit", "password", "sessions"] as const).map((operation) => (
              <Button
                key={operation}
                onClick={() => setEdit({ operation, user: selected })}
              >
                {titles[operation]}
              </Button>
            ))}
          </Space>
        </V12Drawer>
      ) : null}
      {edit ? (
        <UserOperation
          edit={edit}
          close={(pending) => {
            setEdit(undefined);
            if (pending) {
              setUncertain(true);
              refresh();
            }
          }}
          changed={refresh}
        />
      ) : null}
    </>
  );
}
