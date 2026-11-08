import { useQuery } from "@tanstack/react-query";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { ErrorState } from "../../components/State";
import { useParam } from "../../hooks/useParam";
import { shortId } from "../../lib/format";

/** The partner's own sites; every site for an operator. */
export function useSites() {
  const { partner } = useApi();
  return useQuery({ queryKey: keys.fleet.sites, queryFn: () => partner.listSites({}) });
}

/** `?site=<id>`, empty for all sites. */
export const useSiteParam = () => useParam("site");

/** A "Site" select bound to `?site=`. */
export function SiteFilter() {
  const sites = useSites();
  const [site, setSite] = useSiteParam();
  const known = sites.data?.sites.some((s) => s.id === site) ?? true;
  return (
    <div>
      <label>
        Site{" "}
        <select value={site} onChange={(e) => setSite(e.target.value)}>
          <option value="">all sites</option>
          {sites.data?.sites.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({s.region})
            </option>
          ))}
          {site && !known && <option value={site}>unknown site {shortId(site)}</option>}
        </select>
      </label>
      {sites.isError && <ErrorState error={sites.error} onRetry={() => void sites.refetch()} />}
    </div>
  );
}
