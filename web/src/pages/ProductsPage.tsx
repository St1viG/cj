import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useApi } from "../api/ApiProvider";
import { useMe } from "../auth/AuthProvider";
import { Role } from "@gen/kondukter/api/v1/types_pb";
import { keys } from "../api/keys";
import { SiteNav } from "../components/SiteNav";
import { EmptyState, ErrorState } from "../components/State";
import { formatGiB, formatMiB } from "../lib/format";
import { demoProducts, formatPrice, type PriceUnit, type Product, productFromOffer, unitSuffix } from "../lib/products";

type Kind = "all" | "gpu" | "cpu";
type Sort = "price-asc" | "price-desc" | "vcpu" | "ram";
type View = "table" | "cards";

const sorters: Record<Sort, (a: Product, b: Product) => number> = {
  "price-asc": (a, b) => (a.priceMinor ?? Number.POSITIVE_INFINITY) - (b.priceMinor ?? Number.POSITIVE_INFINITY),
  "price-desc": (a, b) => (b.priceMinor ?? -1) - (a.priceMinor ?? -1),
  vcpu: (a, b) => b.vcpu - a.vcpu,
  ram: (a, b) => b.ramMib - a.ramMib,
};

/** Where "Launch" goes: log in first, or straight to the create form for a customer on live data. */
function launchTarget(p: Product, role: Role | undefined): { to: string; label: string } | null {
  if (p.availability === "soon") return null;
  if (role === Role.CUSTOMER) return { to: p.offerId ? `/app/instances/new?offer=${encodeURIComponent(p.offerId)}` : "/app/offers", label: "Launch" };
  if (role !== undefined) return null; // partners and operators do not rent
  return { to: "/login", label: "Log in to launch" };
}

function Specs({ p }: { p: Product }) {
  return (
    <span className="m">
      {p.vcpu} vCPU · {formatMiB(p.ramMib)} · {formatGiB(p.diskGib)}
    </span>
  );
}

function ProductName({ p }: { p: Product }) {
  return (
    <span className="col" style={{ gap: 4 }}>
      <span className="m ink-h">{p.name}</span>
      {p.gpu ? (
        <span className="chip gpu" style={{ width: "fit-content" }}>
          {p.gpu.model}
          {p.gpu.vramGiB ? ` · ${p.gpu.vramGiB} GB` : ""} · {p.gpu.count}×
        </span>
      ) : (
        <span className="mut2 m">CPU only</span>
      )}
    </span>
  );
}

function Price({ p, unit }: { p: Product; unit: PriceUnit }) {
  if (p.priceMinor === undefined) return <span className="mut2 m">coming soon</span>;
  return (
    <span className="m ink-h">
      {formatPrice(p.priceMinor, p.currency, unit)}
      <span className="mut2">{unitSuffix[unit]}</span>
    </span>
  );
}

function Action({ p, role }: { p: Product; role: Role | undefined }) {
  const target = launchTarget(p, role);
  if (p.availability === "soon") return <span className="chip s-pend">soon</span>;
  if (!target) return <span className="mut2 m">customers only</span>;
  return (
    <Link className="btn sm p" to={target.to}>
      {target.label}
    </Link>
  );
}

/**
 * `/products`: the catalog for everyone. A visitor sees the demo catalog; a
 * signed-in customer sees the live offers. Filters, sort, price unit and a
 * table or card view, in the manner of a GPU price-comparison list.
 */
export function ProductsPage() {
  const auth = useMe();
  const { catalog } = useApi();
  const role = auth.status === "signedIn" ? auth.me.role : undefined;
  const live = useQuery({
    queryKey: keys.catalog.offers({}),
    queryFn: () => catalog.listOffers({}),
    enabled: role === Role.CUSTOMER,
  });
  const isLive = role === Role.CUSTOMER && live.data !== undefined;
  const products = useMemo<readonly Product[]>(() => (isLive ? (live.data?.offers ?? []).map(productFromOffer) : demoProducts), [isLive, live.data]);

  const [kind, setKind] = useState<Kind>("all");
  const [models, setModels] = useState<ReadonlySet<string>>(new Set());
  const [archs, setArchs] = useState<ReadonlySet<string>>(new Set());
  const [availableOnly, setAvailableOnly] = useState(false);
  const [maxPrice, setMaxPrice] = useState<number | null>(null);
  const [unit, setUnit] = useState<PriceUnit>("hour");
  const [sort, setSort] = useState<Sort>("price-asc");
  const [view, setView] = useState<View>("table");

  const priceCeiling = Math.max(1, ...products.map((p) => p.priceMinor ?? 0));
  const ceiling = maxPrice ?? priceCeiling;
  const currency = products[0]?.currency ?? "EUR";
  const modelList = useMemo(() => [...new Set(products.flatMap((p) => (p.gpu ? [p.gpu.model] : [])))].sort(), [products]);
  const archList = useMemo(() => [...new Set(products.flatMap((p) => p.archs))].sort(), [products]);

  const shown = useMemo(
    () =>
      products
        .filter((p) => kind === "all" || (kind === "gpu") === Boolean(p.gpu))
        .filter((p) => models.size === 0 || (p.gpu !== undefined && models.has(p.gpu.model)))
        .filter((p) => archs.size === 0 || p.archs.some((a) => archs.has(a)))
        .filter((p) => !availableOnly || p.availability === "available")
        .filter((p) => p.priceMinor === undefined || p.priceMinor <= ceiling)
        .sort(sorters[sort]),
    [products, kind, models, archs, availableOnly, ceiling, sort],
  );

  const toggle = (set: ReadonlySet<string>, v: string, put: (s: ReadonlySet<string>) => void) => {
    const next = new Set(set);
    if (next.has(v)) next.delete(v);
    else next.add(v);
    put(next);
  };
  const clear = () => {
    setKind("all");
    setModels(new Set());
    setArchs(new Set());
    setAvailableOnly(false);
    setMaxPrice(null);
  };
  const filtered = kind !== "all" || models.size > 0 || archs.size > 0 || availableOnly || maxPrice !== null;

  return (
    <div className="shell">
      <SiteNav />
      <main className="pub">
        <header className="pagehead glow">
          <span className="lbl">Products</span>
          <h1>Compute you can log in to.</h1>
          <p className="lead">
            Dedicated virtual machines from partner hardware: CPU shapes and whole GPUs, each with a curated image and direct SSH. Compare shapes, prices and
            architectures, then launch.
          </p>
          <div className="row">
            <span className="chip s-run">
              <span className="led" aria-hidden="true" />
              dedicated
            </span>
            <span className="chip s-run">
              <span className="led" aria-hidden="true" />
              ssh-ready
            </span>
            <span className="chip s-run">
              <span className="led" aria-hidden="true" />
              whole gpu
            </span>
            <span className="chip s-run">
              <span className="led" aria-hidden="true" />
              per-minute estimates
            </span>
          </div>
        </header>

        {role === Role.CUSTOMER && live.isError && <ErrorState error={live.error} onRetry={() => void live.refetch()} />}
        <p className="note">
          {isLive
            ? "Live catalog: these are the offers you can launch right now."
            : "Log in to see live availability and launch an instance."}
        </p>

        <div className="tabs" role="tablist" aria-label="Product type">
          {(["all", "gpu", "cpu"] as const).map((k) => (
            <button key={k} type="button" role="tab" aria-selected={kind === k} className="g" onClick={() => setKind(k)}>
              {k === "all" ? "View all" : k === "gpu" ? "GPU" : "CPU"}
            </button>
          ))}
        </div>

        <div className="market">
          <aside className="filters card" aria-label="Filters">
            <div className="row">
              <span className="lbl">Filters</span>
              <span className="sp" />
              <button type="button" className="sm g" disabled={!filtered} onClick={clear}>
                Clear all
              </button>
            </div>
            <label className="chk">
              <input type="checkbox" checked={availableOnly} onChange={(e) => setAvailableOnly(e.currentTarget.checked)} /> Available now only
            </label>
            {modelList.length > 0 && (
              <fieldset>
                <legend>GPU model</legend>
                {modelList.map((m) => (
                  <label key={m} className="chk">
                    <input type="checkbox" checked={models.has(m)} onChange={() => toggle(models, m, setModels)} />
                    <span>{m}</span>
                    <span className="mut2 m sp" style={{ textAlign: "right" }}>
                      {products.filter((p) => p.gpu?.model === m).length}
                    </span>
                  </label>
                ))}
              </fieldset>
            )}
            <fieldset>
              <legend>Architecture</legend>
              {archList.map((a) => (
                <label key={a} className="chk">
                  <input type="checkbox" checked={archs.has(a)} onChange={() => toggle(archs, a, setArchs)} />
                  <span className="m">{a}</span>
                </label>
              ))}
            </fieldset>
            <div className="fld">
              <label htmlFor="max-price">
                Max price / hour: <span className="m ink-h">{formatPrice(ceiling, currency)}</span>
              </label>
              <input id="max-price" type="range" min={0} max={priceCeiling} step={1} value={ceiling} onChange={(e) => setMaxPrice(Number(e.currentTarget.value))} />
            </div>
          </aside>

          <section className="results">
            <div className="toolbar">
              <span className="m mut" aria-live="polite">
                {shown.length} of {products.length} configurations
              </span>
              <span className="sp" />
              <div className="seg" role="group" aria-label="Price per">
                {(["hour", "day", "month"] as const).map((u) => (
                  <button key={u} type="button" aria-pressed={unit === u} onClick={() => setUnit(u)}>
                    {u}
                  </button>
                ))}
              </div>
              <select aria-label="Sort" value={sort} onChange={(e) => setSort(e.currentTarget.value as Sort)} style={{ width: "auto" }}>
                <option value="price-asc">Price: low to high</option>
                <option value="price-desc">Price: high to low</option>
                <option value="vcpu">Most vCPU</option>
                <option value="ram">Most RAM</option>
              </select>
              <div className="seg" role="group" aria-label="View">
                <button type="button" aria-pressed={view === "table"} onClick={() => setView("table")}>
                  Table
                </button>
                <button type="button" aria-pressed={view === "cards"} onClick={() => setView("cards")}>
                  Cards
                </button>
              </div>
            </div>

            {shown.length === 0 ? (
              <EmptyState>No configuration matches these filters.</EmptyState>
            ) : view === "table" ? (
              <div className="tw">
                <table className="tbl">
                  <caption>{isLive ? "Live offers" : "Catalog"}</caption>
                  <thead>
                    <tr>
                      <th scope="col">Product</th>
                      <th scope="col">Hosted by</th>
                      <th scope="col">Size</th>
                      <th scope="col">Architecture</th>
                      <th scope="col" className="num">
                        Price
                      </th>
                      <th scope="col" className="act" />
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((p) => (
                      <tr key={p.id}>
                        <td>
                          <ProductName p={p} />
                        </td>
                        <td className="mut wrap">{p.host ? `${p.host} · ${p.region}` : p.region}</td>
                        <td>
                          <Specs p={p} />
                        </td>
                        <td className="m">{p.archs.join(" · ") || "—"}</td>
                        <td className="num">
                          <Price p={p} unit={unit} />
                        </td>
                        <td className="act">
                          <Action p={p} role={role} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="g3">
                {shown.map((p) => (
                  <article key={p.id} className="card reg col pcard">
                    <div className="row">
                      <span className={`chip ${p.gpu ? "gpu" : ""}`}>{p.gpu ? "gpu" : "cpu"}</span>
                      <span className="sp" />
                      {p.availability === "soon" && <span className="chip s-pend">soon</span>}
                    </div>
                    <ProductName p={p} />
                    <Specs p={p} />
                    <div className="row" style={{ gap: 6 }}>
                      {p.archs.map((a) => (
                        <span key={a} className="pill">
                          {a}
                        </span>
                      ))}
                      <span className="pill">{p.region}</span>
                      <span className="pill">{p.capacity}</span>
                    </div>
                    <div className="pcard-price">
                      <Price p={p} unit={unit} />
                      {p.priceMinor !== undefined && unit === "hour" && (
                        <span className="mut2 m">
                          {formatPrice(p.priceMinor, p.currency, "day")}/day · {formatPrice(p.priceMinor, p.currency, "month")}/mo
                        </span>
                      )}
                    </div>
                    <Action p={p} role={role} />
                  </article>
                ))}
              </div>
            )}
            <p className="note">
              Curated Ubuntu 24.04 images; GPU shapes ship with the NVIDIA driver and CUDA pre-installed. One tenant per GPU, direct SSH to the node, and capacity
              is dedicated, never shared.
            </p>
          </section>
        </div>
      </main>
    </div>
  );
}
