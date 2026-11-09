import { CheckIcon, ChevronsUpDownIcon, XIcon } from "lucide-react";
import { useId, type ReactNode } from "react";
import type { PlatformDomain } from "@gen/loco/domain/v1/domain_pb";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
import { getErrorMessage } from "@/lib/error-handler";
import type { DomainCheck } from "@/lib/platform-domain";
import { cn } from "@/lib/utils";

function statusLine(check: DomainCheck, owner: string): { tone: "ok" | "bad" | "muted"; text: ReactNode } | null {
	switch (check.state) {
		case "empty":
			return null;
		case "invalid":
			return { tone: "bad", text: "Use a–z, 0–9 and dashes" };
		case "mine":
			return { tone: "bad", text: `Already on ${owner}` };
		case "checking":
			return { tone: "muted", text: `Checking ${check.host}…` };
		case "available":
			return { tone: "ok", text: `${check.host} is available` };
		case "taken":
			return { tone: "bad", text: `${check.host} is taken` };
		case "failed":
			return { tone: "bad", text: getErrorMessage(check.error, "Couldn't check availability") };
	}
}

export function PlatformDomainInput({
	subdomain,
	platformDomain,
	options,
	check,
	owner = "this resource",
	placeholder,
	disabled,
	describedBy,
	className,
	onSubdomainChange,
	onPlatformDomainChange,
}: {
	subdomain: string;
	platformDomain: string;
	options: PlatformDomain[];
	check: DomainCheck;
	owner?: string;
	placeholder?: string;
	disabled?: boolean;
	describedBy?: string | undefined;
	className?: string;
	onSubdomainChange: (subdomain: string) => void;
	onPlatformDomainChange: (platformDomain: string) => void;
}) {
	const statusId = useId();
	const status = statusLine(check, owner);
	const describe = [status === null ? undefined : statusId, describedBy].filter(Boolean).join(" ") || undefined;
	return (
		<div className={cn("flex min-w-0 flex-col gap-2", className)}>
			<InputGroup>
				<InputGroupInput
					value={subdomain}
					placeholder={placeholder}
					disabled={disabled}
					autoComplete="off"
					spellCheck={false}
					aria-label="Subdomain"
					aria-describedby={describe}
					aria-invalid={status?.tone === "bad"}
					className="text-right font-mono text-[12.5px] md:text-[12.5px]"
					onChange={(e) => {
						onSubdomainChange(e.target.value.toLowerCase());
					}}
				/>
				<InputGroupAddon align="inline-end" className="gap-0.5 pr-1">
					<InputGroupText>.</InputGroupText>
					{options.length === 0 ? (
						<InputGroupText>no platform domain</InputGroupText>
					) : (
						<DropdownMenu>
							<DropdownMenuTrigger
								disabled={disabled}
								render={
									<Button
										variant="ghost"
										size="xs"
										className="bg-panel2 font-mono text-[12.5px] text-foreground"
										aria-label={`Platform domain: ${platformDomain}`}
									/>
								}
							>
								{platformDomain}
								<ChevronsUpDownIcon className="text-fg3" />
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="w-auto min-w-[260px]">
								<DropdownMenuRadioGroup value={platformDomain} onValueChange={onPlatformDomainChange}>
									{options.map((d) => (
										<DropdownMenuRadioItem key={d.domain} value={d.domain}>
											<span className="flex flex-col gap-px">
												<span className="font-mono text-[12.5px]">{d.domain}</span>
												<span className="font-mono text-[11px] text-fg3">
													{d.regions.length === 0 ? "no regions" : d.regions.join(", ")}
												</span>
											</span>
										</DropdownMenuRadioItem>
									))}
								</DropdownMenuRadioGroup>
							</DropdownMenuContent>
						</DropdownMenu>
					)}
				</InputGroupAddon>
			</InputGroup>
			<span
				id={statusId}
				aria-live="polite"
				className={cn(
					"flex items-center gap-1.5 text-[12.5px] empty:hidden",
					status?.tone === "ok" && "text-ok-fg",
					status?.tone === "bad" && "text-bad-fg",
					status?.tone === "muted" && "text-fg3",
				)}
			>
				{status?.tone === "ok" && <CheckIcon className="size-3.5" />}
				{status?.tone === "bad" && <XIcon className="size-3.5" />}
				{status?.text}
			</span>
		</div>
	);
}
