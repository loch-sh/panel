import { Button, Result } from "antd";
import { Component, type ErrorInfo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router";

/** A code chunk that failed to load (new deployment, dev server restarted). */
function isChunkError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /dynamically imported module|Importing a module script failed|Loading chunk|error loading dynamically/i.test(
    message,
  );
}

function ErrorFallback({ error }: Readonly<{ error: unknown }>) {
  const { t } = useTranslation();
  const chunk = isChunkError(error);
  return (
    <Result
      status="error"
      title={t(chunk ? "errorBoundary.chunkTitle" : "errorBoundary.title")}
      subTitle={t(
        chunk ? "errorBoundary.chunkDescription" : "errorBoundary.description",
      )}
      extra={
        <Button type="primary" onClick={() => window.location.reload()}>
          {t("errorBoundary.reload")}
        </Button>
      }
    />
  );
}

interface State {
  error: unknown;
}

class Boundary extends Component<Readonly<{ children: ReactNode }>, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: unknown): State {
    return { error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    // Kept in the console for the developer; the page shows a translated
    // message instead of going blank.
    console.error("Page error:", error, info.componentStack);
  }

  render() {
    return this.state.error ? (
      <ErrorFallback error={this.state.error} />
    ) : (
      this.props.children
    );
  }
}

/**
 * Catches the errors of a page so that one failing page never blanks the
 * whole app (the menu stays usable). Reset on navigation: the boundary is
 * keyed by the location.
 */
export default function ErrorBoundary({
  children,
}: Readonly<{ children: ReactNode }>) {
  const { pathname } = useLocation();
  return <Boundary key={pathname}>{children}</Boundary>;
}
