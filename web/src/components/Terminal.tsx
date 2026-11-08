import { CopyButton } from "./CopyButton";

/** A copyable command in a terminal-style block, e.g. the `ssh` line of an instance. */
export function Terminal({ command, title = "ssh" }: { command: string; title?: string }) {
  return (
    <div className="term">
      <header>
        <span className="lbl">{title}</span>
        <span className="acts">
          <CopyButton text={command} />
        </span>
      </header>
      <pre>
        <code>{command}</code>
      </pre>
    </div>
  );
}
