import { useQueryClient } from "@tanstack/react-query";
import { KeyRoundIcon, PlusIcon } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import { listTokens } from "@gen/loco/token/v1/token-TokenService_connectquery";
import type { Token } from "@gen/loco/token/v1/token_pb";
import { createConnectQueryKey } from "@connectrpc/connect-query";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { tsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { CreateTokenDialog } from "./tokens/CreateTokenDialog";
import type { CreateDraft } from "./tokens/CreateTokenDialog";
import { ownerIcon } from "./tokens/icons";
import { heldLevel, matchesQuery, tokenGrants, tokenStatus } from "./tokens/model";
import type { Level, OwnerKey } from "./tokens/model";
import { RevokeDialog } from "./tokens/RevokeDialog";
import { SecretDialog } from "./tokens/SecretDialog";
import type { RevealedSecret } from "./tokens/SecretDialog";
import { TokenList } from "./tokens/TokenList";
import { TokenPanel } from "./tokens/TokenPanel";
import { useTokenData } from "./tokens/useTokenData";

const OWNER_KEYS: OwnerKey[] = ["org", "workspace", "personal"];

function parseOwner(value: string | null): OwnerKey {
	if (value === "org") return "org";
	if (value === "personal") return "personal";
	return "workspace";
}

export function Tokens() {
	const [params, setParams] = useSearchParams();
	const ownerKey = parseOwner(params.get("owner"));
	const data = useTokenData();
	const queryClient = useQueryClient();
	const now = useNow(60_000).getTime();
	const [q, setQ] = useState("");
	const [selected, setSelected] = useState<{ owner: OwnerKey; name: string } | null>(null);
	const [draft, setDraft] = useState<CreateDraft | null>(null);
	const [revealed, setRevealed] = useState<RevealedSecret | null>(null);
	const [revoking, setRevoking] = useState<Token | null>(null);

	const current = data.owners[ownerKey];
	const { owner, tokens } = current;
	const query = q.trim().toLowerCase();
	const rows = tokens
		.filter((t) => matchesQuery(data.tree, t, query))
		.sort((a, b) => {
			const ea = tokenStatus(a, now) === "expired" ? 1 : 0;
			const eb = tokenStatus(b, now) === "expired" ? 1 : 0;
			if (ea !== eb) return ea - eb;
			return tsMs(a.expiresAt) - tsMs(b.expiresAt);
		});
	const selectedName = selected?.owner === ownerKey ? selected.name : null;
	const selectedToken = selectedName === null ? undefined : tokens.find((t) => t.name === selectedName);
	const activeName = selectedToken === undefined ? null : selectedToken.name;

	const setOwner = (next: OwnerKey) => {
		setParams(
			(prev) => {
				const p = new URLSearchParams(prev);
				if (next === "workspace") p.delete("owner");
				else p.set("owner", next);
				return p;
			},
			{ replace: true },
		);
		setSelected(null);
	};

	const invalidate = async () => {
		const key = createConnectQueryKey({ schema: listTokens, cardinality: "finite" });
		await queryClient.invalidateQueries({ queryKey: key });
	};

	const duplicate = (token: Token) => {
		const items: { key: string; level: Level }[] = [];
		for (const [key, level] of tokenGrants(token)) {
			if (!data.tree.nodes.has(key)) continue;
			const max = heldLevel(data.held, data.tree, key);
			if (max === 0) continue;
			items.push({ key, level: level > max ? max : level });
		}
		setDraft({ name: `${token.name}-copy`, items });
	};

	const listError = current.error ?? data.scopesError;
	const emptyLabel = (() => {
		if (!owner.canList) return data.scopesLoading ? "" : `You don't have access to ${owner.label} tokens.`;
		if (listError !== null && listError !== undefined) return getErrorMessage(listError, "Failed to load tokens");
		return tokens.length > 0 ? "No tokens match." : "No tokens yet.";
	})();

	const ownerEmpty = owner.canList && !current.isLoading && (listError === null || listError === undefined) && tokens.length === 0;
	const newTokenButton = (
		<Button
			size="lg"
			disabled={!owner.canCreate}
			title={owner.canCreate ? undefined : `You need write access to ${owner.label} to create tokens`}
			onClick={() => {
				setDraft({ name: "", items: [] });
			}}
			className="px-3.5"
		>
			<PlusIcon />
			New token
		</Button>
	);

	return (
		<Page className="max-w-[1360px] gap-[18px]">
			<PageHeader title="Tokens" actions={ownerEmpty ? undefined : newTokenButton} />
			<div className="flex flex-wrap items-center gap-2.5">
				<ToggleGroup
					variant="segmented"
					value={[ownerKey]}
					onValueChange={(v: string[]) => {
						const next = OWNER_KEYS.find((k) => k === v[0]);
						if (next !== undefined) setOwner(next);
					}}
				>
					{OWNER_KEYS.map((k) => {
						const o = data.owners[k];
						const count = o.owner.canList && !o.isLoading ? o.tokens.length.toString() : "";
						return (
							<ToggleGroupItem
								key={k}
								value={k}
								title={o.owner.kind}
								className="h-7! gap-1.5 px-3 text-[12.5px] [&_svg]:size-[13px]"
							>
								{ownerIcon(k)}
								{o.owner.label}
								{count !== "" && <span className="text-xs font-normal text-fg3">{count}</span>}
							</ToggleGroupItem>
						);
					})}
				</ToggleGroup>
				<div className="flex-1" />
				<SearchInput
					value={q}
					onChange={(e) => {
						setQ(e.target.value);
					}}
					placeholder="Search"
					aria-label="Search tokens"
					className="w-60"
				/>
			</div>
			<div
				className={cn(
					"grid items-start gap-5",
					selectedToken === undefined ? "grid-cols-1" : "grid-cols-1 xl:grid-cols-[minmax(0,1fr)_minmax(360px,420px)]",
				)}
			>
				{ownerEmpty ? (
					<section className="rounded-lg border border-line bg-background">
						<EmptyState icon={<KeyRoundIcon />} title={`No tokens for ${owner.label} yet`} action={newTokenButton}>
							Tokens let CI, scripts and the CLI call the Loco API with exactly the access you grant.
						</EmptyState>
					</section>
				) : (
					<TokenList
						tokens={owner.canList ? rows : []}
						tree={data.tree}
						now={now}
						selected={activeName}
						onSelect={(name) => {
							setSelected(name === null ? null : { owner: ownerKey, name });
						}}
						isLoading={current.isLoading}
						emptyLabel={emptyLabel}
					/>
				)}
				{selectedToken !== undefined && (
					<TokenPanel
						key={selectedToken.name}
						token={selectedToken}
						owner={owner}
						tree={data.tree}
						now={now}
						onClose={() => {
							setSelected(null);
						}}
						onDuplicate={() => {
							duplicate(selectedToken);
						}}
						onRevoke={() => {
							setRevoking(selectedToken);
						}}
					/>
				)}
			</div>
			<CreateTokenDialog
				draft={draft}
				owner={owner}
				tree={data.tree}
				held={data.held}
				takenNames={tokens.map((t) => t.name)}
				resourcesLoading={data.resourcesLoading}
				onClose={() => {
					setDraft(null);
				}}
				onCreated={(name, secret) => {
					setDraft(null);
					setSelected({ owner: ownerKey, name });
					setRevealed({ name, secret });
					void invalidate();
				}}
			/>
			<SecretDialog
				revealed={revealed}
				onClose={() => {
					setRevealed(null);
				}}
			/>
			<RevokeDialog
				token={revoking}
				now={now}
				onClose={() => {
					setRevoking(null);
				}}
				onRevoked={(name) => {
					setRevoking(null);
					setSelected(null);
					toast.success(`Revoked ${name}`);
					void invalidate();
				}}
			/>
		</Page>
	);
}
