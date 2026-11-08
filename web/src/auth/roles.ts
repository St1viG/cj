import { Role } from "@gen/kondukter/api/v1/types_pb";

/** The route areas of WEB_PAGES.md; each belongs to exactly one role. */
export type Area = "app" | "partner" | "admin" | "stage";

const roleOfArea: Record<Area, Role> = {
  app: Role.CUSTOMER,
  partner: Role.PARTNER,
  admin: Role.OPERATOR,
  stage: Role.OPERATOR,
};

/** Where a role lands after login and when it opens a page it may not see. */
export function homePath(role: Role): string {
  switch (role) {
    case Role.CUSTOMER:
      return "/app/instances";
    case Role.PARTNER:
      return "/partner/nodes";
    case Role.OPERATOR:
      return "/admin/nodes";
    default:
      return "/login";
  }
}

export function roleLabel(role: Role): string {
  switch (role) {
    case Role.CUSTOMER:
      return "customer";
    case Role.PARTNER:
      return "partner";
    case Role.OPERATOR:
      return "operator";
    default:
      return "unknown";
  }
}

/** The area a path belongs to, or undefined for /login, / and unknown paths. */
export function areaOf(pathname: string): Area | undefined {
  const first = pathname.split("/")[1];
  return first === "app" || first === "partner" || first === "admin" || first === "stage" ? first : undefined;
}

/** Whether `role` may open `pathname`. */
export function canEnter(role: Role, pathname: string): boolean {
  const area = areaOf(pathname);
  return area !== undefined && roleOfArea[area] === role;
}

/**
 * Where to go after login: the page the person was sent away from if their
 * role may open it, else their home. `from` is only ever an in-app path, so a
 * crafted link cannot bounce a login to another site.
 */
export function landingPath(role: Role, from: unknown): string {
  if (typeof from === "string" && from.startsWith("/") && !from.startsWith("//")) {
    const pathname = from.split(/[?#]/)[0] ?? "";
    if (canEnter(role, pathname)) return from;
  }
  return homePath(role);
}
