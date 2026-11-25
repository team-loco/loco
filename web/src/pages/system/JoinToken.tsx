import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { useState } from "react";

import { CodeBlock } from "@/components/design/CodeBlock";
import { InlineCode } from "@/components/design/InlineCode";
import { maskSecret, SecretValue } from "@/components/design/SecretValue";
import { JOIN_SECRET_NAME, workerInstallCommand, WORKER_NAMESPACE } from "@/lib/system";

import { SystemTime } from "./shared";

/** A join token, masked until revealed, and the commands that install the worker with it. */
export function JoinToken({
	region,
	token,
	expiresAt,
}: {
	region: string;
	token: string;
	expiresAt: Timestamp | undefined;
}) {
	const [shown, setShown] = useState(false);
	const command = workerInstallCommand(region, token);
	return (
		<div className="flex min-w-0 flex-col gap-3">
			<div className="flex flex-col gap-1.5">
				<span className="text-[13px] font-medium">Join token</span>
				<SecretValue secret={token} shown={shown} onShownChange={setShown} noun="join token" />
				<span className="text-[12.5px] text-fg3">
					Works once and is not shown again. Expires <SystemTime ts={expiresAt} />.
				</span>
			</div>
			<div className="flex flex-col gap-1.5">
				<span className="text-[13px] font-medium">Install the worker</span>
				<span className="text-[12.5px] text-fg3">
					Run these on the cluster, from a checkout of the loco repository. The namespace{" "}
					<InlineCode>{WORKER_NAMESPACE}</InlineCode> must exist, and the chart still needs your cluster&apos;s image
					tags, control plane URL and exporter endpoint in a values file. The agent reads the Secret{" "}
					<InlineCode>{JOIN_SECRET_NAME}</InlineCode>.
				</span>
				<CodeBlock
					lineNumbers={false}
					wrap
					label="Install command with the join token hidden"
					copyLabel="Copy install command"
					copyText={command}
				>
					{shown ? command : command.replace(token, maskSecret(token, false))}
				</CodeBlock>
			</div>
		</div>
	);
}
