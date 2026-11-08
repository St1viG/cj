import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";
import { keys } from "./keys";
import { createQueryClient } from "./queryClient";

describe("createQueryClient", () => {
  it("signs the user out when any query comes back unauthenticated", async () => {
    const qc = createQueryClient();
    qc.setQueryData(keys.me, { signedIn: true });

    await qc
      .fetchQuery({
        queryKey: ["anything"],
        queryFn: () => {
          throw new ConnectError("session expired", Code.Unauthenticated);
        },
        retry: false,
      })
      .catch(() => undefined);

    expect(qc.getQueryData(keys.me)).toBeNull();
  });

  it("signs the user out when a mutation comes back unauthenticated", async () => {
    const qc = createQueryClient();
    qc.setQueryData(keys.me, { signedIn: true });

    await qc
      .getMutationCache()
      .build(qc, {
        mutationFn: async () => {
          throw new ConnectError("session expired", Code.Unauthenticated);
        },
      })
      .execute(undefined)
      .catch(() => undefined);

    expect(qc.getQueryData(keys.me)).toBeNull();
  });

  it("keeps the session on other errors", async () => {
    const qc = createQueryClient();
    qc.setQueryData(keys.me, { signedIn: true });

    await qc
      .fetchQuery({
        queryKey: ["anything"],
        queryFn: () => {
          throw new ConnectError("no capacity", Code.ResourceExhausted);
        },
        retry: false,
      })
      .catch(() => undefined);

    expect(qc.getQueryData(keys.me)).toEqual({ signedIn: true });
  });
});
