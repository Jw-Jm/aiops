import type { ReactNode } from "react";

export function NativeLoginLayout({ children }: { children: ReactNode }) {
  return (
    <div className="v12-login">
      <aside className="v12-login-art">
        <div>
          <div className="v12-login-brand">
            <span className="v12-login-symbol" aria-hidden="true">
              <svg
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <path d="m12 3 9 5-9 5-9-5zM3 12l9 5 9-5M3 16l9 5 9-5" />
              </svg>
            </span>
            智能运维平台
          </div>
          <h1>
            从运行状态，
            <br />
            到每一次可靠处置。
          </h1>
          <p>统一资源、观测、调查与人工处置，让每个结论都有依据。</p>
          <div className="v12-login-grid">
            {[
              "资源与关系",
              "指标与日志",
              "告警与故障",
              "智能分析",
              "人工处置",
              "知识沉淀",
            ].map((name) => (
              <div key={name}>{name}</div>
            ))}
          </div>
        </div>
        <p>平台内置账号 · 明确授权 · 全程可追溯</p>
      </aside>
      <main className="v12-login-main">
        <section className="login-card" aria-labelledby="native-login-title">
          <span className="v12-login-tag">内置账号</span>
          <h2 id="native-login-title">登录智能运维平台</h2>
          <p className="login-hint">使用管理员创建的平台账号登录。</p>
          {children}
          <p className="v12-login-footer">
            账号由平台管理员创建并分配权限。
            <br />
            请使用本人账号登录。
          </p>
        </section>
      </main>
    </div>
  );
}
