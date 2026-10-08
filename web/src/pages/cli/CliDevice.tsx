import { useMutation } from "@connectrpc/connect-query";
import { Loader2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";

import { approveDeviceLogin } from "@gen/loco/auth/v1/auth-AuthService_connectquery";

import { forgetNextPath } from "@/auth/next";
import { Button } from "@/components/design/Button";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { getErrorMessage } from "@/lib/error-handler";

import { CliCard } from "./CliCard";

export function CliDevice() {
	const [params] = useSearchParams();
	const [userCode, setUserCode] = useState(params.get("code") ?? "");
	const approve = useMutation(approveDeviceLogin);

	const submit = (event: FormEvent) => {
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

	if (approve.isSuccess) {
		return (
			<CliCard title="Device signed in">
				{() => <p className="m-0 text-center text-fg2">You can return to your terminal.</p>}
			</CliCard>
		);
	}

	return (
		<CliCard title="Sign in a device">
			{(email) => (
				<form className="flex flex-col gap-4" onSubmit={submit}>
					<p className="m-0 text-center text-fg2">
						Enter the code shown by <code>loco login --device</code> to sign that device in as{" "}
						<span className="font-semibold text-foreground">{email}</span>.
					</p>
					<Field
						label="Device code"
						error={approve.error !== null ? getErrorMessage(approve.error, "Could not sign in the device") : undefined}
					>
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
					<Button type="submit" variant="inverted" size="lg" className="w-full" disabled={approve.isPending || userCode.trim() === ""}>
						{approve.isPending && <Loader2 className="size-4 animate-spin" />}
						Approve
					</Button>
				</form>
			)}
		</CliCard>
	);
}
