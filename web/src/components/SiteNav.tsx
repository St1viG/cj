import { useIsFetching } from "@tanstack/react-query";
import { Role } from "@gen/kondukter/api/v1/types_pb";
import { Link, NavLink, useLocation } from "react-router";
import { useMe } from "../auth/AuthProvider";
import { homePath, roleLabel } from "../auth/roles";
import { eur, useFakeBilling } from "../lib/fakeBilling";
import { useTheme } from "../lib/theme";
import { Logo } from "./Logo";

/**
 * The one navigation bar for the whole site: the public pages (Home,
 * Products) for everyone, the person's own dashboard when signed in, and
 * Log in or the account controls on the right. Dashboard pages add their own
 * tab row under it (AreaLayout).
 */
export function SiteNav() {
  const auth = useMe();
  const { pathname } = useLocation();
  const [theme, toggleTheme] = useTheme();
  const fetching = useIsFetching() > 0;
  const bill = useFakeBilling();
  const signedIn = auth.status === "signedIn";
  const dashboard = signedIn ? homePath(auth.me.role) : "";
  const inApp = /^\/(app|partner|admin)(\/|$)/.test(pathname);

  return (
    <header className="sitenav">
      <Link to="/" aria-label="Warden home" className="brand">
        <Logo />
      </Link>
      <nav aria-label="Main" className="links">
        <NavLink to="/" end>
          Home
        </NavLink>
        <NavLink to="/products">Products</NavLink>
        {signedIn && (
          <Link to={dashboard} aria-current={inApp ? "page" : undefined}>
            Dashboard
          </Link>
        )}
      </nav>
      <span className="sp" />
      {signedIn && (
        <span className={`live s-${fetching ? "prov" : "run"}`} title="Pages refresh every few seconds">
          <span className="led" aria-hidden="true" />
          <span className="cw" data-w={fetching ? "SYNC" : "LIVE"} />
        </span>
      )}
      <button type="button" className="sm g themebtn" aria-label="Switch colour theme" onClick={toggleTheme}>
        <span className="cw" data-w={theme === "dark" ? "light" : "dark"} />
      </button>
      {signedIn && auth.me.role === Role.CUSTOMER && (
        <Link to="/app/billing" className="balance" title="Balance">
          <span className="cw lbl" data-w="Balance" />
          <span className="m ink-h">{eur(bill.balance)}</span>
        </Link>
      )}
      {signedIn ? (
        <span className="acct">
          <span className="who-email m" title={auth.me.user?.email}>
            {auth.me.user?.email}
          </span>
          <span className="chip role">{roleLabel(auth.me.role)}</span>
          <button
            type="button"
            className="sm"
            onClick={() => {
              // The route guard sends the signed-out user to /login.
              void auth.logout();
            }}
          >
            Log out
          </button>
        </span>
      ) : (
        <Link className="btn p" to="/login">
          Log in
        </Link>
      )}
    </header>
  );
}
