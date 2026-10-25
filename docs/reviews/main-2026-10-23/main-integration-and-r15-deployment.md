# 主分支合入与 r15 环境同步（2026-10-25）

本轮全部修复、测试、迁移、源码闭包工具与评审材料已提交为 `16858c194f8c2147019ceea70db0c3d3b5f41084`，快进合入 `main` 并推送 `https://github.com/Jw-Jm/aiops`。共343个文件，未改写历史提交或强推。

## 合并前验证

- `go test ./internal/... ./cmd/... ./test/contract -count=1` 全部通过。Graph 包运行554.774秒，交付契约137.720秒，均正常完成。
- 前端 `npm run build` 通过；`npm test` 的6项流测试及168项Vitest全部通过。
- worktree 测试通过临时外部目录链接读取仓库外的原冻结 prototype，未修改产品代码、测试断言或正式原型。常用检出仍使用原有的 `ops/ui-handoff` 输入。
- 产品与报告差异的空白检查通过；原始证据文件、patch和CSV保留原始格式。前述r13远程业务E2E验收证据继续有效。

日志位于 [evidence/main-integration/](evidence/main-integration/)。

## 远程源码与构建产物

mssc-direct 的 `/opt/ops-main-deployment-20261025/source` 保存该提交的完整Git源码归档；`bin/` 保存Go1.27.1交叉编译的Linux/amd64 `platform-api`、`platform-worker`、`opsctl`、`db-migrate`；`web/` 保存本次前端构建。发布完成后的文档回执随主分支再次同步，产品二进制对应上述代码提交。

## 用户指定的 r15 运行服务

- API：`ops-ui-v12-gpt-r15-api.service`，`127.0.0.1:18089`，已换成本次编译的API二进制并重启。
- Web：`ops-ui-v12-gpt-r15-web-r5`，入口 `https://192.168.0.63:30469`，已同步本次前端构建，保留原运行时配置及旧带摘要资源以支持已打开页面。
- 数据库：发现原 `ui_v12_native_identity` 仍为57且被其他预览共用。先做完整备份，再复制为 `ui_v12_native_identity_main_20261025`，迁移57→69后仅切换r15。原共享库仍为57，其他预览服务未重启或切库。
- 原API、启动脚本、前端及数据库备份保存在远程 `/opt/ops-main-deployment-20261025/backup`。数据库口令继续从原受控私有文件读取，没有写入仓库或回执。

## 上线后检查

[deployment-result.json](evidence/main-integration/deployment-result.json) 记录了实际PID、运行中API与构建产物的二进制摘要一致性、数据库版本及HTTP结果。

- API `/healthz`、`/readyz` 均200；未登录 `/api/v1/auth/session` 返回401。
- 使用原配置的TLS证书验证HTTPS入口；首页和14个入口静态资源均200，返回字节与构建文件一致。
- Nginx按既有运行参数 `nginx -t -e /dev/stderr` 检查通过。裸 `nginx -t` 使用镜像不存在的默认日志路径，故采用与运行服务一致的参数；未改Nginx配置或新建日志目录。
- 候选验证API在18090完成预检后已停止。没有恢复虚拟化开发，没有额外启动r15原本未启用的Worker。

## 回滚与范围

如需回滚，先停止r15 API，将 `backup/run-api.py`、`backup/platform-api-password` 恢复至原预览路径，再恢复 `backup/web-footer-final` 的文件并重启API。原启动脚本会连回保留的57版本原共享库；69版本副本保留，不做Down迁移，不删除Audit/Evidence。

本次完成主分支/GitHub/指定r15服务同步；上线检查为健康、认证拒绝和静态资源检查，未冒充完整浏览器登录及全部真实来源的安装态验收。其他MR原有待专项验收项继续以fix-progress为准。
