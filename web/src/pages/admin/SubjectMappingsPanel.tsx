import { useEffect, useId, useRef, useState } from "react";
import { Alert, Button, Input, Select, Space, Table } from "antd";
import { useMutation, useQuery } from "@tanstack/react-query";
import { checked, requestOptions } from "../../api/client";
import {
  createNativeSubjectMapping,
  getNativeSubjectMappings,
} from "../../api/generated/platform";
import type { NativeIdentityAccountViewV1 as Account } from "../../api/generated/models";
import {
  administrativeWrite,
  useNativeWriteProof,
} from "../../auth/useNativeWriteProof";
import { useConfigDraft } from "../../app/ClusterContext";
import { Field, Surface, Toolbar } from "../../components/v12/primitives";
import { V12Modal } from "../../components/v12/Layer";
import { QueryState } from "../../components/QueryState";

function MappingForm({
  users,
  close,
  changed,
}: {
  users: Account[];
  close: (pending?: boolean) => void;
  changed: () => void;
}) {
  const [userId, setUserId] = useState<string>();
  const [subject, setSubject] = useState("");
  const [digest, setDigest] = useState("");
  const user = users.find((u) => u.userId === userId);
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
  const body = {
    legacySubject: subject,
    userId: user?.userId ?? "",
    basis: "verified_inventory" as const,
    evidenceDigest: digest,
    expectedUserRevision: user?.revision ?? 0,
  };
  const valid =
    !!user &&
    !!subject.trim() &&
    Array.from(subject).length <= 256 &&
    /^sha256:[a-f0-9]{64}$/.test(digest);
  const proof = useNativeWriteProof(
    valid
      ? administrativeWrite(
          "user_administration",
          "POST",
          "/api/v1/admin/subject-mappings",
          body,
        )
      : undefined,
    false,
  );
  const submit = useMutation({
    mutationFn: () => {
      if (!valid) throw new Error("请核对目标用户、旧主体与已核对清单摘要。");
      return checked(
        createNativeSubjectMapping(body, {
          ...proof.options(),
          signal: controller.current.signal,
        }),
        false,
        () => active.current,
      );
    },
    onSuccess: () => {
      changed();
      close();
    },
  });
  return (
    <V12Modal
      open
      title="映射历史主体"
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
            确认映射
          </Button>
        </Space>
      }
    >
      <Space orientation="vertical" style={{ width: "100%" }}>
        <Alert
          type="warning"
          title="映射将撤销目标用户的现有会话；不复制历史角色，不自动开放历史记录访问。"
        />
        <Field label="目标用户（当前用户页）">
          <Select
            aria-label="映射目标用户"
            value={userId}
            onChange={setUserId}
            options={users.map((u) => ({
              value: u.userId,
              label: `${u.displayName} · ${u.loginName} · ${u.userId}`,
            }))}
            style={{ width: "100%" }}
          />
        </Field>
        <Field
          label="历史主体"
          htmlFor={id + "subject"}
          help="填写已核对清单中的完整主体标识，不使用登录名推断。"
        >
          <Input
            id={id + "subject"}
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            maxLength={512}
          />
        </Field>
        <Field
          label="已核对清单摘要"
          htmlFor={id + "digest"}
          help="由受控导入回执提供；服务端逐项复核目标账号与旧主体。"
        >
          <Input
            id={id + "digest"}
            value={digest}
            onChange={(e) => setDigest(e.target.value)}
            maxLength={71}
          />
        </Field>
        {user ? (
          <p>
            目标资料版本 r{user.revision} · {user.userId}
          </p>
        ) : null}
        {proof.verification}
        <QueryState loading={false} error={submit.error} />
      </Space>
    </V12Modal>
  );
}

export function SubjectMappingsPanel({
  users,
  usersChanged,
}: {
  users: Account[];
  usersChanged: () => void;
}) {
  const [cursor, setCursor] = useState<string>();
  const [open, setOpen] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  useConfigDraft(open);
  const query = useQuery({
    queryKey: ["native-subject-mappings", cursor],
    queryFn: ({ signal }) =>
      checked(
        getNativeSubjectMappings({ limit: 50, cursor }, requestOptions(signal)),
      ),
  });
  const refresh = () => {
    void query.refetch();
    usersChanged();
  };
  return (
    <Surface title="历史主体映射">
      <Alert
        type="info"
        title="仅允许显式映射已核对清单中的主体。账号、角色授权和历史记录访问分别核对。"
      />
      {uncertain ? (
        <Alert
          type="warning"
          title="窗口已关闭；请求可能仍在完成，请刷新确认最终映射和目标用户状态。"
        />
      ) : null}
      <Toolbar>
        <Button onClick={refresh}>刷新映射</Button>
        <Button type="primary" onClick={() => setOpen(true)}>
          映射历史主体
        </Button>
      </Toolbar>
      <QueryState loading={query.isPending} error={query.error} retry={refresh}>
        <Table
          rowKey="mappingId"
          dataSource={query.data?.data ?? []}
          pagination={false}
          columns={[
            { title: "历史主体", dataIndex: "legacySubject" },
            { title: "原生用户 ID", dataIndex: "userId" },
            { title: "映射证据摘要", dataIndex: "evidenceDigest" },
            { title: "映射时间", dataIndex: "mappedAt" },
          ]}
        />
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
      {open ? (
        <MappingForm
          users={users}
          changed={refresh}
          close={(pending) => {
            setOpen(false);
            if (pending) {
              setUncertain(true);
              refresh();
            }
          }}
        />
      ) : null}
    </Surface>
  );
}
