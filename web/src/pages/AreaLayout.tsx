import { NavLink, Outlet } from "react-router";
import { SiteNav } from "../components/SiteNav";
import type { AreaDef } from "../routes";

/**
 * The frame around a dashboard area: the site's navigation bar, then this
 * area's own tabs, then the page.
 */
export function AreaLayout({ area }: { area: AreaDef }) {
  const navPages = area.pages.filter((p) => p.nav);
  return (
    <div className="shell">
      <SiteNav />
      <nav className="subnav" aria-label={area.title}>
        <span className="lbl" aria-hidden="true">
          {area.title}
        </span>
        {navPages.map((p, i) => (
          <NavLink key={p.path} to={p.path}>
            <span className="ix" aria-hidden="true">
              {String(i + 1).padStart(2, "0")}
            </span>
            {p.title}
          </NavLink>
        ))}
      </nav>
      <main className="page">
        <Outlet />
      </main>
    </div>
  );
}
