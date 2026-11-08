import { Role } from "@gen/kondukter/api/v1/types_pb";
import { describe, expect, it } from "vitest";
import { areaOf, canEnter, homePath, landingPath } from "./roles";

describe("homePath", () => {
  it("gives every role its own area", () => {
    expect(homePath(Role.CUSTOMER)).toBe("/app/instances");
    expect(homePath(Role.PARTNER)).toBe("/partner/nodes");
    expect(homePath(Role.OPERATOR)).toBe("/admin/nodes");
  });
});

describe("areaOf", () => {
  it("reads the first path segment", () => {
    expect(areaOf("/app/instances/42")).toBe("app");
    expect(areaOf("/stage")).toBe("stage");
    expect(areaOf("/login")).toBeUndefined();
    expect(areaOf("/")).toBeUndefined();
    expect(areaOf("/application")).toBeUndefined();
  });
});

describe("canEnter", () => {
  it("lets each role into its own areas only", () => {
    expect(canEnter(Role.CUSTOMER, "/app/keys")).toBe(true);
    expect(canEnter(Role.CUSTOMER, "/admin/nodes")).toBe(false);
    expect(canEnter(Role.PARTNER, "/partner/enroll")).toBe(true);
    expect(canEnter(Role.PARTNER, "/app")).toBe(false);
    expect(canEnter(Role.OPERATOR, "/admin/audit")).toBe(true);
    expect(canEnter(Role.OPERATOR, "/stage")).toBe(true);
    expect(canEnter(Role.OPERATOR, "/partner/nodes")).toBe(false);
  });
});

describe("landingPath", () => {
  it("returns to the page the visitor was sent away from when the role may open it", () => {
    expect(landingPath(Role.CUSTOMER, "/app/instances/42?tab=usage")).toBe("/app/instances/42?tab=usage");
    expect(landingPath(Role.OPERATOR, "/stage?demo=1")).toBe("/stage?demo=1");
  });

  it("falls back to the role's home", () => {
    expect(landingPath(Role.CUSTOMER, undefined)).toBe("/app/instances");
    expect(landingPath(Role.CUSTOMER, "/admin/nodes")).toBe("/app/instances");
    expect(landingPath(Role.PARTNER, "/login")).toBe("/partner/nodes");
  });

  it("never leaves the app", () => {
    expect(landingPath(Role.CUSTOMER, "https://evil.example/app")).toBe("/app/instances");
    expect(landingPath(Role.CUSTOMER, "//evil.example/app/x")).toBe("/app/instances");
    expect(landingPath(Role.CUSTOMER, 42)).toBe("/app/instances");
  });
});
