import type { DescMessage, DescMethodUnary, MessageInitShape } from "@bufbuild/protobuf";
import { useQuery, type UseQueryOptions } from "@connectrpc/connect-query";

const COUNT_REFRESH_MS = 60_000;

/**
 * Loads the page the last of tokens names, or the first page when there are none. The total comes
 * from its own one-row request per set of filters, which stays cached while the list is shown, so
 * paging never counts again; a list that polls refreshes its total once a minute, not on every poll.
 */
export function useTokenPage<I extends DescMessage, O extends DescMessage>(
	schema: DescMethodUnary<I, O>,
	input: MessageInitShape<I>,
	tokens: readonly string[],
	options?: UseQueryOptions<O>,
) {
	const query = useQuery(schema, { ...input, pageToken: tokens.at(-1) ?? "" }, options);
	const counted = useQuery(
		schema,
		{ ...input, pageSize: 1, pageToken: "", includeTotalCount: true },
		{
			enabled: options?.enabled ?? true,
			...(options?.placeholderData === undefined ? {} : { placeholderData: options.placeholderData }),
			staleTime: Infinity,
			refetchInterval: options?.refetchInterval ? COUNT_REFRESH_MS : false,
		},
	);
	return {
		query,
		nextPageToken: (query.data as { nextPageToken: string } | undefined)?.nextPageToken ?? "",
		total: (counted.data as { totalCount: number } | undefined)?.totalCount,
		/** The response that carried the total, for lists that return more counts beside it. */
		counts: counted.data,
	};
}
