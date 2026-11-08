// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { AccountService } from "@gen/kondukter/api/v1/account_pb";
import { AdminService } from "@gen/kondukter/api/v1/admin_pb";
import { AuthService } from "@gen/kondukter/api/v1/auth_pb";
import { CatalogService } from "@gen/kondukter/api/v1/catalog_pb";
import { InstanceService } from "@gen/kondukter/api/v1/instance_pb";
import { PartnerService } from "@gen/kondukter/api/v1/partner_pb";
import { UsageService } from "@gen/kondukter/api/v1/usage_pb";
import { describe, expect, it } from "vitest";
import { allRoutePaths, areas } from "./routes";

// docs/WEB_PAGES.md is the hand-off to the design pass, so the route table in
// code must say what the document says (roadmap P5 exit criterion).
const doc = readFileSync(fileURLToPath(new URL("../../docs/WEB_PAGES.md", import.meta.url)), "utf8");

interface DocRow {
  path: string;
  rpcs: Set<string>;
}

function docRows(): DocRow[] {
  const rows: DocRow[] = [];
  for (const line of doc.split("\n")) {
    const cells = line.split("|").map((c) => c.trim());
    const path = /^`(\/[^`]*)`$/.exec(cells[1] ?? "")?.[1];
    if (!path) continue;
    // Columns: route | role | data | actions | RPCs
    const rpcs = new Set(Array.from((cells[5] ?? "").matchAll(/`?(\w+Service\.(?:\w+|\*))`?/g), (m) => m[1] as string));
    rows.push({ path, rpcs });
  }
  return rows;
}

describe("routes.ts and docs/WEB_PAGES.md", () => {
  const rows = docRows();

  it("document the same set of routes", () => {
    expect(rows.map((r) => r.path).sort()).toEqual(allRoutePaths().sort());
  });

  it("name the same RPCs for every page", () => {
    for (const page of areas.flatMap((a) => a.pages)) {
      const row = rows.find((r) => r.path === page.path);
      expect([...(row?.rpcs ?? [])].sort(), page.path).toEqual([...page.rpcs].sort());
    }
  });
});

describe("the RPCs the pages rely on", () => {
  const services = {
    AuthService,
    CatalogService,
    InstanceService,
    AccountService,
    UsageService,
    PartnerService,
    AdminService,
  } as const;

  // RPCs a page may list before the proto has them. None now: the stage page
  // polls ListNodes and ListInstances, so WatchFleet was never added.
  const notYet = new Set<string>();

  it("exist in the generated services", () => {
    for (const page of areas.flatMap((a) => a.pages)) {
      for (const rpc of page.rpcs) {
        if (notYet.has(rpc) || rpc.endsWith(".*")) continue;
        const [serviceName, method] = rpc.split(".") as [keyof typeof services, string];
        const service = services[serviceName];
        const local = method.charAt(0).toLowerCase() + method.slice(1);
        expect(service, `${page.path}: ${rpc}`).toBeDefined();
        expect(Object.keys(service.method), `${page.path}: ${rpc}`).toContain(local);
      }
    }
  });
});
