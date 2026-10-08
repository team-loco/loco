import { CheckIcon, CopyIcon, ServerIcon, ShieldCheckIcon } from "lucide-react";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";

import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { useCopy } from "@/hooks/useCopy";
import { INSTALL_COMMAND } from "@/pages/splash/links";

import { imageError } from "./drafts";

const STEPS = [INSTALL_COMMAND, "loco init", "loco deploy <name>"];

export function FirstServiceEmpty({
	scope,
	region,
	otherEnvName,
	onSwitchEnv,
	onImage,
}: {
	scope: string;
	region: string;
	otherEnvName: string | null;
	onSwitchEnv: () => void;
	onImage: (image: string) => void;
}) {
	const [image, setImage] = useState("");
	const [copied, copy] = useCopy(1400);
	const { data: configRes } = useQuery(getConfig, {});
	const platformDomain = configRes?.serviceDefaults?.platformDomain ?? "";
	const trimmed = image.trim();
	const error = trimmed === "" ? null : imageError(trimmed);
	const ok = trimmed !== "" && error === null;

	const submit = () => {
		if (!ok) return;
		onImage(trimmed);
	};

	return (
		<section className="grid grid-cols-1 rounded-lg border border-line bg-background md:grid-cols-2">
			<div className="flex flex-col gap-5 border-line px-7 pt-7 pb-8 md:border-r">
				<div className="flex flex-col gap-1.5">
					<span className="text-[18px] font-semibold tracking-[-0.01em]">Deploy your first service</span>
					<span className="text-fg2">{scope} has no resources.</span>
				</div>
				<div className="flex flex-col gap-2.5">
					<span className="text-sm font-semibold text-fg2">From your terminal</span>
					{STEPS.map((cmd, i) => {
						const key = i.toString();
						const done = copied === key;
						return (
							<div key={cmd} className="flex items-center gap-3">
								<span className="flex size-5 shrink-0 items-center justify-center rounded-full border border-line text-xs text-fg3">
									{i + 1}
								</span>
								<div className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-sm border border-line bg-bg2 pr-1.5 pl-3 font-mono text-[12.5px]">
									<span className="text-fg4">$</span>
									<span className="min-w-0 flex-1 truncate">{cmd}</span>
									<Button
										variant="ghost"
										size="icon-xs"
										aria-label="Copy command"
										className={done ? "text-ok-fg" : "text-fg3"}
										onClick={() => {
											copy(key, cmd);
										}}
									>
										{done ? <CheckIcon /> : <CopyIcon />}
									</Button>
								</div>
							</div>
						);
					})}
				</div>
				<div className="flex flex-col gap-2.5">
					<span className="text-sm font-semibold text-fg2">Or from a container image</span>
					<div className="flex gap-2">
						<Input
							value={image}
							placeholder="ghcr.io/team/api:1.4.2"
							aria-label="Container image"
							aria-invalid={error !== null}
							className="h-9 min-w-0 flex-1 px-3 text-[13.5px]"
							onChange={(e) => {
								setImage(e.target.value);
							}}
							onKeyDown={(e) => {
								if (e.key === "Enter") {
									e.preventDefault();
									submit();
								}
							}}
						/>
						<Button className="h-9 px-3.5" disabled={!ok} onClick={submit}>
							Continue
						</Button>
					</div>
					{error !== null && <span className="text-sm text-bad-fg">{error}</span>}
				</div>
				{otherEnvName !== null && (
					<Button variant="link" className="self-start text-fg2 underline-offset-3" onClick={onSwitchEnv}>
						View {otherEnvName} instead
					</Button>
				)}
			</div>
			<div className="hidden min-h-80 items-center justify-center rounded-r-lg bg-[radial-gradient(var(--line)_1px,transparent_1px)] bg-size-[16px_16px] md:flex">
				<div className="flex w-[150px] flex-col gap-1 rounded-md border border-line2 bg-background px-3 py-2.5">
					<span className="flex items-center gap-2 font-semibold">
						<ShieldCheckIcon className="size-[15px] text-fg3" />
						Gateway
					</span>
					{platformDomain !== "" && <span className="text-sm text-fg3">*.{platformDomain}</span>}
				</div>
				<div className="w-14 border-t-2 border-dashed border-line2" />
				<div className="flex w-[150px] flex-col gap-1 rounded-md border border-dashed border-line2 bg-bg2 px-3 py-2.5 text-fg3">
					<span className="flex items-center gap-2 font-semibold">
						<ServerIcon className="size-[15px]" />
						your service
					</span>
					<span className="text-sm">{region}</span>
				</div>
			</div>
		</section>
	);
}
