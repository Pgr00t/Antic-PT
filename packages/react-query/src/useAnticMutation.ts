import { useMutation, useQueryClient, UseMutationOptions } from "@tanstack/react-query";
import { AnticipationResolver, ResolverOptions, ResolverStatus } from "@antic-pt/resolver";
import { useState } from "react";

export function useAnticMutation<TData = any, TVariables = void>(
  path: string,
  method: string = "POST",
  options?: {
    anticOptions?: ResolverOptions;
    mutationOptions?: Omit<UseMutationOptions<TData, Error, TVariables, any>, "mutationFn">;
    queryKeyToInvalidate?: unknown[];
  }
) {
  const queryClient = useQueryClient();
  const [anticStatus, setAnticStatus] = useState<ResolverStatus>("idle");

  const mutationFn = async (variables: TVariables): Promise<TData> => {
    return new Promise((resolve, reject) => {
      const resolver = new AnticipationResolver(path, options?.anticOptions);

      resolver.on("speculative", (data: any) => {
        setAnticStatus("speculative");
        resolve(data);
      });

      resolver.on("confirm", () => {
        setAnticStatus("confirmed");
        if (options?.queryKeyToInvalidate) {
          queryClient.invalidateQueries({ queryKey: options.queryKeyToInvalidate });
        }
      });

      resolver.on("replace", (data: any) => {
        setAnticStatus("confirmed");
        if (options?.queryKeyToInvalidate) {
          queryClient.setQueryData(options.queryKeyToInvalidate, data);
        }
      });

      resolver.on("abort", (reason: string) => {
        setAnticStatus("error");
        reject(new Error(reason));
      });

      resolver.mutate(method, variables).catch(reject);
    });
  };

  const mutation = useMutation({
    mutationFn,
    ...options?.mutationOptions,
  });

  return {
    ...mutation,
    anticStatus,
  };
}
