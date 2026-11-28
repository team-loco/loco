import { useDeferredValue, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { BoxIcon, CheckIcon, GlobeIcon } from "lucide-react";
import { ResourceOrder, ResourceService, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { CommandGroup, CommandItem } from "@/components/design/Command";
import { FilterPopover } from "@/components/design/FilterPopover";
import { getErrorMessage } from "@/lib/error-handler";

const RESOURCE_PAGE_SIZE = 20;

export function MetricResources({
	workspaceId,
	environmentId,
	selected,
	ready,
	pending,
	onChange,
}: {
	workspaceId: string;
	environmentId: string;
	selected: Resource[];
	ready: boolean;
	pending: boolean;
	onChange: (resources: Resource[]) => void;
}) {
	const transport = useTransport();
	const [search, setSearch] = useState("");
	const query = useDeferredValue(search);
	const input = {
		workspaceId,
		environmentId,
		query,
		pageSize: RESOURCE_PAGE_SIZE,
		orderBy: ResourceOrder.NAME,
	};
	const scopeReady = workspaceId !== "" && environmentId !== "";
	const pages = useInfiniteQuery({
		queryKey: createConnectQueryKey({
			schema: ResourceService.method.listWorkspaceResources,
			transport,
			input,
			cardinality: "infinite",
		}),
		initialPageParam: "",
		queryFn: async ({ pageParam, signal }) =>
			await createClient(ResourceService, transport).listWorkspaceResources(
				{ ...input, pageToken: pageParam },
				{ signal },
			),
		getNextPageParam: (last) => last.nextPageToken || undefined,
		enabled: ready && scopeReady,
	});
	const listed = pages.data?.pages.flatMap((page) => page.resources) ?? [];
	const pinned = selected.filter((resource) => !listed.some((item) => item.id === resource.id));
	let value = pending ? "Loading resources…" : "All resources";
	if (selected.length === 1) value = selected[0]?.name ?? value;
	if (selected.length > 1) value = `${selected.length.toString()} resources`;
	const toggle = (resource: Resource) => {
		const exists = selected.some((item) => item.id === resource.id);
		onChange(exists ? selected.filter((item) => item.id !== resource.id) : [...selected, resource]);
	};
	const option = (resource: Resource) => (
		<CommandItem
			key={resource.id}
			value={resource.id}
			onSelect={() => {
				toggle(resource);
			}}
		>
			<BoxIcon className="text-fg3" />
			<span className="min-w-0 flex-1 truncate">{resource.name}</span>
			{selected.some((item) => item.id === resource.id) && <CheckIcon />}
		</CommandItem>
	);
	return (
		<FilterPopover
			icon={<BoxIcon />}
			label="Resources"
			value={value}
			placeholder="Search resources…"
			onSearch={setSearch}
			width="w-[340px] max-w-[calc(100vw-32px)]"
			footer={
				pages.error === null ? undefined : (
					<span role="alert">{getErrorMessage(pages.error, "Could not search resources")}</span>
				)
			}
		>
			{(close) => (
				<>
					{query === "" && (
						<CommandItem
							value="all"
							onSelect={() => {
								onChange([]);
								close();
							}}
						>
							<span className="flex-1">All resources</span>
							{selected.length === 0 && <CheckIcon />}
						</CommandItem>
					)}
					{pinned.length > 0 && <CommandGroup heading="Selected">{pinned.map(option)}</CommandGroup>}
					<CommandGroup>{listed.map(option)}</CommandGroup>
					{pages.isPending && (
						<div role="status" className="px-3 py-2 text-[12px] text-fg3">
							Loading resources…
						</div>
					)}
					{pages.hasNextPage && (
						<CommandItem value="more" disabled={pages.isFetchingNextPage} onSelect={() => void pages.fetchNextPage()}>
							Show more
						</CommandItem>
					)}
				</>
			)}
		</FilterPopover>
	);
}

export function MetricRegions({
	regions,
	selected,
	colors,
	onChange,
}: {
	regions: string[];
	selected: string[];
	colors: Map<string, string>;
	onChange: (regions: string[]) => void;
}) {
	let value = "All regions";
	if (selected.length === 1) value = selected[0] ?? value;
	if (selected.length > 1) value = `${selected.length.toString()} regions`;
	const choices = [...new Set([...regions, ...selected])].toSorted();
	return (
		<FilterPopover
			icon={<GlobeIcon />}
			label="Regions"
			value={value}
			placeholder="Search regions…"
			width="w-[280px] max-w-[calc(100vw-32px)]"
		>
			{(close) => (
				<>
					<CommandItem
						value="all"
						onSelect={() => {
							onChange([]);
							close();
						}}
					>
						<span className="flex-1">All regions</span>
						{selected.length === 0 && <CheckIcon />}
					</CommandItem>
					{choices.map((region) => (
						<CommandItem
							key={region}
							value={region}
							onSelect={() => {
								onChange(
									selected.includes(region) ? selected.filter((item) => item !== region) : [...selected, region],
								);
							}}
						>
							<span className="size-2 rounded-[2px]" style={{ background: colors.get(region) }} />
							<span className="flex-1 font-mono text-[12px]">{region}</span>
							{selected.includes(region) && <CheckIcon />}
						</CommandItem>
					))}
					{selected.length > 0 && (
						<Button
							variant="ghost"
							size="xs"
							onClick={() => {
								onChange([]);
								close();
							}}
						>
							Clear regions
						</Button>
					)}
				</>
			)}
		</FilterPopover>
	);
}
