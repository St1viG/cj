import type { ReactNode } from "react";
import { EmptyState, ErrorState, LoadingState } from "./State";

export interface Column<T> {
  header: string;
  cell: (row: T) => ReactNode;
}

export interface DataTableProps<T> {
  columns: readonly Column<T>[];
  /** `undefined` means not loaded yet. */
  rows: readonly T[] | undefined;
  rowKey: (row: T) => string;
  loading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  /** Shown when there are no rows. */
  empty?: ReactNode;
  caption?: string;
}

/**
 * A plain table with the loading, empty and error states built in. A failed
 * refetch keeps the rows already loaded and shows the error above them.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading = false,
  error,
  onRetry,
  empty = "Nothing here yet.",
  caption,
}: DataTableProps<T>) {
  const failed = error !== undefined && error !== null;
  if (loading && rows === undefined && !failed) return <LoadingState />;
  if (rows === undefined || rows.length === 0) {
    if (failed) return <ErrorState error={error} onRetry={onRetry} />;
    return <EmptyState>{empty}</EmptyState>;
  }
  return (
    <>
      {failed && <ErrorState error={error} onRetry={onRetry} />}
      <table>
        {caption && <caption>{caption}</caption>}
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.header} scope="col">
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={rowKey(row)}>
              {columns.map((c) => (
                <td key={c.header}>{c.cell(row)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
