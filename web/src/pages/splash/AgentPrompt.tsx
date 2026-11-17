import { BotIcon, CheckIcon, CopyIcon, SparklesIcon, TerminalIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import "./AgentPrompt.css";

const AGENT_PROMPT = "Deploy my app to Loco: https://loco.build/skills.md";

export function AgentPrompt() {
	const [copied, copy] = useCopy(1600);
	return (
		<Button
			variant="outline"
			className="sp-agent"
			title={`Copies: ${AGENT_PROMPT}`}
			onClick={() => {
				copy("agent", AGENT_PROMPT);
			}}
		>
			<span className="sp-agent-t" aria-live="polite">
				{copied === null ? "Onboard your agent to Loco" : "Copied. Paste it into your agent"}
			</span>
			<span className="sp-agent-ics" aria-hidden>
				<span className="sp-agent-ic">
					<TerminalIcon />
				</span>
				<span className="sp-agent-ic">
					<BotIcon />
				</span>
				<span className="sp-agent-ic">
					<SparklesIcon />
				</span>
				<span className="sp-agent-ic">{copied === null ? <CopyIcon /> : <CheckIcon />}</span>
			</span>
		</Button>
	);
}
