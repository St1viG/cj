import { type FormEvent, type ReactNode, useEffect, useRef, useState } from "react";
import { errorMessage } from "../api/errors";

/**
 * A form that owns its submit state: the fields are disabled while the call
 * runs, and a failure shows under the fields. `onSubmit` gets the browser's
 * FormData, so repeated names (`data.getAll("ssh_key")`) work.
 */
export function Form({
  onSubmit,
  submitLabel = "Submit",
  error: outsideError,
  children,
}: {
  onSubmit: (data: FormData) => void | Promise<void>;
  submitLabel?: string;
  /** An error the caller wants shown, e.g. from a mutation it manages itself. */
  error?: unknown;
  children: ReactNode;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function handle(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (pending) return;
    const data = new FormData(e.currentTarget);
    setPending(true);
    setError(undefined);
    try {
      await onSubmit(data);
    } catch (err) {
      // A successful submit often navigates away, so the form may be gone.
      if (mounted.current) setError(err);
    } finally {
      if (mounted.current) setPending(false);
    }
  }

  const shown = error ?? outsideError;
  return (
    <form onSubmit={(e) => void handle(e)}>
      <fieldset disabled={pending}>
        {children}
        <button type="submit">{submitLabel}</button>
      </fieldset>
      {pending && <p role="status">Working…</p>}
      {shown !== undefined && shown !== null && <p role="alert">{errorMessage(shown)}</p>}
    </form>
  );
}
