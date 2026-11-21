import { useQuery } from "@connectrpc/connect-query";
import { CheckIcon, CopyIcon, GlobeIcon, PlusIcon, ShieldCheckIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";

import { Button } from "@/components/design/Button";
import { DocsButton } from "@/components/design/DocsButton";
import { EmptyHero } from "@/components/design/EmptyHero";
import { useCopy } from "@/hooks/useCopy";
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

function DeployCommand() {
	const [copied, copy] = useCopy();
	return (
		<Button
			variant="outline"
			size="lg"
			title="Copy"
			aria-label={copied === null ? `Copy ${DEPLOY_COMMAND}` : "Copied"}
			className="my-1 gap-2.5 bg-background pr-2.5 pl-3 font-mono"
			onClick={() => {
				copy(DEPLOY_COMMAND, DEPLOY_COMMAND);
			}}
		>
			<span>
				<span className="text-fg4">$ </span>
				{DEPLOY_COMMAND}
			</span>
			{copied === null ? <CopyIcon className="text-fg3" /> : <CheckIcon className="text-ok-fg" />}
		</Button>
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
			subtitle="Run loco deploy in a folder with a Dockerfile, or start from an image."
		>
			<DeployCommand />
			<DocsButton href={DOCS_URL} />
		</EmptyHero>
	);
}
