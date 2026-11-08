import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { useMe } from "../auth/AuthProvider";
import { landingPath } from "../auth/roles";
import { Field } from "../components/Field";
import { Form } from "../components/Form";
import { Logo } from "../components/Logo";
import { SiteNav } from "../components/SiteNav";
import { ErrorState, LoadingState } from "../components/State";

function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="shell">
      <SiteNav />
      <div className="login">{children}</div>
    </div>
  );
}

/** `/login`: email and password against the seeded accounts (ADR-0021). */
export function LoginPage() {
  const auth = useMe();
  const location = useLocation();
  const from: unknown = (location.state as { from?: unknown } | null)?.from;

  switch (auth.status) {
    case "loading":
      return (
        <Frame>
          <div className="panel">
            <LoadingState />
          </div>
        </Frame>
      );
    case "error":
      return (
        <Frame>
          <div className="panel">
            <ErrorState error={auth.error} onRetry={auth.retry} />
          </div>
        </Frame>
      );
    case "signedIn":
      return <Navigate to={landingPath(auth.me.role, from)} replace />;
    case "signedOut": {
      const { login } = auth;
      return (
        <Frame>
          <section className="panel card reg glow">
            <Logo size={30} />
            <div className="col">
              <span className="lbl">Compute, on demand</span>
              <h1>Log in</h1>
              <p className="mut">Precision infrastructure, calm by default.</p>
            </div>
            <Form
              submitLabel="Log in"
              onSubmit={async (data) => {
                // On success the principal lands in the query cache and this
                // page redirects on its next render.
                await login(String(data.get("email") ?? ""), String(data.get("password") ?? ""));
              }}
            >
              <Field label="Email" name="email" type="email" autoComplete="username" required autoFocus />
              <Field label="Password" name="password" type="password" autoComplete="current-password" required />
            </Form>
          </section>
        </Frame>
      );
    }
  }
}
