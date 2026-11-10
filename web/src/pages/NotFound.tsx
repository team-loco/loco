import { LayoutGridIcon, SearchIcon } from "lucide-react";
import { Link, useLocation } from "react-router";

import { PageTitle } from "@/components/PageTitle";
import { Button } from "@/components/design/Button";
import { ART_SOFT, ART_STRONG, ClusterArt } from "@/components/design/ClusterArt";
import { EmptyHero } from "@/components/design/EmptyHero";
import { Kbd } from "@/components/design/Kbd";
import { SHORTCUT_LABEL, useOpenPalette } from "@/components/shell/CommandMenu";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { workspacePath } from "@/lib/routes";

function NotFoundArt() {
	return (
		<ClusterArt>
			<rect x={142} y={26} width={82} height={11} rx={5.5} fill="var(--panel)" stroke="var(--line2)" />
			<circle cx={148.5} cy={31.5} r={1.6} fill={ART_STRONG} />
			<rect x={153} y={30} width={44} height={3} rx={1.5} fill={ART_SOFT} />
			<path
				d="M183 37 C183 50 160 46 160 58 C160 66 172 66 176 70"
				fill="none"
				stroke={ART_STRONG}
				strokeDasharray="2 2"
			/>
			<rect x={178} y={62} width={44} height={30} rx={4} fill="none" stroke={ART_STRONG} strokeDasharray="3 2.5" />
			<text x={200} y={81} textAnchor="middle" fontFamily="var(--font-mono)" fontSize={10} fill={ART_STRONG}>
				404
			</text>
		</ClusterArt>
	);
}

export function NotFound() {
	const { pathname, search } = useLocation();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const openPalette = useOpenPalette();
	const dashboard =
		activeOrgId !== null && activeWorkspaceId !== null ? workspacePath(activeOrgId, activeWorkspaceId) : "/dashboard";

	return (
		<div className="flex min-h-[calc(100vh-140px)] items-center justify-center px-4">
			<PageTitle title="Page not found" />
			<EmptyHero
				art={<NotFoundArt />}
				title="This page doesn’t exist"
				subtitle="The link may be old, or the resource was renamed or deleted."
				className="w-full max-w-[560px] bg-transparent py-10"
			>
				<span className="max-w-full truncate rounded-md bg-panel px-2.5 py-1 font-mono text-[12.5px] text-fg3">
					{pathname + search}
				</span>
				<div className="mt-1.5 flex flex-wrap justify-center gap-2">
					<Button variant="outline" nativeButton={false} role="link" render={<Link to={dashboard} />}>
						<LayoutGridIcon className="text-fg3" aria-hidden />
						Go to dashboard
					</Button>
					<Button variant="outline" aria-keyshortcuts="Meta+K Control+K" onClick={openPalette}>
						<SearchIcon className="text-fg3" aria-hidden />
						Search
						<Kbd className="ml-0.5">{SHORTCUT_LABEL}</Kbd>
					</Button>
				</div>
			</EmptyHero>
		</div>
	);
}
