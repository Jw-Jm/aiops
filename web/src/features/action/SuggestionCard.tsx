import { Alert } from "antd";
import { record, text, list } from "../../components/data";
import { Details } from "../../components/Details";
export function SuggestionCard({ value }: { value: unknown }) {
  const plan = record(value);
  return (
    <section className="section">
      <h3>建议命令（只读）</h3>
      <Alert
        showIcon
        type="info"
        title="建议不构成授权"
        description="实际命令输入框初始为空。由本人独立输入、评估风险并确认提交。"
      />
      {value ? (
        <div>
          <p>{text(plan.reason)}</p>
          <pre className="command-review">{text(plan.suggestedCommand)}</pre>
          <p>预期影响：{text(plan.expectedImpact)}</p>
          {list(plan.preconditions).length > 0 && (
            <>
              <h4>执行前请核对</h4>
              <ul>
                {list(plan.preconditions).map((item, index) => (
                  <li key={index}>{text(item)}</li>
                ))}
              </ul>
            </>
          )}
          <details className="technical-details">
            <summary>建议详情与完整编号</summary>
            <Details value={value} />
          </details>
        </div>
      ) : (
        <p>
          当前没有已选择的处置建议。可以直接输入已核实的命令，或先在智能调查中查看建议。
        </p>
      )}
    </section>
  );
}
