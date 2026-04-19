/**
 * React integration for Antic-PT v0.2.
 *
 * useAnticipation is a thin wrapper around AnticipationResolver that manages
 * lifecycle and exposes a reactive status/data/meta surface.
 *
 * NOTE: This file is intentionally excluded from the zero-dependency IIFE/CJS
 * bundles. It is provided as an optional peer-dependency wrapper for React codebases.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { AnticipationResolver, ResolverMeta, ResolverOptions, ResolverStatus, PatchOp } from './resolver';

export interface UseAnticipationResult {
  /** Current speculative (or confirmed) resource data. */
  data: Record<string, any> | null;
  /** Fields currently withheld pending Formal Track delivery. */
  deferredFields: string[];
  /** Metadata from the most recent speculative response. */
  meta: ResolverMeta | null;
  /** Current lifecycle status of the resolution. */
  status: ResolverStatus;
  /** Manually trigger a re-fetch (e.g. after retryable abort). */
  refetch: () => void;
}

/**
 * useAnticipation fetches a resource speculatively through Spec-Link and
 * reconciles the result as signals arrive.
 *
 * @example
 * function UserCard({ id }: { id: string }) {
 *   const { data, meta, status, deferredFields } = useAnticipation(`/spec/user/${id}`, {
 *     baseUrl: 'http://localhost:4000',
 *   });
 *
 *   if (!data) return <LoadingSpinner />;
 *   return (
 *     <div>
 *       <Name value={data.name} syncing={status === 'speculative'} />
 *       {deferredFields.includes('kpi_score')
 *         ? <Placeholder />
 *         : <KPIBadge value={data.kpi_score} />
 *       }
 *     </div>
 *   );
 * }
 */
export function useAnticipation(
  path: string,
  options?: ResolverOptions
): UseAnticipationResult {
  const [data, setData] = useState<Record<string, any> | null>(null);
  const [meta, setMeta] = useState<ResolverMeta | null>(null);
  const [status, setStatus] = useState<ResolverStatus>('idle');
  const [deferredFields, setDeferredFields] = useState<string[]>([]);

  // Keep a stable ref to the current data so patch operations can close over it.
  const dataRef = useRef<Record<string, any> | null>(null);

  const resolverRef = useRef<AnticipationResolver | null>(null);

  const doFetch = useCallback(() => {
    const resolver = new AnticipationResolver(path, options);
    resolverRef.current = resolver;

    resolver
      .on('speculative', (incoming, m) => {
        dataRef.current = incoming;
        setData(incoming);
        setMeta(m);
        setDeferredFields(m.deferredFields);
        setStatus('speculative');
      })
      .on('patch', (ops: PatchOp[]) => {
        setStatus('patching');
        if (dataRef.current) {
          const patched = AnticipationResolver.applyPatch(dataRef.current, ops);
          dataRef.current = patched;
          setData({ ...patched });
        }
      })
      .on('fill', (fields) => {
        setStatus('filling');
        if (dataRef.current) {
          const merged = { ...dataRef.current, ...fields };
          dataRef.current = merged;
          setData(merged);
        }
        setDeferredFields([]);
      })
      .on('confirm', () => {
        setStatus('confirmed');
      })
      .on('replace', (incoming) => {
        dataRef.current = incoming;
        setData(incoming);
        setDeferredFields([]);
        setStatus('confirmed');
      })
      .on('abort', (reason, retryable) => {
        setStatus('error');
        console.warn(`[useAnticipation] abort: ${reason} (retryable=${retryable})`);
      })
      .on('speculationAbandoned', (m) => {
        console.warn(`[useAnticipation] speculation abandoned: ${m.reason}`);
      });

    resolver.fetch();
  }, [path, options?.baseUrl, options?.maxWindow, options?.onTimeout]);

  useEffect(() => {
    doFetch();
    // No cleanup needed — AnticipationResolver cleans up automatically on finalize.
  }, [doFetch]);

  return { data, meta, status, deferredFields, refetch: doFetch };
}
