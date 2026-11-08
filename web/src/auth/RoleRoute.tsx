import type { Role } from "@gen/kondukter/api/v1/types_pb";
import { Navigate, Outlet, useLocation } from "react-router";
import { ErrorState, LoadingState } from "../components/State";
import { useMe } from "./AuthProvider";
import { homePath } from "./roles";

/**
 * Guards a group of routes (`/app`, `/partner`, `/admin`, `/stage`):
 * signed out goes to /login and comes back after, the wrong role goes to its
 * own home, and the right role sees the nested routes.
 */
export function RoleRoute({ role }: { role: Role }) {
  const auth = useMe();
  const location = useLocation();

  switch (auth.status) {
    case "loading":
      return <LoadingState />;
    case "error":
      return <ErrorState error={auth.error} onRetry={auth.retry} />;
    case "signedOut":
      // A session that ended behind the person's back returns them to where they were
      // after login; a logout they chose starts the next login at home.
      return <Navigate to="/login" replace state={auth.afterLogout ? undefined : { from: location.pathname + location.search }} />;
    case "signedIn":
      return auth.me.role === role ? <Outlet /> : <Navigate to={homePath(auth.me.role)} replace />;
  }
}
