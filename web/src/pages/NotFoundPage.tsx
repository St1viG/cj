import { Link } from "react-router";
import { Logo } from "../components/Logo";
import { SiteNav } from "../components/SiteNav";

export function NotFoundPage() {
  return (
    <div className="shell">
      <SiteNav />
      <div className="login">
        <section className="panel card reg">
          <Logo />
          <h1>Page not found</h1>
          <p className="mut">That address leads nowhere in Warden.</p>
          <p>
            <Link to="/">Back to the start</Link>
          </p>
        </section>
      </div>
    </div>
  );
}
