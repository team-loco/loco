import type { ReactNode } from "react"

import { Button } from "@/components/design/Button"
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup"
import { cn } from "@/lib/utils"

function Pager({
	label,
	pageSizes,
	pageSize,
	onPageSize,
	prevLabel = "Previous",
	nextLabel = "Next",
	onPrev,
	onNext,
	buttonSize = "xs",
	className,
}: {
	label: ReactNode
	pageSizes?: readonly number[] | undefined
	pageSize?: number | undefined
	onPageSize?: ((size: number) => void) | undefined
	prevLabel?: string | undefined
	nextLabel?: string | undefined
	onPrev: (() => void) | null
	onNext: (() => void) | null
	buttonSize?: "xs" | "sm" | undefined
	className?: string | undefined
}) {
	const sizes = pageSizes !== undefined && pageSize !== undefined && onPageSize !== undefined
	return (
		<div className={cn("flex flex-wrap items-center justify-between gap-2 px-4 py-2 text-sm text-fg3", className)}>
			<div className="flex items-center gap-3">
				<span className="tabular-nums">{label}</span>
				{sizes && (
					<div className="flex items-center gap-1.5">
						Rows
						<ToggleGroup
							variant="segmented"
							value={[String(pageSize)]}
							onValueChange={(v: string[]) => {
								const n = Number(v[0])
								if (Number.isFinite(n) && n > 0) onPageSize(n)
							}}
							className="rounded-md p-0.5"
						>
							{pageSizes.map((n) => (
								<ToggleGroupItem key={n} value={String(n)} className="h-5! rounded-sm! px-2 text-sm font-normal">
									{n}
								</ToggleGroupItem>
							))}
						</ToggleGroup>
					</div>
				)}
			</div>
			<div className="flex gap-1">
				<Button variant="outline" size={buttonSize} disabled={onPrev === null} onClick={onPrev ?? undefined}>
					{prevLabel}
				</Button>
				<Button variant="outline" size={buttonSize} disabled={onNext === null} onClick={onNext ?? undefined}>
					{nextLabel}
				</Button>
			</div>
		</div>
	)
}

export { Pager }
