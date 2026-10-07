import { useMutation } from "@connectrpc/connect-query";
import { EyeIcon, EyeOffIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { updateResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { toastConnectError } from "@/lib/error-handler";

import { validateEnv, type EnvPair } from "./env";
import type { Notice } from "./model";

interface DraftRow {
 id: number;
 key: string;
 value: string;
 revealed: boolean;
}

let nextId = 1;
const newRow = (): DraftRow => ({ id: nextId++, key: "", value: "", revealed: false });

export function VariablesTab({ resourceId, resourceName, variableKeys, regionNames, hasDeployment, onNotice, onSaved }: {
 resourceId: string;
 resourceName: string;
 variableKeys: string[];
 regionNames: string[];
 hasDeployment: boolean;
 onNotice: (notice: Notice) => void;
 onSaved: () => void;
}) {
 const [draft, setDraft] = useState<DraftRow[] | null>(null);
 const [error, setError] = useState<string | undefined>();
 const [invalid, setInvalid] = useState<number | undefined>();
 const save = useMutation(updateResourceEnv);
 const keys = [...variableKeys].sort((a, b) => a.localeCompare(b));
 const cancel = () => { setDraft(null); setError(undefined); setInvalid(undefined); };
 const update = (id: number, patch: Partial<DraftRow>) => { setDraft((rows) => rows?.map((row) => row.id === id ? { ...row, ...patch } : row) ?? null); };
 const submit = (event: React.SubmitEvent<HTMLFormElement>) => {
  event.preventDefault();
  if (save.isPending) return;
  const pairs: EnvPair[] = (draft ?? []).map((row) => [row.key.trim(), row.value]);
  const problem = validateEnv(pairs);
  setError(problem);
  if (problem !== undefined) {
   const index = pairs.findIndex((_, position) => validateEnv(pairs.slice(0, position + 1)) !== undefined);
   setInvalid(index);
   const row = draft?.[index];
   if (row !== undefined) document.getElementById(`variable-key-${row.id.toString()}`)?.focus();
   return;
  }
  setInvalid(undefined);
  save.mutate({ resourceId, env: Object.fromEntries(pairs) }, {
   onSuccess: () => {
    cancel(); onSaved();
    onNotice({ tone: "info", title: `Updating variables on ${resourceName}`, message: `New deployments scheduled in ${regionNames.join(", ")}. Existing variables keep their values unless replaced.` });
   },
   onError: (failure) => { toastConnectError(failure, "Failed to update variables"); },
  });
 };
 return (
  <Section title="Environment variables" actions={draft === null ? (
   <Button variant="outline" className="h-[30px]" disabled={!hasDeployment} title={hasDeployment ? undefined : "Deploy before setting variables here"} onClick={() => { setDraft([newRow()]); }}>Set variables</Button>
  ) : undefined}>
   <p className="border-b border-line px-4 py-3 text-sm text-fg3">Values are write only. Set a name again to replace its value. Changes to variables managed by a Go definition appear in the next infrastructure plan.</p>
   {keys.length === 0 ? <EmptyState title="No environment variables">Declare variables in your Go definition or set them after the first deployment.</EmptyState> : keys.map((key) => (
    <div key={key} className="grid grid-cols-2 gap-3 border-b border-line px-4 py-3 font-mono text-sm">
     <span className="break-all font-semibold">{key}</span><span className="text-fg3" aria-label="Value hidden">••••••••</span>
    </div>
   ))}
   {draft !== null && (
    <form noValidate onSubmit={submit} className="space-y-4 p-4">
     {draft.map((row, index) => (
      <div key={row.id} className="flex flex-wrap items-end gap-3">
       <Field label="Name" className="min-w-0 flex-1">
        <Input id={`variable-key-${row.id.toString()}`} value={row.key} autoComplete="off" aria-invalid={invalid === index} aria-describedby={invalid === index ? "variable-error" : undefined} onChange={(event) => { update(row.id, { key: event.target.value }); }} />
       </Field>
       <Field label="New value" className="min-w-0 flex-1">
        <Input type={row.revealed ? "text" : "password"} value={row.value} autoComplete="new-password" onChange={(event) => { update(row.id, { value: event.target.value }); }} />
       </Field>
       <Button type="button" variant="ghost" size="icon-sm" aria-label={row.revealed ? "Hide new value" : "Show new value"} onClick={() => { update(row.id, { revealed: !row.revealed }); }}>{row.revealed ? <EyeOffIcon /> : <EyeIcon />}</Button>
       <Button type="button" variant="ghost" size="icon-sm" aria-label="Remove draft variable" onClick={() => { setDraft((rows) => rows?.filter((item) => item.id !== row.id) ?? null); }}><Trash2Icon /></Button>
      </div>
     ))}
     {error !== undefined && <p id="variable-error" role="alert" className="text-sm text-bad-fg">{error}</p>}
     <div className="flex flex-wrap items-center gap-2">
      <Button type="button" variant="ghost" onClick={() => { setDraft((rows) => [...(rows ?? []), newRow()]); }}><PlusIcon />Add variable</Button>
      <Button type="button" variant="outline" disabled={save.isPending} onClick={cancel}>Cancel</Button>
      <Button type="submit" disabled={save.isPending}>{save.isPending ? "Saving…" : "Save and redeploy"}</Button>
     </div>
    </form>
   )}
  </Section>
 );
}
