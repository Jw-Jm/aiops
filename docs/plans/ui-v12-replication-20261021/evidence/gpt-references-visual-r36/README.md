# r36 历史引用与原生界面核对（2026-10-28）

GPT直接执行，无CodeBuddy，无子代理。main/082f87d66c5b1336b4cd32c48ebbe59e4093144b 上保留既有未提交改动。

## 历史读取

`TestNativeMappedHistoricalQueryAndFormalReferences` 在 mssc-direct 一次性 PostgreSQL 库通过。真实原生 Cookie 中间件/读取处理器验证 query-result、固定 RCA v1 与处置建议：显式映射只读、范围收窄拒绝/恢复、来源 revision 变更/停用拒绝、固定引用内容变化拒绝、撤权拒绝；不回退到最新版本，不泄漏命令/执行句柄，冻结旧归属不变。**历史业务内容是明确的受控 fixture，不代表真实旧库迁移或真实日志来源。** 无生产后端修改。首轮 allowedSchemas fixture 错误保留于 history-first-failure.log；修正为正式允许值后 history-passed.log 通过。

## 视觉和交互

复用原736张参考，不采集/生成新参考。只拍正式产品状态；1440×960、100%、远程 Chrome、zh-CN/Asia-Shanghai。

修复实际公共偏差：恢复30px状态条几何（生产文案）、侧栏品牌图标/名称、去除旧topbar覆盖，13px/1.65字体、用户表列宽/间距、状态标记、按钮及弹层危险动作。用户卡片正式实测 y=251.17→282.69；冻结SL-023 PNG卡片上沿约283。账号弹窗仍660×423.81；原型文档滚动条导致的7px横向差异未通过整体挪动掩盖。

visual-before.json、visual-final.json、shots-before/、shots-final/ 对应实际产品；10项安全状态包含登录空/错误、账号、改密、MFA状态、用户表、创建、详情、授权、角色页。另有首次改密/不一致/MFA未绑定/退出截图与 visual-first-password.json。首改密原生“当前密码”与密码策略导致增加高度；创建/授权含真实再次验证和独立角色范围，属于必须登记的补充状态。真实账号/角色/行数、原型演示文案与基于角色隐藏的导航不伪造为同一数据。

首改密由新增专属零角色账号完成；密码私有保存，仅真实创建/改密/退出。原r24 admin/member密码、MFA、授权均未改。MFA种子/恢复码/动态码不截图，不保存Trace/HTML秘密；MFA尚无原始独立切片，不能虚构像素通过。

34项定向Vitest与正式构建通过。**visual全维仍未通过：尚未覆盖全部错误/加载/权限/恢复状态；本轮没有把截图存在等同通过。** 视觉缺口和交互结果分开记录。

## 运行版本

r36b Web已部署30471、30472，index SHA256见 web-runtime-final.json；API/Worker当时仍r35/schema70。后续r37开始将30471 API/Worker前滚schema71，不能把本目录截图解释为r37日志页面证据。旧Web容器/制品保留。

operator-pages.json 是任务专属运维账号实际登录的页面/菜单/请求断言：有运行总览、资源目录、智能助手。纯管理员仅接入/系统菜单属于双角色独立授权。此脚本未采集业务截图：安全探针拒绝疑似恢复码形状的页面，未放宽探针；失败记录保留远程。
