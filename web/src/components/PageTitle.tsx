import type { ReactNode } from "react";
import { matchPath, useLocation } from "react-router";
import { pageBlurbs } from "../lib/pageBlurbs";
import { areaOf } from "../auth/roles";
import { areas } from "../routes";

/**
 * The page's `<h1>` in the design's hero treatment: a small area label above,
 * the serif title, and a one-line description of the page below, all on a
 * soft glow. The label and description are drawn from the route table.
 */
export function PageTitle({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  const area = areas.find((a) => a.area === areaOf(pathname));
  const page = area?.pages.find((p) => matchPath({ path: p.path, end: true }, pathname));
  const blurb = page ? pageBlurbs[page.path] : undefined;
  return (
    <header className="pagehead glow">
      {area && <span className="lbl cw" data-w={`${area.title}${page ? ` · ${page.title}` : ""}`} />}
      <h1>{children}</h1>
      {blurb && <p className="lead">{blurb}</p>}
    </header>
  );
}
