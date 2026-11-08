import type { ReactNode } from "react";
import { EarningsPage } from "./EarningsPage";
import { EnrollPage } from "./EnrollPage";
import { NodesPage } from "./NodesPage";
import { SitesPage } from "./SitesPage";
import { UtilizationPage } from "./UtilizationPage";

/** The partner area's pages (roadmap P5.3), by route pattern. */
export const partnerPages: Record<string, ReactNode> = {
  "/partner/sites": <SitesPage />,
  "/partner/nodes": <NodesPage />,
  "/partner/enroll": <EnrollPage />,
  "/partner/utilization": <UtilizationPage />,
  "/partner/earnings": <EarningsPage />,
};
