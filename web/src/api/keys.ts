/**
 * TanStack Query keys. Every page builds its keys here so a stream update, a
 * mutation or a logout can find the entries it must change. Pages add their
 * own keys under these roots when they land (P5.2 to P5.5).
 */
export const keys = {
  /** The signed-in principal; `null` data means signed out. */
  me: ["me"] as const,
  instances: {
    all: ["instances"] as const,
    /** Prefix of every list query, whatever its parameters. */
    lists: ["instances", "list"] as const,
    list: (params: object = {}) => ["instances", "list", params] as const,
    detail: (id: string) => ["instances", "detail", id] as const,
  },
  catalog: {
    offers: (filter: object = {}) => ["catalog", "offers", filter] as const,
    images: (offerId: string, arch: string) => ["catalog", "images", offerId, arch] as const,
  },
  account: {
    sshKeys: ["account", "ssh-keys"] as const,
    apiKeys: ["account", "api-keys"] as const,
  },
  usage: {
    list: (period: object = {}) => ["usage", "list", period] as const,
  },
  /** Every view of nodes starts with this root, so a status change can refetch them all. */
  fleet: {
    all: ["fleet"] as const,
    sites: ["fleet", "sites"] as const,
    partnerNodes: (siteId: string) => ["fleet", "partner-nodes", siteId] as const,
    utilization: (params: object) => ["fleet", "utilization", params] as const,
    adminNodes: (params: object) => ["fleet", "admin-nodes", params] as const,
    /** Under `fleet` as well: a force terminate changes the nodes' instance counts. */
    adminInstances: (params: object) => ["fleet", "admin-instances", params] as const,
  },
  admin: {
    catalog: ["admin", "catalog"] as const,
    audit: (params: object) => ["admin", "audit", params] as const,
  },
};
