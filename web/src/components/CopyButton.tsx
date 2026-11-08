import { useState } from "react";
import { copyText } from "../lib/clipboard";

/**
 * Copies `text` and says whether it worked. Show the text next to it as well:
 * when the browser refuses (plain HTTP, no permission) a person can still
 * select it by hand.
 */
export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  return (
    <>
      <button
        type="button"
        className="sm"
        onClick={() => {
          void copyText(text).then((ok) => setState(ok ? "copied" : "failed"));
        }}
      >
        {label}
      </button>
      {state === "copied" && <span role="status" className="rsn"> Copied</span>}
      {state === "failed" && <span role="status" className="msg err"> Could not copy; select the text and copy it.</span>}
    </>
  );
}
