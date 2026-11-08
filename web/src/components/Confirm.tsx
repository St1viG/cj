import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api/errors";

/**
 * A button that asks before it acts: the first click swaps it for the
 * question with Confirm and Cancel, in place (no browser dialog). A failure
 * stays visible next to the buttons. With `reason` (the field's label) the
 * question also has a text field, and `onConfirm` gets what was typed,
 * trimmed.
 */
export function Confirm({
  label,
  message,
  confirmLabel = label,
  reason,
  onConfirm,
  disabled = false,
}: {
  label: string;
  message: string;
  confirmLabel?: string;
  reason?: string;
  onConfirm: (reason: string) => void | Promise<void>;
  disabled?: boolean;
}) {
  const [asking, setAsking] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(undefined);
  const [text, setText] = useState("");
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
      await onConfirm(text.trim());
      if (mounted.current) {
        setAsking(false);
        setText("");
      }
    } catch (err) {
      if (mounted.current) setError(err);
    } finally {
      if (mounted.current) setPending(false);
    }
  }

  // Destructive actions read red until pressed; the rest stay neutral.
  const danger = /terminate|delete|revoke|quarantine|force/i.test(label) && !/^un/i.test(label);
  if (!asking) {
    return (
      <button type="button" className={`sm${danger ? " d" : ""}`} disabled={disabled} onClick={() => setAsking(true)}>
        {label}
      </button>
    );
  }
  return (
    <span role="group" aria-label={message} className="conf">
      <span>{message}</span>
      {reason !== undefined && (
        <>
          <label>
            {reason} <input value={text} disabled={pending} onChange={(e) => setText(e.target.value)} />
          </label>
        </>
      )}
      <button type="button" className={`sm${danger ? " d solid" : " p"}`} disabled={pending} onClick={() => void run()}>
        {confirmLabel}
      </button>
      <button
        type="button"
        className="sm g"
        disabled={pending}
        onClick={() => {
          setAsking(false);
          setError(undefined);
          setText("");
        }}
      >
        Cancel
      </button>
      {error !== undefined && (
        <span role="alert" className="msg err">
          {errorMessage(error)}
        </span>
      )}
    </span>
  );
}
