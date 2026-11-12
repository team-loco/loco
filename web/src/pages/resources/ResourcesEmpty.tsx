import { useQuery } from "@connectrpc/connect-query";
import { ArrowUpRightIcon, GlobeIcon, PlusIcon, ShieldCheckIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";

import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";
import { EmptyHero } from "@/components/design/EmptyHero";
import { DOCS_URL } from "@/pages/splash/links";

const DEPLOY_COMMAND = "loco deploy";
const NODE = "flex h-[38px] items-center gap-[7px] rounded-[9px] border px-3 text-[12.5px] whitespace-nowrap";

function Node({ children }: { children: ReactNode }) {
	return <span className={`${NODE} border-line2 bg-background text-fg2 [&_svg]:size-3.5`}>{children}</span>;
}

function ResourcesArt({ domain, newHref }: { domain: string | undefined; newHref: string }) {
	return (
		<div className="flex max-w-full flex-wrap items-center justify-center gap-y-2">
			<Node>
				<GlobeIcon />
				Internet
			</Node>
			<span aria-hidden className="h-px w-9 bg-line2" />
			<Node>
				<ShieldCheckIcon />
				<span className="font-mono">{domain === undefined ? "Gateway" : `*.${domain}`}</span>
			</Node>
			<span aria-hidden className="w-9 animate-pulse border-t border-dashed border-fg3" />
			<Link
				to={newHref}
				aria-label="New resource"
				className={`${NODE} border-dashed border-fg3 text-foreground no-underline hover:bg-panel2 [&_svg]:size-3.5`}
			>
				<PlusIcon />
				your first resource
			</Link>
		</div>
	);
}

export function ResourcesEmpty({ environmentId, newHref }: { environmentId: string | undefined; newHref: string }) {
	const domainsQuery = useQuery(
		listPlatformDomains,
		{ environmentId: environmentId ?? "" },
		{ enabled: environmentId !== undefined },
	);
	return (
		<EmptyHero
			art={<ResourcesArt domain={domainsQuery.data?.platformDomains[0]?.domain} newHref={newHref} />}
			title="Deploy your first resource"
			subtitle="Run loco deploy in a folder with a Dockerfile, or start one from New resource."
		>
			<div className="my-2 flex w-[min(320px,100%)] items-center justify-between gap-6 rounded-lg bg-background py-3 pr-3 pl-4 text-left ring-1 ring-line">
				<span className="font-mono text-[13px]">
					<span className="text-fg4">$ </span>
					{DEPLOY_COMMAND}
				</span>
				<CopyButton text={DEPLOY_COMMAND} name="Copy loco deploy" />
			</div>
			<Button
				variant="outline"
				nativeButton={false}
				render={<a href={DOCS_URL} target="_blank" rel="noopener noreferrer" />}
			>
				Read the docs
				<ArrowUpRightIcon />
			</Button>
		</EmptyHero>
	);
}
