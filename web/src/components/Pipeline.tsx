import { InstancePhase } from "@gen/kondukter/api/v1/types_pb";
import { phaseLabel } from "../lib/format";
import { phaseKind } from "./Status";

const steps = [
  { phase: InstancePhase.PENDING, word: "Pending" },
  { phase: InstancePhase.SCHEDULED, word: "Scheduled" },
  { phase: InstancePhase.PROVISIONING, word: "Provisioning" },
  { phase: InstancePhase.RUNNING, word: "Running" },
];

/** Where on the happy path the phase sits, and which off-path state (if any) to show below it. */
function place(phase: InstancePhase): { at: number; off?: string; ended: boolean } {
  switch (phase) {
    case InstancePhase.PENDING:
      return { at: 0, ended: false };
    case InstancePhase.SCHEDULED:
      return { at: 1, ended: false };
    case InstancePhase.PROVISIONING:
      return { at: 2, ended: false };
    case InstancePhase.RUNNING:
      return { at: 3, ended: false };
    case InstancePhase.UNREACHABLE:
      return { at: 3, off: "unreachable", ended: false };
    case InstancePhase.TERMINATING:
      return { at: 3, off: "terminating", ended: true };
    case InstancePhase.TERMINATED:
      return { at: 3, off: "terminated", ended: true };
    case InstancePhase.FAILED:
      return { at: 2, off: "failed", ended: true };
    case InstancePhase.LOST:
      return { at: 3, off: "lost", ended: true };
    default:
      return { at: -1, ended: false };
  }
}

/**
 * The instance lifecycle as a stepper: completed steps are green, the current
 * one glows, and a light travels toward the next while the instance is
 * between states. Off-path phases (unreachable, failed, terminated…) hang
 * below it. The words are drawn from attributes, so the page's text holds the
 * phase once, in the status chip.
 */
export function Pipeline({ phase }: { phase: InstancePhase }) {
  const { at, off, ended } = place(phase);
  const travelling = !ended && at >= 0 && at < 3;
  return (
    <div className="pipewrap">
      <div role="img" aria-label={`Lifecycle: ${phaseLabel(phase)}`}>
        <div className="pipe">
          {steps.map((s, i) => {
            const done = i < at || (ended && i <= at && !(phase === InstancePhase.FAILED && i === at));
            const now = i === at && !ended;
            const cls = done ? "done" : now ? `now${s.phase === InstancePhase.RUNNING ? " run" : ""}` : "";
            return (
              <span key={s.phase} style={{ display: "contents" }}>
                <span className={`st ${cls}`}>
                  <span className="nd" />
                  <span className="cw" data-w={s.word} />
                </span>
                {i < steps.length - 1 && <span className={`ln ${i < at ? "done" : i === at && travelling ? "go" : ""}`} />}
              </span>
            );
          })}
        </div>
        {off && (
          <div className="branch">
            <span className={`chip s-${phaseKind(phase)}`}>
              <span className="led" aria-hidden="true" />
              <span className="cw" data-w={off} />
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
