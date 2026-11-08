import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { AuthService, type Principal, PrincipalSchema } from "@gen/kondukter/api/v1/auth_pb";
import { OrgSchema, Role, UserSchema } from "@gen/kondukter/api/v1/types_pb";
import { render } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { AppRoutes } from "../AppRoutes";
import { createQueryClient } from "../api/queryClient";
import { Providers } from "../Providers";

/** The seeded demo accounts (kondukter seed accounts), minus the real password. */
export const accounts = {
  customer: { email: "customer@demo.local", role: Role.CUSTOMER, org: "acme" },
  partner: { email: "partner@demo.local", role: Role.PARTNER, org: "demo-dc" },
  operator: { email: "operator@demo.local", role: Role.OPERATOR, org: "kondukter" },
} as const;

export type AccountName = keyof typeof accounts;
export const PASSWORD = "cj";

export function principalOf(name: AccountName): Principal {
  const a = accounts[name];
  return create(PrincipalSchema, {
    user: create(UserSchema, { id: `user-${name}`, email: a.email, displayName: name }),
    org: create(OrgSchema, { id: `org-${name}`, name: a.org }),
    role: a.role,
  });
}

/**
 * An in-memory AuthService with the server's behavior: a session cookie is
 * modeled as `session`; GetMe without one is Unauthenticated.
 */
export function fakeAuthServer(signedInAs?: AccountName) {
  let session: Principal | undefined = signedInAs ? principalOf(signedInAs) : undefined;
  let down = false;
  const calls = { getMe: 0, login: 0, logout: 0 };

  const transport: Transport = createRouterTransport(({ service }) => {
    service(AuthService, {
      getMe: () => {
        calls.getMe++;
        if (down) throw new ConnectError("the server is unreachable", Code.Unavailable);
        if (!session) throw new ConnectError("missing credentials", Code.Unauthenticated);
        return { principal: session };
      },
      login: (req) => {
        calls.login++;
        const name = (Object.keys(accounts) as AccountName[]).find((n) => accounts[n].email === req.email);
        if (!name || req.password !== PASSWORD) {
          throw new ConnectError("wrong email or password", Code.Unauthenticated);
        }
        session = principalOf(name);
        return { principal: session };
      },
      logout: () => {
        calls.logout++;
        session = undefined;
        return {};
      },
    });
  });

  return {
    transport,
    calls,
    /** The session ended behind the page's back (expired, logged out elsewhere). */
    expire: () => {
      session = undefined;
    },
    setDown: (value: boolean) => {
      down = value;
    },
  };
}

function LocationProbe() {
  const l = useLocation();
  return <output data-testid="location">{l.pathname + l.search}</output>;
}

/** Renders the whole route tree at `path` against an in-memory server (any fake with a transport). */
export function renderApp<S extends { transport: Transport } = ReturnType<typeof fakeAuthServer>>(
  path: string,
  server: S = fakeAuthServer() as unknown as S,
) {
  // No retries: a failing call shows its error at once instead of after the real backoff.
  const queryClient = createQueryClient();
  queryClient.setDefaultOptions({ queries: { ...queryClient.getDefaultOptions().queries, retry: false } });
  const view = render(
    <Providers transport={server.transport} queryClient={queryClient}>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
        <LocationProbe />
      </MemoryRouter>
    </Providers>,
  );
  return { ...view, server };
}
