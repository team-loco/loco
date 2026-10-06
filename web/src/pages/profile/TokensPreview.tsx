import { useQuery } from "@connectrpc/connect-query";
import { ArrowRight, KeyRound } from "lucide-react";
import { useNavigate } from "react-router";

import { listTokens } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType, type Token } from "@gen/loco/token/v1/token_pb";

import { Button } from "@/components/design/Button";
import { Section, SectionFooter } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { getErrorMessage } from "@/lib/error-handler";
import { tsMs } from "@/lib/time";

const PREVIEW_COUNT = 3;

const expiryFormat = new Intl.DateTimeFormat();

function expiryLabel(token: Token): string {
	if (token.expiresAt === undefined) {
		return "Never expires";
	}
	return `Expires ${expiryFormat.format(tsMs(token.expiresAt))}`;
}

export function TokensPreview({ userId }: { userId: string }) {
	const navigate = useNavigate();
	const { data, isLoading, error } = useQuery(listTokens, { entityType: EntityType.USER, entityId: userId });
	const tokens = data?.tokens ?? [];
	const remaining = tokens.length - PREVIEW_COUNT;

	const renderBody = () => {
		if (isLoading) {
			return (
				<div className="flex flex-col">
					{[0, 1].map((i) => (
						<div key={i} className="flex items-center gap-3 border-b border-line px-5 py-3 last:border-b-0">
							<Skeleton className="size-4" />
							<Skeleton className="h-3.5 w-36" />
							<div className="flex-1" />
							<Skeleton className="h-3 w-24" />
						</div>
					))}
				</div>
			);
		}
		if (error) {
			const message = getErrorMessage(error, "Failed to load tokens");
			return <p className="m-0 px-5 py-4 text-bad-fg">{message}</p>;
		}
		if (tokens.length === 0) {
			return <p className="m-0 px-5 py-4 text-fg3">No tokens yet. Create one on the tokens page.</p>;
		}
		return tokens.slice(0, PREVIEW_COUNT).map((token) => {
			const expiry = expiryLabel(token);
			return (
				<div
					key={`${token.name}-${token.entityType.toString()}-${token.entityId}`}
					className="flex items-center gap-2.5 border-b border-line px-5 py-3 last:border-b-0"
				>
					<KeyRound className="size-4 text-fg3" />
					<span className="min-w-0 flex-1 truncate font-medium">{token.name}</span>
					<span className="text-[12.5px] text-fg3">{expiry}</span>
				</div>
			);
		});
	};

	const body = renderBody();

	return (
		<Section
			title="Tokens"
			actions={
				<Button
					variant="outline"
					size="sm"
					onClick={() => {
						void navigate("/tokens?owner=personal");
					}}
				>
					Manage tokens
					<ArrowRight />
				</Button>
			}
		>
			{body}
			{remaining > 0 && (
				<SectionFooter className="border-t border-line">
					+{remaining} more token{remaining === 1 ? "" : "s"}
				</SectionFooter>
			)}
		</Section>
	);
}
