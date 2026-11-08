import type { ReactNode } from "react";
import { BillingPage } from "./BillingPage";
import { InstanceDetailPage } from "./InstanceDetailPage";
import { InstancesPage } from "./InstancesPage";
import { KeysPage } from "./KeysPage";
import { NewInstancePage } from "./NewInstancePage";
import { OffersPage } from "./OffersPage";
import { UsagePage } from "./UsagePage";

/** The customer area's pages (roadmap P5.2), by route pattern. */
export const customerPages: Record<string, ReactNode> = {
  "/app/offers": <OffersPage />,
  "/app/instances": <InstancesPage />,
  "/app/instances/new": <NewInstancePage />,
  "/app/instances/:id": <InstanceDetailPage />,
  "/app/keys": <KeysPage />,
  "/app/usage": <UsagePage />,
  "/app/billing": <BillingPage />,
};
