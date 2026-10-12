import { Component, type ReactNode } from "react";
import { Alert, Button } from "antd";
export class RegionBoundary extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    if (this.state.failed)
      return (
        <Alert
          showIcon
          type="error"
          title="该区域暂时无法展示"
          description={
            <>
              <p>
                该区域的显示发生异常，其他工作台区域仍可使用。请刷新观测并核对接口数据。
              </p>
              <Button onClick={() => this.setState({ failed: false })}>
                重新展示该区域
              </Button>
            </>
          }
        />
      );
    return this.props.children;
  }
}
