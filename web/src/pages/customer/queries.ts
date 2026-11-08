import type { CapacityClass } from "@gen/kondukter/api/v1/types_pb";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";

/**
 * The filters of the offers page. They are part of the query key, which
 * TanStack Query hashes as JSON, so they hold plain numbers; the request
 * needs `minRamMib` as a bigint and gets it in `useOffers`.
 */
export interface OffersFilter {
  region?: string;
  acceleratorKind?: string;
  acceleratorModel?: string;
  arch?: string;
  capacityClass?: CapacityClass;
  minVcpu?: number;
  minRamMib?: number;
}

/** What the API returns per list page; the pages ask for this many and say so when there are more. */
export const INSTANCE_PAGE_SIZE = 100;

/** Offers for sale that match `filter` (empty: all of them). */
export function useOffers(filter: OffersFilter = {}) {
  const { catalog } = useApi();
  return useQuery({
    queryKey: keys.catalog.offers(filter),
    queryFn: () => catalog.listOffers({ ...filter, minRamMib: filter.minRamMib === undefined ? undefined : BigInt(filter.minRamMib) }),
    // Changing a filter keeps the rows on screen until the new ones arrive.
    placeholderData: keepPreviousData,
  });
}

/** Images that can run on `offerId` for `arch`; waits until both are known. */
export function useImages(offerId: string, arch: string) {
  const { catalog } = useApi();
  return useQuery({
    queryKey: keys.catalog.images(offerId, arch),
    queryFn: () => catalog.listImages({ offerId, arch }),
    enabled: offerId !== "" && arch !== "",
  });
}

export function useSshKeys() {
  const { account } = useApi();
  return useQuery({ queryKey: keys.account.sshKeys, queryFn: () => account.listSSHKeys({}) });
}

export function useApiKeys() {
  const { account } = useApi();
  return useQuery({ queryKey: keys.account.apiKeys, queryFn: () => account.listAPIKeys({}) });
}

/** The caller's newest instances; `includeTerminal` adds ended, failed and lost ones. */
export function useInstances(includeTerminal: boolean) {
  const { instances } = useApi();
  return useQuery({
    queryKey: keys.instances.list({ includeTerminal }),
    queryFn: () => instances.listInstances({ includeTerminal, pageSize: INSTANCE_PAGE_SIZE }),
  });
}

/** One instance with its timeline. */
export function useInstance(id: string) {
  const { instances } = useApi();
  return useQuery({ queryKey: keys.instances.detail(id), queryFn: () => instances.getInstance({ id }) });
}
