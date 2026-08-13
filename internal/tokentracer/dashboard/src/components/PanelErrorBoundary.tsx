import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

interface PanelErrorBoundaryProps {
  title: string;
  children: ReactNode;
}

interface PanelErrorBoundaryState {
  failed: boolean;
}

export class PanelErrorBoundary extends Component<PanelErrorBoundaryProps, PanelErrorBoundaryState> {
  state: PanelErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): PanelErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch(_error: Error, _info: ErrorInfo): void {
    // The boundary isolates panel failures; error details never reach the UI.
  }

  private reset = (): void => {
    this.setState({ failed: false });
  };

  render(): ReactNode {
    if (this.state.failed) {
      return (
        <div className="panel-error" role="alert">
          <div>{this.props.title}：组件渲染失败</div>
          <button type="button" onClick={this.reset}>
            重试
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
