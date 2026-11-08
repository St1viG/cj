import type { ReactNode } from "react";
import { StagePage } from "./StagePage";

/** The projector page (roadmap P5.5), by route pattern. */
export const stagePages: Record<string, ReactNode> = {
  "/stage": <StagePage />,
};
