import { LayoutGridIcon, SearchIcon } from "lucide-react";
import { Component, type ComponentProps, type ReactNode } from "react";
import { Link, useLocation } from "react-router";

import { PageTitle } from "@/components/PageTitle";
import { Button } from "@/components/design/Button";
import { ART_SOFT, ART_STRONG, ClusterArt } from "@/components/design/ClusterArt";
import { EmptyHero } from "@/components/design/EmptyHero";
import { Kbd } from "@/components/design/Kbd";
import { LocoLogo } from "@/components/design/LocoLogo";
import { SHORTCUT_LABEL, useOpenPalette } from "@/components/shell/CommandMenu";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { PageNotFound } from "@/lib/not-found";
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

function DashboardLink({
	unknownWorkspace,
	staticPage,
	children,
	...props
}: ComponentProps<"a"> & { unknownWorkspace: boolean; staticPage: boolean }) {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const to =
		!unknownWorkspace && activeOrgId !== null && activeWorkspaceId !== null
			? workspacePath(activeOrgId, activeWorkspaceId)
			: "/dashboard";
	return staticPage ? (
		<a href={to} {...props}>
			{children}
		</a>
	) : (
		<Link to={to} {...props}>
			{children}
		</Link>
	);
}

function MissingPath() {
	const { pathname, search } = useLocation();
	return (
		<span className="max-w-full truncate rounded-[7px] border border-dashed border-[color-mix(in_oklch,var(--err)_55%,var(--line2))] bg-background px-2.5 py-1 font-mono text-[15px] text-foreground">
			{pathname + search}
		</span>
	);
}

function SearchButton() {
	const openPalette = useOpenPalette();
	return (
		<Button variant="outline" aria-keyshortcuts="Meta+K Control+K" onClick={openPalette}>
			<SearchIcon className="text-fg3" aria-hidden />
			Search
			<Kbd className="ml-0.5">{SHORTCUT_LABEL}</Kbd>
		</Button>
	);
}

/**
 * `staticPage` renders the page for the 404 file the static server returns for missing files and unknown
 * documentation paths, built by build.ts. That file runs no script and has no router, so it leaves out the
 * parts that need them (the missing path and the search button) and gets the logo inline, because /logo.svg
 * does not exist on the documentation host.
 */
export function NotFound({
	unknownWorkspace = false,
	staticPage,
}: {
	unknownWorkspace?: boolean;
	staticPage?: { logoUrl: string };
}) {
	const isStatic = staticPage !== undefined;

	return (
		<main className="flex min-h-dvh flex-col bg-background text-foreground">
			<PageTitle title="Page not found" />
			<header className="px-[clamp(16px,3vw,28px)] pt-5">
				<DashboardLink
					unknownWorkspace={unknownWorkspace}
					staticPage={isStatic}
					aria-label="Loco dashboard"
					className="inline-flex h-8 items-center rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
				>
					<LocoLogo className="w-[46px]" src={staticPage?.logoUrl} />
				</DashboardLink>
			</header>
			<div className="flex flex-1 items-center justify-center px-4 pb-14">
				<EmptyHero
					art={
						<>
							<NotFoundArt />
							{!isStatic && <MissingPath />}
						</>
					}
					title="Nothing lives at this address"
					subtitle="The link may be wrong, or you may not have access to it."
					className="w-full max-w-[560px] bg-transparent py-10"
				>
					<div className="mt-1.5 flex flex-wrap justify-center gap-2">
						<Button
							variant="outline"
							nativeButton={false}
							render={<DashboardLink unknownWorkspace={unknownWorkspace} staticPage={isStatic} />}
						>
							<LayoutGridIcon className="text-fg3" aria-hidden />
							Go to dashboard
						</Button>
						{!isStatic && <SearchButton />}
					</div>
				</EmptyHero>
			</div>
		</main>
	);
}

interface BoundaryState {
	path: string;
	error: Error | null;
}

export class NotFoundBoundary extends Component<{ path: string; children: ReactNode }, BoundaryState> {
	override state: BoundaryState = { path: this.props.path, error: null };

	static getDerivedStateFromProps(props: { path: string }, state: BoundaryState): Partial<BoundaryState> | null {
		return props.path === state.path ? null : { path: props.path, error: null };
	}

	static getDerivedStateFromError(error: unknown): Partial<BoundaryState> {
		return { error: error instanceof Error ? error : new Error(String(error)) };
	}

	override render(): ReactNode {
		if (this.state.error === null) return this.props.children;
		if (this.state.error instanceof PageNotFound) return <NotFound />;
		throw this.state.error;
	}
}
