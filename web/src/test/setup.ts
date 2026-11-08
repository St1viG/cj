import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library only unmounts between tests on its own when the test
// runner exposes globals; here it is explicit.
afterEach(() => cleanup());

// `findBy*` and `waitFor` give up after 1 s by default. They return as soon as
// the condition holds, so a longer limit costs nothing when the machine is
// fast and keeps the suite from failing when it is busy (a shared CI runner,
// other builds running): rendering the whole route tree takes a while then.
configure({ asyncUtilTimeout: 8000 });
