import { Navigate, useLocation } from "react-router";
import { useMe } from "../auth/AuthProvider";
import { landingPath } from "../auth/roles";
import { Field } from "../components/Field";
import { Form } from "../components/Form";
import { ErrorState, LoadingState } from "../components/State";

/** `/login`: email and password against the seeded accounts (ADR-0021). */
export function LoginPage() {
  const auth = useMe();
  const location = useLocation();
  const from: unknown = (location.state as { from?: unknown } | null)?.from;

  switch (auth.status) {
    case "loading":
      return <LoadingState />;
    case "error":
      return <ErrorState error={auth.error} onRetry={auth.retry} />;
    case "signedIn":
      return <Navigate to={landingPath(auth.me.role, from)} replace />;
    case "signedOut": {
      const { login } = auth;
      return (
        <section>
          <h1>Log in</h1>
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
          {import.meta.env.DEV && (
            <p>
              <small>
                Demo accounts: customer@demo.local, partner@demo.local, operator@demo.local, with the password given to{" "}
                <code>kondukter seed accounts</code>.
              </small>
            </p>
          )}
        </section>
      );
    }
  }
}
