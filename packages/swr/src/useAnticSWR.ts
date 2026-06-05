import useSWR, { SWRConfiguration, SWRResponse } from "swr";
import {
  AnticipationResolver,
  ResolverMeta,
  ResolverOptions,
  ResolverStatus,
} from "@antic-pt/resolver";
import { useState, useRef, useEffect } from "react";

export type UseAnticSWRResult<TData = any> = SWRResponse<TData, any> & {
  anticStatus: ResolverStatus;
  anticMeta: ResolverMeta | null;
  deferredFields: string[];
};

export function useAnticSWR<TData = any>(
  key: string | null,
  options?: {
    anticOptions?: ResolverOptions;
    swrOptions?: SWRConfiguration<TData, any>;
  },
): UseAnticSWRResult<TData> {
  const [anticMeta, setAnticMeta] = useState<ResolverMeta | null>(null);
  const [anticStatus, setAnticStatus] = useState<ResolverStatus>("idle");

  const fetcher = (url: string): Promise<TData> => {
    return new Promise((resolve, reject) => {
      if (typeof window === "undefined") {
        fetch(url)
          .then((res) => res.json())
          .then(resolve)
          .catch(reject);
        return;
      }

      const resolver = new AnticipationResolver(url, options?.anticOptions);

      resolver.on("speculative", (data: any, meta: ResolverMeta) => {
        setAnticMeta(meta);
        setAnticStatus("speculative");
        resolve(data); // Resolves SWR fetcher
      });

      resolver.on("patch", (ops: any) => {
        setAnticStatus("patching");
        swr.mutate(
          (old: any) => AnticipationResolver.applyPatch(old, ops) as any,
          false,
        );
      });

      resolver.on("fill", (fields: any) => {
        setAnticStatus("filling");
        swr.mutate((old: any) => ({ ...old, ...fields }) as any, false);
      });

      resolver.on("confirm", () => setAnticStatus("confirmed"));
      resolver.on("replace", (data: any) => {
        setAnticStatus("confirmed");
        swr.mutate(data, false);
      });

      resolver.on("abort", (reason: string, retryable: boolean) => {
        setAnticStatus("error");
        if (!retryable) reject(new Error(reason));
      });

      resolver.fetch();
    });
  };

  const swr = useSWR<TData>(key, fetcher, options?.swrOptions);

  return {
    ...swr,
    anticStatus,
    anticMeta,
    deferredFields: anticMeta?.deferredFields ?? [],
  };
}
