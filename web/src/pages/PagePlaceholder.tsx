import type { PageDef } from "../routes";

/**
 * Stands in for a page until its roadmap item lands (P5.2 to P5.5). It shows
 * the route and the RPCs from docs/WEB_PAGES.md, so the skeleton can be
 * clicked through end to end.
 */
export function PagePlaceholder({ page }: { page: PageDef }) {
  return (
    <section>
      <h1>{page.title}</h1>
      <p>
        <code>{page.path}</code>: the contents arrive with roadmap {page.item}.
      </p>
      <p>
        Backed by:{" "}
        {page.rpcs.map((rpc, i) => (
          <span key={rpc}>
            {i > 0 && ", "}
            <code>{rpc}</code>
          </span>
        ))}
      </p>
    </section>
  );
}
