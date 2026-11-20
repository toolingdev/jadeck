import { Component, type ErrorInfo, type ReactNode } from "react";
import { reportClientError } from "@/lib/report";

/**
 * Catches a crash inside one widget (or page) so the rest of the screen
 * keeps working, and sends the error to the controller's log.
 */
export class Boundary extends Component<{ name: string; children: ReactNode; full?: boolean }, { err: Error | null }> {
  state = { err: null as Error | null };

  static getDerivedStateFromError(err: Error) {
    return { err };
  }

  componentDidCatch(err: Error, info: ErrorInfo) {
    reportClientError(`${this.props.name} crashed: ${err.message}`, `${err.stack ?? ""}\n${info.componentStack ?? ""}`);
  }

  render() {
    if (!this.state.err) return this.props.children;
    return (
      <div className={this.props.full ? "flex min-h-[50dvh] items-center justify-center p-6" : "flex h-full items-center justify-center rounded-[var(--radius-card)] border border-pgm/40 bg-panel p-3"}>
        <div className="text-center">
          <div className="text-sm font-semibold text-bad">{this.props.name} hit a problem</div>
          <div className="mt-1 text-[11px] text-dim">It was logged for diagnosis. The rest keeps working.</div>
          <button type="button" className="mt-2 rounded-lg border border-line bg-panel-2 px-3 py-1 text-xs" onClick={() => this.setState({ err: null })}>Try again</button>
        </div>
      </div>
    );
  }
}
