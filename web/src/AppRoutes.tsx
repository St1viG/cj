import { Navigate, Outlet, Route, Routes } from "react-router";
import { RoleRoute } from "./auth/RoleRoute";
import { AreaLayout } from "./pages/AreaLayout";
import { LandingPage } from "./pages/LandingPage";
import { LoginPage } from "./pages/LoginPage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { PagePlaceholder } from "./pages/PagePlaceholder";
import { ProductsPage } from "./pages/ProductsPage";
import { pageElements } from "./pages/registry";
import { areas } from "./routes";

/**
 * Every route of docs/WEB_PAGES.md, built from `routes.ts`: the guest home page, /login, then one
 * role-guarded group per area (/app, /partner, /admin, /stage).
 */
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<LandingPage />} />
      <Route path="/products" element={<ProductsPage />} />
      <Route path="/login" element={<LoginPage />} />
      {areas.map((area) => {
        const base = `/${area.area}`;
        return (
          <Route key={area.area} element={<RoleRoute role={area.role} />}>
            <Route path={base} element={area.shell ? <AreaLayout area={area} /> : <Outlet />}>
              {area.pages.map((page) => {
                const element = pageElements[page.path] ?? <PagePlaceholder page={page} />;
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
