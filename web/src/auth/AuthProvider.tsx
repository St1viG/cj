import type { Principal } from "@gen/kondukter/api/v1/auth_pb";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useCallback, useContext, useMemo, useRef } from "react";
import { useApi } from "../api/ApiProvider";
import { isUnauthenticated, toConnectError } from "../api/errors";
import { keys } from "../api/keys";

export type Auth =
  | { status: "loading" }
  /** `afterLogout`: the person chose to sign out, as opposed to a session that ended behind their back. */
  | { status: "signedOut"; login: Login; afterLogout: boolean }
  | { status: "signedIn"; me: Principal; logout: () => Promise<void> }
  | { status: "error"; error: Error; retry: () => void };

type Login = (email: string, password: string) => Promise<Principal>;

const AuthContext = createContext<Auth | null>(null);

/**
 * Holds the signed-in principal. It asks `AuthService.GetMe` once; a 401 means
 * signed out, anything else (the server is down) is an error the guards show
 * with a retry. Login and logout write the answer into the query cache, so the
 * route guards react at once.
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const { auth } = useApi();
  const qc = useQueryClient();
  // Set by logout() before the cache changes, so the guards can tell it from an expired session.
  const loggedOut = useRef(false);

  const meQuery = useQuery({
    queryKey: keys.me,
    queryFn: async (): Promise<Principal | null> => {
      try {
        return (await auth.getMe({})).principal ?? null;
      } catch (e) {
        if (isUnauthenticated(e)) return null;
        throw e;
      }
    },
    retry: false,
    // GetMe touches the session; asking again on a timer would keep it alive forever.
    refetchInterval: false,
    staleTime: Number.POSITIVE_INFINITY,
  });

  const { mutateAsync: doLogin } = useMutation({
    mutationFn: async ({ email, password }: { email: string; password: string }) => {
      const resp = await auth.login({ email, password });
      if (!resp.principal) throw new Error("the server sent no principal");
      return resp.principal;
    },
    onSuccess: (principal) => {
      loggedOut.current = false;
      qc.setQueryData(keys.me, principal);
    },
  });

  const login = useCallback<Login>((email, password) => doLogin({ email, password }), [doLogin]);

  const logout = useCallback(async () => {
    try {
      await auth.logout({});
    } finally {
      // Whatever the server said, this browser is done with the session. The
      // rest of the cache belongs to the old user; `me` stays so its observer
      // sees the change.
      loggedOut.current = true;
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== keys.me[0] });
      qc.setQueryData(keys.me, null);
    }
  }, [auth, qc]);

  const { isPending, isError, error, data, refetch } = meQuery;
  const value = useMemo<Auth>(() => {
    if (isPending) return { status: "loading" };
    if (isError) return { status: "error", error: toConnectError(error), retry: () => void refetch() };
    if (data) return { status: "signedIn", me: data, logout };
    return { status: "signedOut", login, afterLogout: loggedOut.current };
  }, [isPending, isError, error, data, refetch, login, logout]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

/** The current session: `useMe().status` is loading, signedOut, signedIn or error. */
export function useMe(): Auth {
  const auth = useContext(AuthContext);
  if (!auth) throw new Error("useMe needs an <AuthProvider>");
  return auth;
}

/**
 * The signed-in principal, for pages inside a role-guarded area, where the
 * guard has already made sure there is one.
 */
export function useCurrentUser(): Principal {
  const auth = useMe();
  if (auth.status !== "signedIn") throw new Error("useCurrentUser outside a signed-in area");
  return auth.me;
}
