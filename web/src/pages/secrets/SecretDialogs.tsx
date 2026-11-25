import { useMutation } from "@connectrpc/connect-query";
import { setSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import { EyeIcon, EyeOffIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "@/lib/toast";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Textarea } from "@/components/design/Textarea";
import { getErrorMessage } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

import { DotEnvNotes } from "@/components/DotEnvNotes";
import { importPreview, secretNameProblem } from "./secrets";
import { InlineCode, InlineCodeList, withCode } from "@/components/design/InlineCode";
import { referenceOf } from "@/pages/resource/variables";

/** Creates a secret, or with name set replaces the value of that secret. */
export function SecretDialog({
	open,
	environmentId,
	environmentName,
	name: fixedName,
	users,
	existing,
	onClose,
	onSaved,
}: {
	open: boolean;
	environmentId: string;
	environmentName: string;
	name: string | null;
	users: string[];
	existing: ReadonlySet<string>;
	onClose: () => void;
	onSaved: () => void;
}) {
	const [name, setName] = useState("");
	const [value, setValue] = useState("");
	const [shown, setShown] = useState(false);
	const save = useMutation(setSecrets);
	const updating = fixedName !== null;
	const current = fixedName ?? name;
	const problem = updating ? "" : secretNameProblem(name, existing.has(name));
	const reset = () => {
		setName("");
		setValue("");
		setShown(false);
		save.reset();
	};
	const submit = () => {
		if (current === "" || problem !== "" || value === "") return;
		save.mutate(
			{ environmentId, values: { [current]: value } },
			{
				onSuccess: () => {
					toast.success(updating ? withCode`Updated ${current}` : withCode`Created ${current}`);
					reset();
					onSaved();
				},
			},
		);
	};
	const eyeLabel = shown ? "Hide value" : "Show what you typed";
	return (
		<Dialog
			open={open}
			onOpenChange={(next: boolean) => {
				if (!next) {
					reset();
					onClose();
				}
			}}
		>
			<DialogContent
				title={
					updating ? (
						<>
							Update <InlineCode>{current}</InlineCode>
						</>
					) : (
						<>
							New secret in <InlineCode>{environmentName}</InlineCode>
						</>
					)
				}
				showCloseButton={false}
			>
				<form
					noValidate
					onSubmit={(e) => {
						e.preventDefault();
						submit();
					}}
				>
					<DialogBody>
						{!updating && (
							<Field
								label="Name"
								error={
									problem === "" ? undefined : problem === "taken" ? (
										<>
											<InlineCode>{name}</InlineCode> already exists in <InlineCode>{environmentName}</InlineCode>.
										</>
									) : (
										"Use capital letters, digits and underscores, not starting with a digit."
									)
								}
							>
								<Input
									className="font-mono"
									value={name}
									placeholder="STRIPE_SECRET_KEY"
									spellCheck={false}
									autoComplete="off"
									aria-invalid={problem !== ""}
									onChange={(e) => {
										setName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"));
									}}
								/>
							</Field>
						)}
						<Field label="Value">
							<span className="relative flex items-center">
								<Input
									className="pr-9 font-mono"
									type={shown ? "text" : "password"}
									autoComplete="new-password"
									value={value}
									placeholder={updating ? "Type the new value" : "value"}
									onChange={(e) => {
										setValue(e.target.value);
									}}
								/>
								<Button
									type="button"
									variant="ghost"
									size="icon-xs"
									className="absolute right-1.5"
									aria-label={eyeLabel}
									title={eyeLabel}
									disabled={value === ""}
									onClick={() => {
										setShown((s) => !s);
									}}
								>
									{shown ? <EyeOffIcon /> : <EyeIcon />}
								</Button>
							</span>
						</Field>
						<div
							role="note"
							data-secret-warn=""
							className="flex items-start gap-2 rounded-[6px] bg-[color-mix(in_oklab,var(--warn)_12%,transparent)] px-3 py-[9px] text-[12.5px] leading-[1.45] text-foreground"
						>
							<StatusGlyph glyph="warn" className="mt-px" />
							<span>You won’t be able to see this value again after you save.</span>
						</div>
						<span className="text-[12.5px] text-fg3">
							{!updating ? (
								<>
									Refer to it from a resource’s variables as{" "}
									<InlineCode>{referenceOf("secrets", current === "" ? "NAME" : current)}</InlineCode>.
								</>
							) : users.length === 0 ? (
								"No resource refers to it yet."
							) : (
								<>
									<InlineCodeList names={users} /> {users.length === 1 ? "gets" : "get"} the new value on{" "}
									{users.length === 1 ? "its" : "their"} next deploy or restart.
								</>
							)}
						</span>
						{save.error !== null && (
							<span role="alert" className="text-[12.5px] text-bad-fg">
								{getErrorMessage(save.error, "Couldn't save the secret")}
							</span>
						)}
					</DialogBody>
					<DialogFooter>
						<DialogClose render={<Button variant="outline" type="button" />}>Cancel</DialogClose>
						<Button type="submit" disabled={current === "" || problem !== "" || value === "" || save.isPending}>
							{updating ? "Update" : "Create secret"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

export function ImportDialog({
	open,
	environmentId,
	environmentName,
	existing,
	onClose,
	onSaved,
}: {
	open: boolean;
	environmentId: string;
	environmentName: string;
	existing: ReadonlySet<string>;
	onClose: () => void;
	onSaved: () => void;
}) {
	const [text, setText] = useState("");
	const save = useMutation(setSecrets);
	const preview = importPreview(text, existing);
	const count = Object.keys(preview.values).length;
	const ready = count > 0 && preview.invalid.length === 0;
	return (
		<Dialog
			open={open}
			onOpenChange={(next: boolean) => {
				if (!next) {
					setText("");
					save.reset();
					onClose();
				}
			}}
		>
			<DialogContent title={`Import .env into ${environmentName}`} showCloseButton={false}>
				<form
					noValidate
					onSubmit={(e) => {
						e.preventDefault();
						if (!ready) return;
						save.mutate(
							{ environmentId, values: preview.values },
							{
								onSuccess: () => {
									toast.success(`Imported ${count.toString()} secrets`);
									setText("");
									onSaved();
								},
							},
						);
					}}
				>
					<DialogBody>
						<span className="text-fg2">Each line becomes a secret. Names that exist get a new version.</span>
						<Textarea
							aria-label=".env contents"
							className="resize-y font-mono text-[12.5px]"
							rows={8}
							spellCheck={false}
							placeholder="DATABASE_URL=postgres://…"
							value={text}
							onChange={(e) => {
								setText(e.target.value);
							}}
						/>
						<span className={cn("font-mono text-[12px]", preview.invalid.length > 0 ? "text-bad-fg" : "text-fg3")}>
							{preview.summary}
						</span>
						<DotEnvNotes notes={preview.notes} />
						{save.error !== null && (
							<span role="alert" className="text-[12.5px] text-bad-fg">
								{getErrorMessage(save.error, "Couldn't import the secrets")}
							</span>
						)}
					</DialogBody>
					<DialogFooter>
						<DialogClose render={<Button variant="outline" type="button" />}>Cancel</DialogClose>
						<Button type="submit" disabled={!ready || save.isPending}>
							{count > 0 ? `Import ${count.toString()}` : "Import"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
