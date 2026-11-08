import { Navigate, Outlet, Route, Routes } from "react-router";
import { RoleRoute } from "./auth/RoleRoute";
import { useMe } from "./auth/AuthProvider";
import { homePath } from "./auth/roles";
import { ErrorState, LoadingState } from "./components/State";
import { AreaLayout } from "./pages/AreaLayout";
import { LoginPage } from "./pages/LoginPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { PagePlaceholder } from "./pages/PagePlaceholder";
import { areas } from "./routes";

/** `/`: signed in goes home, signed out goes to the login page. */
function RootRedirect() {
  const auth = useMe();
  switch (auth.status) {
    case "loading":
      return <LoadingState />;
    case "error":
      return <ErrorState error={auth.error} onRetry={auth.retry} />;
    case "signedIn":
      return <Navigate to={homePath(auth.me.role)} replace />;
    case "signedOut":
      return <Navigate to="/login" replace />;
  }
}

/**
 * Every route of docs/WEB_PAGES.md, built from `routes.ts`: /login, then one
 * role-guarded group per area (/app, /partner, /admin, /stage).
 */
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<RootRedirect />} />
      <Route path="/login" element={<LoginPage />} />
      {areas.map((area) => {
        const base = `/${area.area}`;
        return (
          <Route key={area.area} element={<RoleRoute role={area.role} />}>
            <Route path={base} element={area.shell ? <AreaLayout area={area} /> : <Outlet />}>
              {area.pages.map((page) => {
                const element = page.element ?? <PagePlaceholder page={page} />;
                // A page at the area's own path is its index route.
                return page.path === base ? (
                  <Route key={page.path} index element={element} />
                ) : (
                  <Route key={page.path} path={page.path.slice(base.length + 1)} element={element} />
                );
              })}
              {area.shell && <Route index element={<Navigate to={area.home} replace />} />}
              <Route path="*" element={<NotFoundPage />} />
            </Route>
          </Route>
        );
      })}
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  );
}
