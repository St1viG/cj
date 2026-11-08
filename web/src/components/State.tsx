import type { ReactNode } from "react";
import { errorMessage } from "../api/errors";

/**
 * The three states every page must have besides its data (WEB_PAGES.md):
 * loading, empty and error.
 */

export function LoadingState({ label = "Loading…" }: { label?: string }) {
  return (
    <div className="skel" aria-busy="true">
      <span role="status" className="rsn">
        {label}
      </span>
      <span className="sk" style={{ width: "70%" }} />
      <span className="sk" style={{ width: "92%" }} />
      <span className="sk" style={{ width: "54%" }} />
    </div>
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return (
    <div className="empty">
      <p>{children}</p>
    </div>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div role="alert" className="errp">
      <p>{errorMessage(error)}</p>
      {onRetry && (
        <button type="button" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}

/** The slice of a TanStack query result that QueryState reads. */
interface QueryLike<T> {
  isPending: boolean;
  isError: boolean;
  error: unknown;
  data: T | undefined;
  refetch: () => unknown;
}

/**
 * Renders a query: loading while it has no data, the error with a retry when
 * it failed, `empty` when `isEmpty` says there is nothing, else `children`.
 */
export function QueryState<T>({
  query,
  isEmpty,
  empty = "Nothing here yet.",
  children,
}: {
  query: QueryLike<T>;
  isEmpty?: (data: T) => boolean;
  empty?: ReactNode;
  children: (data: T) => ReactNode;
}) {
  if (query.isPending) return <LoadingState />;
  if (query.isError && query.data === undefined) {
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  }
  const data = query.data as T;
  if (isEmpty?.(data)) return <EmptyState>{empty}</EmptyState>;
  return <>{children(data)}</>;
}
