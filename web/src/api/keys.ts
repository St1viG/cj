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
};
