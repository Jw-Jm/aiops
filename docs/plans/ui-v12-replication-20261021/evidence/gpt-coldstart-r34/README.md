# r34 原生映射与独立全栈冷启动

2026-10-27～28，GPT直接执行；main/082f87d 工作树未提交。未使用 CodeBuddy，未恢复虚拟化/Trace，未调用模型。此次产品变更仅部署到独立预览 https://192.168.0.63:30472/login，原30471保持r33 API/Worker、Web-r14及既有账号不变。

## 产品改动与验证

AssistantPage在会话读回并确认属于当前主体后才打开owner SSE。显式映射历史保留旧归属，以只读说明、冻结范围和禁用的任务/归档控件展示；历史用户消息不再标为“我”。后端授权继续独立执行，不用界面禁用代替服务端检查。ConversationContext复用原组件增加只读说明，没有新增依赖或抽象。

web-historical-tests.log：两个测试文件11项通过，包括历史消息保留、禁止SSE及写操作、原会话跨范围中断；补全原SSE测试缺失的ownerSubject/binding夹具。web-build.log正式构建通过，保留现有大chunk提示。safe-error-tests.log三项通过。源码与运行哈希分别见source.json/runtime-final.json；API/Worker复用r33二进制，本轮重建CLI和Web。

## C：受控历史映射

只在新r34库构造清楚标注的旧主体与空审计会话，使用生产Repository和真实CLI/API/UI；不是实际旧库迁移，没有合成模型回答或Job。新账号与旧主体同名仍不继承历史（404）或角色。真实opsctl导入inventory首次Imported=true，重放=false；错误摘要通过真实再次验证后仍409且目标会话不变。正确摘要经密码/TOTP证明创建映射201，刷新后保留，目标旧会话401。

映射不授予角色：目标重新登录仍零角色，历史访问403；管理员另行通过UI授予platform_admin后只读历史200，无operator和集群权限。映射前后原owner、revision1、bindingDigest不变。重启后真实浏览器再次验证只读及零owner SSE；安全截图history-read-only.png已查看，仅证明本状态行为，不是与HTML切片的像素验收。

证据：inventory-receipt.json、inventory-repeat.json、mapping-browser.json（含实际请求ID）、history-fixture.json、history-after-mapping.json、history-readonly.json。实际旧库历史执行、审计和证据引用仍待覆盖，C没有整体关闭。

## D：独立无OIDC全栈

获准ssh mssc-direct，namespace ops-native-v12-r34-20261027，任务目录/data/ops-v12-native-cold-r34-20261027。新建数据库ui_v12_r34_native，迁移至70，复用任务专属PG17.11/15454实例，不是新安装PG。独立OpenBao TLS/PKI/Transit/Kubernetes角色、S3 TLS/桶/IAM与任务hostPath；未改共享存储。首次租户通过真实CLI创建，浏览器首次改密、MFA设置确认通过。凭据只留远程private/和Secret，不入证据。

API18094/metrics19096、Worker metrics19097/SP04 mTLS18410、API SP06 mTLS18411、调查器18412、Web30472。API/Worker无OIDC环境，解析Profile禁用Keycloak。API/Worker/调查器新Pod UID、Web-r4重启后，同一Cookie/sessionRef仍200；浏览器刷新可恢复管理页面。故障窗口session502使页面跳到/login，刷新后进入/access/sources，再显式导航管理页成功，不宣称保持原路径。first-tenant重复执行拒绝覆盖，无身份重置。

重启前后，使用真实Worker SA及投射600秒凭据经自身Bao PKI签证书，mTLS访问真实调查器到达请求校验400；无客户端证书被拒绝。新库2账号、0 Jobs、0模型请求；复用冻结的已准入模型配置只供启动，未新增或冒充准入/回答质量验收。该环境无集群/业务来源登记，不能用它代替r28真实业务链路或P3来源验收。

runtime-final.json记录实际Pod UID、镜像与hostPath运行命令、二进制和Web哈希。这是源码canary，不是正式不可变OCI/Bundle、离线空缓存安装、HA或整套切换验收。

## 失败与凭据处置

保留登录连接失败/脚本错误、映射tab与AntD可见选项定位失败；修正自动化定位后通过，没有放宽产品门禁。Web首次重复nginx参数失败；r3重启时tmpfs权限导致nginx不能创建client_temp，改用r4显式/tmp mode1777后重启通过。旧失败容器停止保留。调查器首次PKI初始化失败，随后同SA认证/签证书/CRL与重建成功；初次失败根因尚未确定。Bao初始化须先由opsctl configure建立Transit再建MFA key；不改已有环境。

首次重启脚本的Playwright异常带出了一个r34测试HttpOnly Cookie。只在远程检查该会话：已自然满足idle/absolute过期，旧请求401；未执行Logout，未物理撤销，未改其他账号/会话。失败证据先脱敏再回收（restart-first-failure-redacted.json、session-containment.json）。新增safe-error仅处理此次实际发现的日志泄露边界，三项自检通过。未保存MFA画面、Trace、视频或秘密HTML；只回收明确列出的公开证据，不复制private目录。

## 接续与验收边界

P2-04/P2-05仍in_progress；458行六维通过数不变：visual0、interaction5、api5、persistence5、authorization3、live5。后续先补历史执行/审计/引用和权限组合、实际采集凭据失效恢复与第二namespace授权；P1/P2视觉及P3 SL-049同组按原依赖继续。未知根因和正式发行材料分别记录，不把局部机制通过当作V1.2完成。
