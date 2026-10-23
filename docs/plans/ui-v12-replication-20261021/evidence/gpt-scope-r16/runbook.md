# 原生授权范围前滚57

仅获准任务库mssc-direct:15453 / ui_v12_native_identity，精确任务标记ops.task=ui-v12-p2-01-r1。迁移57已执行并冻结；1～56不变。不要重跑要求版本56的migrate.py，也不要回退旧身份/撤销/授权数据。后续SQL使用58+前滚修复。

既有NativeIdentityScopeV1合同允许最多50个cluster UID、200个namespace；55的SQL仍是100/100。57只替换admin_account_write的这两个上限，保留函数所有事务锁、当前会话/角色/版本检查、最后管理员保护、独立授权版本推进和旧会话撤销。既有授权不迁移、不删除、不扩权。API/Worker仍没有identity业务表直接授权。

浏览器before以真实密码+TOTP证明提交200项得到400；迁移后after得到201；SQL独立核对持久化200项。API角色直接SQL事务验证200可接受、201个namespace及51个cluster在注册表检查前拒绝，全部探测写入ROLLBACK，原binding/范围/账号版本不变。真实来源、Worker、并发/审计故障和全部产品视觉不由此通过。

30467、30468、30469共用此任务库，当前库版本57；它们的代码与Web制品版本各不相同，按轮次source.json和runtime.txt核对。共享开发库、生产、旧参考库未写入。旧容器/目录/镜像均保留。
