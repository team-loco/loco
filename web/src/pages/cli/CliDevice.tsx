import { Spinner } from "@/components/design/Spinner";
import { useMutation } from "@connectrpc/connect-query";
import { useState, type SubmitEvent } from "react";
import { useSearchParams } from "react-router";
import { SquareTerminalIcon } from "lucide-react";

import { approveDeviceLogin } from "@gen/loco/auth/v1/auth-AuthService_connectquery";

import { forgetNextPath } from "@/auth/next";
import { Button } from "@/components/design/Button";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { getErrorMessage } from "@/lib/error-handler";

import { CliCard } from "./CliCard";

export function CliDevice() {
	const [params] = useSearchParams();
	const linkedCode = params.get("code");
	const [userCode, setUserCode] = useState(linkedCode ?? "");
	const approve = useMutation(approveDeviceLogin);

	const submit = (event: SubmitEvent<HTMLFormElement>) => {
		event.preventDefault();
		approve.mutate(
			{ userCode },
			{
				onSuccess: () => {
					forgetNextPath();
				},
			},
		);
	};

	const errorText = approve.error !== null ? getErrorMessage(approve.error, "Could not sign in the device") : undefined;

	if (approve.isSuccess) {
		return (
			<CliCard title="Device signed in">
				{() => <p className="m-0 text-center text-[13.5px] text-fg2">You can return to your terminal.</p>}
			</CliCard>
		);
	}

	return (
		<CliCard title="Sign in a device">
			{(email) => (
				<form className="flex flex-col gap-4" onSubmit={submit}>
					<p className="m-0 text-center text-[13.5px] text-fg2">
						{linkedCode === null ? "Enter the code shown by " : "Approve the device running "}
						<code className="font-mono text-[0.92em] whitespace-nowrap text-foreground">loco login --device</code> to
						sign it in as <span className="font-medium text-foreground">{email}</span>.
					</p>
					{linkedCode === null ? (
						<Field label="Device code" error={errorText}>
							<Input
								value={userCode}
								required
								autoComplete="off"
								autoCapitalize="characters"
								spellCheck={false}
								placeholder="XXXX-XXXX"
								className="h-[34px] font-mono tracking-widest uppercase"
								onChange={(event) => {
									setUserCode(event.target.value);
								}}
							/>
						</Field>
					) : (
						<div className="flex flex-col gap-2.5 rounded-[10px] bg-panel px-4 py-3.5">
							<span className="flex items-center gap-2 text-[12.5px] text-fg3">
								<SquareTerminalIcon className="size-[15px]" aria-hidden />
								Check that this matches your terminal
							</span>
							<output className="py-1 text-center font-mono text-[26px] tracking-[0.16em] uppercase">
								{linkedCode}
							</output>
							{errorText !== undefined && (
								<span role="alert" className="text-[12.5px] text-bad-fg">
									{errorText}
								</span>
							)}
						</div>
					)}
					<Button type="submit" size="xl" className="w-full" disabled={approve.isPending || userCode.trim() === ""}>
						{approve.isPending && <Spinner className="size-4" />}
						Approve
					</Button>
				</form>
			)}
		</CliCard>
	);
}
