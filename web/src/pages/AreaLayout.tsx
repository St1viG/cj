import { NavLink, Outlet, useNavigate } from "react-router";
import { useCurrentUser, useMe } from "../auth/AuthProvider";
import { roleLabel } from "../auth/roles";
import type { AreaDef } from "../routes";

/**
 * The frame around an area's pages: its navigation, who is signed in, and
 * logout. Plain markup; the owner's design replaces it.
 */
export function AreaLayout({ area }: { area: AreaDef }) {
  const me = useCurrentUser();
  const auth = useMe();
  const navigate = useNavigate();

  return (
    <>
      <header>
        <strong>Kondukter</strong> <span>{area.title}</span>
        <nav aria-label={area.title}>
          <ul>
            {area.pages
              .filter((p) => p.nav)
              .map((p) => (
                <li key={p.path}>
                  <NavLink to={p.path}>{p.title}</NavLink>
                </li>
              ))}
          </ul>
        </nav>
        <span>
          {me.user?.email} ({roleLabel(me.role)}, {me.org?.name})
        </span>{" "}
        <button
          type="button"
          onClick={() => {
            if (auth.status !== "signedIn") return;
            void auth.logout().then(() => navigate("/login", { replace: true }));
          }}
        >
          Log out
        </button>
      </header>
      <main>
        <Outlet />
      </main>
    </>
  );
}
