import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api/errors";

/**
 * A button that asks before it acts: the first click swaps it for the
 * question with Confirm and Cancel, in place (no browser dialog). A failure
 * stays visible next to the buttons.
 */
export function Confirm({
  label,
  message,
  confirmLabel = label,
  onConfirm,
  disabled = false,
}: {
  label: string;
  message: string;
  confirmLabel?: string;
  onConfirm: () => void | Promise<void>;
  disabled?: boolean;
}) {
  const [asking, setAsking] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function run() {
    setPending(true);
    setError(undefined);
    try {
      await onConfirm();
      if (mounted.current) setAsking(false);
    } catch (err) {
      if (mounted.current) setError(err);
    } finally {
      if (mounted.current) setPending(false);
    }
  }

  if (!asking) {
    return (
      <button type="button" disabled={disabled} onClick={() => setAsking(true)}>
        {label}
      </button>
    );
  }
  return (
    <span role="group" aria-label={message}>
      <span>{message}</span>{" "}
      <button type="button" disabled={pending} onClick={() => void run()}>
        {confirmLabel}
      </button>{" "}
      <button
        type="button"
        disabled={pending}
        onClick={() => {
          setAsking(false);
          setError(undefined);
        }}
      >
        Cancel
      </button>
      {error !== undefined && <span role="alert"> {errorMessage(error)}</span>}
    </span>
  );
}
