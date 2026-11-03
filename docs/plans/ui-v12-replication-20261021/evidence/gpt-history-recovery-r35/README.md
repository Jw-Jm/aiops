# r35 历史读取与采集失效恢复（2026-10-28）

GPT直接实施，无CodeBuddy、无子代理。main/082f87d工作树继续保留，未提交/推送/重置。本轮只处理用户指定的历史读取和采集故障恢复，不重采参考图、不恢复虚拟化/Trace，不新增模型调用或发行部署。

## 已修复的实际缺口

调查v2原本只有主体精确匹配，显式映射账号仍无法读详情、步骤和历史列表。新增共享只读授权入口currentRead，仅接受已核实inventory映射的legacy v2任务，事务内复检当前原生会话/角色、完整冻结范围和策略；禁止把native Job转让给映射账号。成功读取追加独立审计，保留旧主体/范围/结果；取消与Worker仍走原主体严格路径。SSE在每次披露前用当前读取主体重新验证，撤权后不继续发送已取出的结果。

InvestigationPanel复用服务端subject与当前身份区分历史任务，展示“历史调查只读”，禁用取消；独立授权下发起新调查不受映射影响。没有新增依赖或通用包装，没有新增try/catch或重复入口判空。

来源范围由revision2扩展为3后，未变化UID/RV复用旧归档标识，旧冻结范围与新声明不匹配，导致SOURCE_SCOPE_UNVERIFIED。投影v3在标识/QueryHash中加入SourceRevision，继承固定采集模板的并发幂等检查。旧v1/v2材料保留，不改写旧证据或绕过来源资格。archive-version-proof.json证明同canonical/content digest的旧、新记录使用不同Evidence ID；旧source2/v2仍有179条，新source3/v3继续新增。

## 1. 历史链路验证

historical-archive-ninth.log的三个顶层用例及九个归档子项通过，无SKIP。真实独立PG70、正式Foundation原生Cookie/CSRF与处理器，使用明确标注的迁移夹具：

- 生产Service创建legacy执行、输出入库和结束；映射账号按当前operator范围读取详情、输出SSE与post-check，原执行内容不变。未实际运行外部命令，返回的pending post-check不冒充真实检查完成。
- 调查详情、步骤、历史列表和SSE读回；保留原主体/冻结范围/结果。原主体取消403；未映射同名账号403；收窄或撤销operator后拒绝。原生Job不能借legacy映射读取的入口反例已测试。
- operator不自动读审计403；独立授予platform_admin后可读取旧主体审计。只剩admin不能替代operator。映射读取本身留下可追溯审计。
- SSE第一条发送后撤operator，第二条已缓存结果不再发送。来源disabled后引用拒绝，未把保留历史字节等同于来源仍合格。
- 受控Evidence为archive_pending元数据，明确不称完整归档体通过；真实归档体另外通过实际任务Pod的API→Worker→S3/OpenBao读取200，UID/RV/source3/digest关联见archive-read-authorized.json，partial=false。没有将两类数据拼成真实旧库实迁证据。

本地19项定向Vitest、正式构建、Go定向/合同/race通过；日志见web-tests-final.log、web-build-final.log、go-focused.log、go-contract.log、go-race.log。新历史调查UI尚未做完整业务浏览器或HTML像素验收。

## 2. 真实采集失效、恢复和范围

获准ssh mssc-direct，实际hostname mssc，根分区77%、/data12%。沿用任务专属namespace/PG17.11:15454/schema70，在/data/ops-v12-native-visual-r24-20261026/r35操作；未写共享数据库、未改共享存储、未删除历史文件/镜像。

credential-recovery.json：在原r33栈使用同任务SA的临时600秒有效凭据，记录当前ConfigMap；替换为无效令牌并重建仅本任务Worker，实际拒绝、资源503；恢复原kubelet projected volume，失效期间的更新读回，随后新更新继续入库，12次连续200。失败时也恢复原投射挂载。令牌内容/浏览器Cookie存储未回收；该测试早于r35源码部署，凭据transport本轮未变。

namespace-rbac.json：第二任务命名空间最初list pods=no，仅在那里创建已有形状的只读Role/Binding后为yes；第三任务命名空间仍no，Secrets和Pod写仍no。来源声明通过真实管理员密码/TOTP、与请求摘要绑定的PATCH扩大到A+B，SourceRevision3；Worker配置精确同步。不是拿注册声明代替实际RBAC。

namespace-persistence.json：A=ops-native-v12-r24-20261026、B=ops-native-v12-r35-scope同名r35-same-name，各自真实UID/RV及持久canonical ID独立；C=ops-native-v12-r35-outside实际有同名ConfigMap但不入库。用户范围A时B列表200空；明确授权A+B后12次每次200且含两边各一条、UID不同；C列表仍空。

完整撤operator使旧Cookie401、新登录资源403。之后真实重新授权A+B，再通过同角色范围替换收窄至原A，旧Cookie401，重登录B列表200空、A仍200且一条。当前r31测试账号已恢复原A范围，密码/MFA未改；版本按真实授权变更递增。注意本接口替换同角色范围，不能把两次POST当成范围叠加。protected v12.r24.admin/member未改授权或密码/MFA。

503仍有边界：重建Worker的list/watch未就绪、无效令牌、来源配置与运行版本切换窗口均实际观察到。恢复和最终稳定窗口各12次200，只证明记录窗口；未做长时间/高负载消除503的承诺。初次Transit不可用随后恢复，未凭推测宣称供应端根因已修复。

## 运行与接续

当前30471：r35 Web/API/Worker，来源3/两个任务命名空间；30472：r35 Web/API，原r34 Worker/调查器保持。调查器/模型配置未改，未新建业务Job或调用模型。实际二进制/Web哈希与旧容器保留位置见runtime-final.json。两者都是任务源码canary，非正式不可变OCI/Bundle发布；迁移仍70。

failures.json关联测试夹具、自动化定位、API重启502和超时等原始失败。部署曾打断一次授权脚本的session GET，确认尚未发grant后才重做；没有把未知写结果当作失败盲重试。旧历史失败日志保留，超时goroutine大日志仅保留远程路径，不回收秘密或完整浏览器Trace。证据自检见live-receipt-check.log。

P2-04/05仍in_progress，458行六维状态不提升。已覆盖本轮指定的受控历史授权和真实采集恢复/跨范围链路，但实际旧库迁移、历史query-result/formal-reference全部组合、完整视觉及正式Bundle/离线发行仍未关闭。下一步回到P1/P2视觉收尾与剩余历史引用组合，再按既定依赖开展SL-049资源日志样板；不重复本轮已过的范围/凭据演练。
