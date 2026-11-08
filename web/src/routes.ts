import { Role } from "@gen/kondukter/api/v1/types_pb";
import type { Area } from "./auth/roles";

/**
 * The route map of docs/WEB_PAGES.md as data. The router, the navigation and
 * the placeholder pages all read it, and a test checks it against the
 * document, so the two cannot drift apart.
 */

export interface PageDef {
  /** Absolute route pattern, exactly as in WEB_PAGES.md. */
  path: string;
  title: string;
  /** The roadmap item that fills the page in. */
  item: "P5.2" | "P5.3" | "P5.4" | "P5.5";
  /** The RPCs that back the page, as in WEB_PAGES.md. */
  rpcs: readonly string[];
  /** Listed in the area's navigation. */
  nav?: boolean;
}

export interface AreaDef {
  area: Area;
  role: Role;
  title: string;
  /** Where `base` redirects to. */
  home: string;
  /** Wraps the pages in the header with navigation and logout. */
  shell: boolean;
  pages: readonly PageDef[];
}

export const areas: readonly AreaDef[] = [
  {
    area: "app",
    role: Role.CUSTOMER,
    title: "Customer",
    home: "/app/instances",
    shell: true,
    pages: [
      {
        path: "/app/offers",
        title: "Offers",
        item: "P5.2",
        nav: true,
        rpcs: ["CatalogService.ListOffers"],
      },
      {
        path: "/app/instances",
        title: "Instances",
        item: "P5.2",
        nav: true,
        rpcs: ["InstanceService.ListInstances", "InstanceService.WatchInstance", "InstanceService.TerminateInstance"],
      },
      {
        path: "/app/instances/new",
        title: "New instance",
        item: "P5.2",
        rpcs: [
          "CatalogService.ListOffers",
          "CatalogService.ListImages",
          "AccountService.ListSSHKeys",
          "InstanceService.CreateInstance",
        ],
      },
      {
        path: "/app/instances/:id",
        title: "Instance",
        item: "P5.2",
        rpcs: ["InstanceService.GetInstance", "InstanceService.WatchInstance", "InstanceService.TerminateInstance"],
      },
      { path: "/app/keys", title: "Keys", item: "P5.2", nav: true, rpcs: ["AccountService.*"] },
      { path: "/app/usage", title: "Usage", item: "P5.2", nav: true, rpcs: ["UsageService.ListUsage"] },
    ],
  },
  {
    area: "partner",
    role: Role.PARTNER,
    title: "Partner",
    home: "/partner/nodes",
    shell: true,
    pages: [
      { path: "/partner/sites", title: "Sites", item: "P5.3", nav: true, rpcs: ["PartnerService.ListSites"] },
      {
        path: "/partner/nodes",
        title: "Nodes",
        item: "P5.3",
        nav: true,
        rpcs: ["PartnerService.ListNodes", "PartnerService.DrainNode", "PartnerService.UndrainNode", "PartnerService.ListSites"],
      },
      {
        path: "/partner/enroll",
        title: "Enroll a node",
        item: "P5.3",
        nav: true,
        rpcs: ["PartnerService.CreateEnrollmentToken", "PartnerService.ListSites"],
      },
      {
        path: "/partner/utilization",
        title: "Utilization",
        item: "P5.3",
        nav: true,
        rpcs: ["PartnerService.GetUtilization", "PartnerService.ListSites"],
      },
    ],
  },
  {
    area: "admin",
    role: Role.OPERATOR,
    title: "Operator",
    home: "/admin/nodes",
    shell: true,
    pages: [
      {
        path: "/admin/nodes",
        title: "Nodes",
        item: "P5.4",
        nav: true,
        rpcs: ["AdminService.ListNodes", "AdminService.QuarantineNode", "PartnerService.DrainNode"],
      },
      {
        path: "/admin/instances",
        title: "Instances",
        item: "P5.4",
        nav: true,
        rpcs: ["AdminService.ListInstances", "AdminService.ForceTerminateInstance"],
      },
      { path: "/admin/catalog", title: "Catalog", item: "P5.4", nav: true, rpcs: ["AdminService.ListCatalog"] },
      { path: "/admin/audit", title: "Audit log", item: "P5.4", nav: true, rpcs: ["AdminService.ListAuditLog"] },
    ],
  },
  {
    area: "stage",
    role: Role.OPERATOR,
    title: "Stage",
    home: "/stage",
    shell: false,
    pages: [
      {
        path: "/stage",
        title: "Fleet",
        item: "P5.5",
        rpcs: ["AdminService.WatchFleet", "AdminService.ListNodes", "AdminService.ListInstances"],
      },
    ],
  },
];

/** Every route pattern the router serves, for tests and docs. */
export function allRoutePaths(): string[] {
  return ["/login", ...areas.flatMap((a) => (a.shell ? [`/${a.area}`] : [])), ...areas.flatMap((a) => a.pages.map((p) => p.path))];
}
