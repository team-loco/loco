import type { DescMessage, DescMethodUnary, MessageInitShape } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery, type UseQueryOptions } from "@connectrpc/connect-query";
import { useEffect } from "react";

const COUNT_REFRESH_MS = 60_000;

/**
 * Loads the page the last of tokens names, or the first page when there are none. The total comes
 * from its own one-row request per set of filters, which stays cached while the list is shown, so
 * paging never counts again; a list that polls refreshes its total once a minute, not on every poll.
 * countInput, input by default, is the request the total and any counts beside it answer.
 * onInvalidTokens, when given, runs while the API rejects tokens, such as an old link's, to go back
 * to the first page; until then the rejection is no error.
 */
export function useTokenPage<I extends DescMessage, O extends DescMessage>(
	schema: DescMethodUnary<I, O>,
	input: MessageInitShape<I>,
	tokens: readonly string[],
	options?: UseQueryOptions<O>,
	countInput: MessageInitShape<I> = input,
	onInvalidTokens?: () => void,
) {
	const query = useQuery(schema, { ...input, pageToken: tokens.at(-1) ?? "" }, options);
	const invalid =
		onInvalidTokens !== undefined &&
		tokens.length > 0 &&
		query.error instanceof ConnectError &&
		query.error.code === Code.InvalidArgument;
	// The tokens live in the URL, which this syncs to the first page.
	useEffect(() => {
		if (invalid) onInvalidTokens();
	}, [invalid, onInvalidTokens]);
	const counted = useQuery(
		schema,
		{ ...countInput, pageSize: 1, pageToken: "", includeTotalCount: true },
		{
			enabled: options?.enabled ?? true,
			...(options?.placeholderData === undefined ? {} : { placeholderData: options.placeholderData }),
			staleTime: Infinity,
			refetchInterval: options?.refetchInterval ? COUNT_REFRESH_MS : false,
		},
	);
	return {
		query,
		error: invalid ? null : query.error,
		/** Whether the list waits for onInvalidTokens to take it to the first page. */
		resetting: invalid,
		nextPageToken: (query.data as { nextPageToken: string } | undefined)?.nextPageToken ?? "",
		total: (counted.data as { totalCount: number } | undefined)?.totalCount,
		/** The response that carried the total, for lists that return more counts beside it. */
		counts: counted.data,
	};
}
