import {
  useQuery,
  useQueryClient,
  UseQueryOptions,
  UseQueryResult,
} from "@tanstack/react-query";
import {
  AnticipationResolver,
  ResolverMeta,
  ResolverOptions,
  ResolverStatus,
} from "@antic-pt/resolver";
import { useState, useRef, useEffect } from "react";

export type UseAnticQueryResult<TData = any> = UseQueryResult<TData, any> & {
  anticStatus: ResolverStatus;
  anticMeta: ResolverMeta | null;
  deferredFields: string[];
};

export function useAnticQuery<TData = any>(
  queryKey: unknown[],
  path: string,
  options?: {
    anticOptions?: ResolverOptions;
    queryOptions?: Omit<
      UseQueryOptions<TData, Error, TData, any>,
      "queryKey" | "queryFn"
    >;
  },
): UseAnticQueryResult<TData> {
  const queryClient = useQueryClient();
  const [anticMeta, setAnticMeta] = useState<ResolverMeta | null>(null);
  const [anticStatus, setAnticStatus] = useState<ResolverStatus>("idle");

  // Need to hold refs to avoid stale closures in SSE callbacks
  const queryKeyRef = useRef(queryKey);
  queryKeyRef.current = queryKey;

  const queryFn = ({ signal }: { signal: AbortSignal }): Promise<TData> => {
    return new Promise((resolve, reject) => {
      // Return early if we are purely rendering on the server
      if (typeof window === "undefined") {
        fetch(path)
          .then((res) => res.json())
          .then(resolve)
          .catch(reject);
        return;
      }

      const resolver = new AnticipationResolver(path, options?.anticOptions);

      // Prevent memory leaks if query is cancelled
      signal.addEventListener("abort", () => {
        // Force an abort on the resolver to clear its SSE subscription
        (resolver as any).handleSignal("abort", {
          reason: "query_cancelled",
          retryable: false,
        });
      });

      resolver.on("speculative", (data: any, meta: ResolverMeta) => {
        setAnticMeta(meta);
        setAnticStatus("speculative");
        resolve(data); // Tells React Query to cache and render!
      });

      resolver.on("patch", (ops: any) => {
        setAnticStatus("patching");
        queryClient.setQueryData(queryKeyRef.current, (old: any) =>
          AnticipationResolver.applyPatch(old, ops),
        );
      });

      resolver.on("fill", (fields: any) => {
        setAnticStatus("filling");
        queryClient.setQueryData(queryKeyRef.current, (old: any) => ({
          ...old,
          ...fields,
        }));
      });

      resolver.on("confirm", () => setAnticStatus("confirmed"));
      resolver.on("replace", (data: any) => {
        setAnticStatus("confirmed");
        queryClient.setQueryData(queryKeyRef.current, data);
      });

      resolver.on("abort", (reason: string, retryable: boolean) => {
        setAnticStatus("error");
        if (!retryable) reject(new Error(reason));
      });

      resolver.fetch();
    });
  };

  const query = useQuery({
    queryKey,
    queryFn,
    ...options?.queryOptions,
  });

  return {
    ...query,
    anticStatus,
    anticMeta,
    deferredFields: anticMeta?.deferredFields ?? [],
  } as UseAnticQueryResult<TData>;
}
