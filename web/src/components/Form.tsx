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
  resetOnSuccess = false,
  error: outsideError,
  inline = false,
  children,
}: {
  onSubmit: (data: FormData) => void | Promise<void>;
  submitLabel?: string;
  /** Clear the fields after a submit that succeeded (a form that adds a row to a list). */
  resetOnSuccess?: boolean;
  /** An error the caller wants shown, e.g. from a mutation it manages itself. */
  error?: unknown;
  /** A compact row of fields (a filter bar) instead of a stacked form. */
  inline?: boolean;
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
    const form = e.currentTarget;
    const data = new FormData(form);
    setPending(true);
    setError(undefined);
    try {
      await onSubmit(data);
      if (resetOnSuccess && mounted.current) form.reset();
    } catch (err) {
      // A successful submit often navigates away, so the form may be gone.
      if (mounted.current) setError(err);
    } finally {
      if (mounted.current) setPending(false);
    }
  }

  const shown = error ?? outsideError;
  return (
    <form className={inline ? "form filters" : "form"} onSubmit={(e) => void handle(e)}>
      <fieldset disabled={pending}>
        {children}
        <div className="actions">
          <button type="submit" className="p">
            {submitLabel}
          </button>
        </div>
      </fieldset>
      {pending && (
        <p role="status" className="working">
          Working…
        </p>
      )}
      {shown !== undefined && shown !== null && (
        <p role="alert" className="msg err">
          {errorMessage(shown)}
        </p>
      )}
    </form>
  );
}
