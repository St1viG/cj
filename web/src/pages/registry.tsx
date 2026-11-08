import type { ReactNode } from "react";
import { customerPages } from "./customer";

/**
 * The finished pages by route pattern. A route in `routes.ts` with no entry
 * here renders a placeholder naming its RPCs. Each roadmap item (P5.2 to P5.5)
 * adds its area's map.
 */
export const pageElements: Readonly<Record<string, ReactNode>> = {
  ...customerPages,
};
