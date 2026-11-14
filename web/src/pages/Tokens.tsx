import { useQueryClient } from "@tanstack/react-query";
import { ExternalLinkIcon, PlusIcon, SearchXIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listTokens } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType, type Token } from "@gen/loco/token/v1/token_pb";
import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";

import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useNow } from "@/hooks/useNow";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { limitReason, limitUsage } from "@/lib/limits";
import { usePagedRows } from "@/lib/paging";
import { tsMs } from "@/lib/time";

import { DOCS_URL } from "./splash/links";
import { CreateTokenDrawer } from "./tokens/CreateTokenDrawer";
import type { CreateDraft } from "./tokens/CreateTokenDrawer";
import { ownerIcon } from "./tokens/icons";
import { LockArt } from "./tokens/LockArt";
import { heldLevel, matchesQuery, OWNER_KEYS, OWNER_TITLE, tokenGrants, tokenStatus } from "./tokens/model";
import type { Level, OwnerKey } from "./tokens/model";
import { RevokeDialog } from "./tokens/RevokeDialog";
import { TokenTable } from "./tokens/TokenTable";
import { useTokenCreators } from "./tokens/useTokenCreators";
import { useTokenData } from "./tokens/useTokenData";
import type { Owner } from "./tokens/useTokenData";

const PAGE_SIZES = [25, 50, 100] as const;
const NOW_TICK_MS = 60_000;
const NEW_PARAM = "new";
const TOKEN_DOCS = `${DOCS_URL}/operations/access/#deploy-from-ci-with-an-api-token`;

function parseOwner(value: string | null): OwnerKey {
	return OWNER_KEYS.find((k) => k === value) ?? "workspace";
}

function firstRun(owner: Owner): { title: string; body: string; snippet: string[] } {
	switch (owner.key) {
		case "org":
			return {
				title: "No organization tokens yet",
				body: `Organization tokens belong to ${owner.label}, so automation keeps working when people leave.`,
				snippet: ["# provisioning and org-wide automation", "export LOCO_TOKEN=…", "loco workspace list"],
			};
		case "workspace":
			return {
				title: "No workspace tokens yet",
				body: `Workspace tokens let CI deploy to ${owner.label}, or to just a few of its resources.`,
				snippet: [
					"# .github/workflows/deploy.yml",
					"- run: loco deploy --env production",
					"  env:",
					"    LOCO_TOKEN: ${{ secrets.LOCO_TOKEN }}",
				],
			};
		case "personal":
			return {
				title: "No personal tokens yet",
				body: "Personal tokens act as you, for scripts on your own machine.",
				snippet: ["# ~/.zshrc", "export LOCO_TOKEN=…", "loco resource logs web --follow"],
			};
	}
}

function SnippetArt({ lines }: { lines: string[] }) {
	return (
		<pre className="m-0 w-[min(420px,100%)] overflow-x-auto rounded-[10px] border border-line2 bg-background px-3.5 py-3 text-left font-mono text-[12px] leading-[1.7]">
			{lines.map((line) => (
				<div key={line} className={line.startsWith("#") ? "text-fg3" : "text-foreground"}>
					{line}
				</div>
			))}
		</pre>
	);
}

export function Tokens() {
	const [params, setParams] = useSearchParams();
	const ownerKey = parseOwner(params.get("owner"));
	const data = useTokenData();
	const { activeOrgId } = useOrgWorkspace();
	const limits = useOrgLimits(activeOrgId ?? "");
	const config = useQuery(getConfig, {});
	const queryClient = useQueryClient();
	const now = useNow(NOW_TICK_MS).getTime();
	const [q, setQ] = useState("");
	const [draft, setDraft] = useState<CreateDraft | null>(null);
	const [revoking, setRevoking] = useState<Token | null>(null);

	const current = data.owners[ownerKey];
	const { owner, tokens } = current;
	const creators = useTokenCreators(owner);
	const query = q.trim().toLowerCase();
	const rows = tokens
		.filter((t) => matchesQuery(data.tree, t, query, creators.get(t.createdBy)?.name))
		.toSorted((a, b) => {
			const ea = tokenStatus(a, now) === "expired" ? 1 : 0;
			const eb = tokenStatus(b, now) === "expired" ? 1 : 0;
			if (ea !== eb) return ea - eb;
			return tsMs(a.expiresAt) - tsMs(b.expiresAt);
		});
	const paged = usePagedRows(owner.canList ? rows : [], PAGE_SIZES);
	const liveCount = (k: OwnerKey) => data.owners[k].tokens.filter((t) => tokenStatus(t, now) !== "expired").length;
	const anyLoading = data.scopesLoading || OWNER_KEYS.some((k) => data.owners[k].isLoading);

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
		paged.reset();
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
		setDraft({ owner: ownerKey, name: `${token.name}-copy`, items });
	};

	// The command palette links to ?new=1 to open the create drawer.
	const shownDraft =
		draft ?? (params.get(NEW_PARAM) === "1" && owner.canCreate ? { owner: ownerKey, name: "", items: [] } : null);

	const openCreate = () => {
		setDraft({ owner: ownerKey, name: "", items: [] });
	};
	const tokenLimit =
		owner.entityType === EntityType.USER ? config.data?.countLimits?.tokensPerEntity : limits?.tokensPerEntity;
	const scope = ownerKey === "personal" ? "account" : OWNER_TITLE[ownerKey].toLowerCase();
	const usage = current.isLoading || current.error ? null : limitUsage(tokens.length, tokenLimit, "tokens", scope);
	const createBlocked = owner.canCreate
		? limitReason(usage)
		: `You need write access to ${owner.label} to create tokens`;
	const newTokenButton = (
		<Button disabled={createBlocked !== null} onClick={openCreate}>
			<PlusIcon />
			New token
		</Button>
	);

	const listError = current.error ?? data.scopesError;
	const firstRunEmpty = owner.canList && (listError === null || listError === undefined) && tokens.length === 0;
	const empty = (() => {
		if (!owner.canList) {
			if (data.scopesLoading) return null;
			return <div className="py-7 text-center text-fg3">{`You don't have access to ${owner.label} tokens.`}</div>;
		}
		if (tokens.length > 0) {
			return (
				<EmptyState
					icon={<SearchXIcon />}
					title="No tokens match"
					query={q.trim()}
					action={
						<Button
							variant="outline"
							onClick={() => {
								setQ("");
							}}
						>
							<XIcon />
							Clear search
						</Button>
					}
				/>
			);
		}
		return null;
	})();
	const intro = firstRun(owner);

	return (
		<Page>
			<PageHeader
				title={
					<>
						Tokens
						{!anyLoading && <LimitCount count={tokens.length} usage={usage} />}
					</>
				}
				actions={
					<LimitedAction usage={usage} reason={createBlocked}>
						{newTokenButton}
					</LimitedAction>
				}
			/>
			<Section
				filters={
					<ToggleGroup
						variant="segmented"
						aria-label="Owner"
						className="max-w-full overflow-x-auto"
						value={[ownerKey]}
						onValueChange={(v: string[]) => {
							const next = OWNER_KEYS.find((k) => k === v[0]);
							if (next !== undefined) setOwner(next);
						}}
					>
						{OWNER_KEYS.map((k) => {
							const o = data.owners[k];
							const count = o.owner.canList && !o.isLoading ? liveCount(k) : undefined;
							return (
								<ToggleGroupItem
									key={k}
									value={k}
									title={o.owner.label}
									count={count}
									className="[&_svg]:size-[13px] [&_svg]:text-fg3"
								>
									{ownerIcon(k)}
									{OWNER_TITLE[k]}
								</ToggleGroupItem>
							);
						})}
					</ToggleGroup>
				}
				search={
					<SearchInput
						value={q}
						onChange={(e) => {
							setQ(e.target.value);
							paged.reset();
						}}
						onClear={() => {
							setQ("");
							paged.reset();
						}}
						placeholder="Search by name, creator or access"
						aria-label="Search tokens"
					/>
				}
			>
				{listError !== null && listError !== undefined ? (
					<ErrorState
						error={listError}
						what="tokens"
						rpc={listTokens}
						onRetry={current.refetch}
						className="rounded-none border-t border-line2"
					/>
				) : firstRunEmpty && !current.isLoading ? (
					<EmptyHero
						art={
							<div className="flex w-full flex-col items-center gap-5">
								<LockArt />
								<SnippetArt lines={intro.snippet} />
							</div>
						}
						title={intro.title}
						subtitle={intro.body}
						className="rounded-none border-t border-line2"
					>
						<span className="flex flex-wrap justify-center gap-2">
							<LimitedAction usage={null} reason={createBlocked}>
								{newTokenButton}
							</LimitedAction>
							<Button variant="outline" render={<a href={TOKEN_DOCS} target="_blank" rel="noopener noreferrer" />}>
								Read the docs
								<ExternalLinkIcon className="text-fg3" />
							</Button>
						</span>
					</EmptyHero>
				) : (
					<>
						<TokenTable
							tokens={paged.rows}
							creators={creators}
							tree={data.tree}
							now={now}
							isLoading={current.isLoading}
							empty={empty}
							canDuplicate={owner.canCreate}
							canRevoke={owner.canCreate || owner.key === "personal"}
							onDuplicate={duplicate}
							onRevoke={setRevoking}
						/>
						<Pager {...paged.pager} />
					</>
				)}
			</Section>
			<CreateTokenDrawer
				draft={shownDraft}
				data={data}
				now={now}
				onClose={() => {
					setDraft(null);
					if (params.has(NEW_PARAM)) {
						setParams(
							(prev) => {
								const p = new URLSearchParams(prev);
								p.delete(NEW_PARAM);
								return p;
							},
							{ replace: true },
						);
					}
				}}
				onCreated={(created) => {
					if (created !== ownerKey) setOwner(created);
					void invalidate();
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
					toast.success(`Revoked ${name}`);
					void invalidate();
				}}
			/>
		</Page>
	);
}
