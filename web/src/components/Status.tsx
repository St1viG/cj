import { InstancePhase, NodeStatus } from "@gen/kondukter/api/v1/types_pb";
import type { ReactNode } from "react";

/** The design's status vocabulary: a glyph, a colour and a word, never colour alone. */
export type StatusKind = "pend" | "sched" | "prov" | "run" | "unr" | "fail" | "quar" | "end" | "drain";

export function phaseKind(p: InstancePhase): StatusKind {
  switch (p) {
    case InstancePhase.PENDING:
      return "pend";
    case InstancePhase.SCHEDULED:
      return "sched";
    case InstancePhase.PROVISIONING:
      return "prov";
    case InstancePhase.RUNNING:
      return "run";
    case InstancePhase.UNREACHABLE:
      return "unr";
    case InstancePhase.FAILED:
    case InstancePhase.LOST:
      return "fail";
    case InstancePhase.TERMINATING:
      return "drain";
    default:
      return "end";
  }
}

export function nodeKind(s: NodeStatus): StatusKind {
  switch (s) {
    case NodeStatus.ENROLLING:
      return "pend";
    case NodeStatus.READY:
      return "run";
    case NodeStatus.DRAINING:
    case NodeStatus.MAINTENANCE:
      return "drain";
    case NodeStatus.UNREACHABLE:
      return "unr";
    case NodeStatus.QUARANTINED:
      return "quar";
    default:
      return "end";
  }
}

/** A status chip: LED plus the word (and any reason) as plain text. */
export function StatusChip({ kind, children }: { kind: StatusKind; children: ReactNode }) {
  return (
    <span className={`chip s-${kind}`}>
      <span className="led" aria-hidden="true" />
      {children}
    </span>
  );
}
