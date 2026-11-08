import { useEffect, useState } from "react";

export type Theme = "dark" | "light";
const KEY = "kdk-theme";

/** Dark is the primary theme; the choice is a per-browser convenience, so storage failures are ignored. */
export function storedTheme(): Theme {
  try {
    return globalThis.localStorage?.getItem(KEY) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
}

export function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(storedTheme);
  useEffect(() => applyTheme(theme), [theme]);
  return [
    theme,
    () =>
      setTheme((t) => {
        const next = t === "dark" ? "light" : "dark";
        try {
          globalThis.localStorage?.setItem(KEY, next);
        } catch {
          /* private window or blocked storage: the choice just does not persist */
        }
        return next;
      }),
  ];
}
