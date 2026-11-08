import type { ReactNode } from "react";
import { AuditPage } from "./AuditPage";
import { CatalogPage } from "./CatalogPage";
import { InstancesPage } from "./InstancesPage";
import { NodesPage } from "./NodesPage";

/** The operator area's pages (roadmap P5.4), by route pattern. */
export const adminPages: Record<string, ReactNode> = {
  "/admin/nodes": <NodesPage />,
  "/admin/instances": <InstancesPage />,
  "/admin/catalog": <CatalogPage />,
  "/admin/audit": <AuditPage />,
};
