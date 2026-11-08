import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library only unmounts between tests on its own when the test
// runner exposes globals; here it is explicit.
afterEach(() => cleanup());
