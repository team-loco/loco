import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AppType, createApp } from "@/gen/app/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";

const APP_TYPES = [
	{ value: "SERVICE", label: "Service", description: "Backend service or API" },
	{ value: "DATABASE", label: "Database", description: "Managed database" },
	{ value: "FUNCTION", label: "Function", description: "Serverless function" },
	{ value: "CACHE", label: "Cache", description: "In-memory cache" },
	{ value: "QUEUE", label: "Queue", description: "Message queue" },
	{ value: "BLOB", label: "Blob Storage", description: "Object storage" },
];

export function CreateApp() {
	const navigate = useNavigate();
	const { workspaceId: paramWorkspaceId } = useParams();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");

	const [appName, setAppName] = useState("");
	const [appType, setAppType] = useState("SERVICE");
	const [subdomain, setSubdomain] = useState("");

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const orgs = orgsRes?.orgs ?? [];
	const firstOrgId = orgs.length > 0 ? orgs[0].id : null;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);
	const workspaces = workspacesRes?.workspaces ?? [];
	const workspaceId =
		paramWorkspaceId || (workspaces.length > 0 ? workspaces[0].id : null);

	const createAppMutation = useMutation(createApp);

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();

		if (!appName.trim()) {
			toast.error("App name is required");
			return;
		}

		if (!workspaceId) {
			toast.error("No workspace available");
			return;
		}

		try {
			const res = await createAppMutation.mutateAsync({
				name: appName,
				workspaceId:
					typeof workspaceId === "string" ? BigInt(workspaceId) : workspaceId,
				type: AppType[appType as keyof typeof AppType],
				subdomain: subdomain || undefined,
			});

			if (res.app?.id) {
				toast.success("App created successfully");
				navigate(`/app/${res.app.id}${workspaceFromUrl ? `?workspace=${workspaceFromUrl}` : ""}`);
			} else {
				toast.error("Failed to create app");
			}
		} catch (error) {
			const message =
				error instanceof Error ? error.message : "Failed to create app";
			toast.error(message);
		}
	};

	return (
		<div className="max-w-2xl mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl font-heading text-foreground mb-2">
					Create App
				</h1>
				<p className="text-muted-foreground">
					Set up a new application or service
				</p>
			</div>

			<form onSubmit={handleSubmit} className="space-y-6">
				{/* App Name */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">App Name</CardTitle>
					</CardHeader>
					<CardContent>
						<Label htmlFor="app-name" className="text-sm mb-2 block">
							Name
						</Label>
						<Input
							id="app-name"
							placeholder="e.g., API v2, Database, Worker"
							value={appName}
							onChange={(e) => setAppName(e.target.value)}
							className="border-border"
						/>
					</CardContent>
				</Card>

				{/* App Type */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">App Type</CardTitle>
						<CardDescription>
							Choose what kind of app you're deploying
						</CardDescription>
					</CardHeader>
					<CardContent>
						<div className="grid grid-cols-2 gap-3">
							{APP_TYPES.map((type) => (
								<button
									key={type.value}
									type="button"
									onClick={() => setAppType(type.value)}
									className={`p-4 rounded-neo border-2 text-left transition-all ${
										appType === type.value
											? "border-main bg-main/5"
											: "border-border hover:border-main/50"
									}`}
								>
									<div className="font-medium text-sm text-foreground">
										{type.label}
									</div>
									<div className="text-xs text-muted-foreground mt-1">
										{type.description}
									</div>
								</button>
							))}
						</div>
					</CardContent>
				</Card>

				{/* Subdomain */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">Subdomain</CardTitle>
						<CardDescription>Your app's URL on deploy-app.com</CardDescription>
					</CardHeader>
					<CardContent>
						<div className="flex gap-2">
							<Input
								placeholder="my-app"
								value={subdomain}
								onChange={(e) =>
									setSubdomain(
										e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "")
									)
								}
								className="border-border flex-1"
							/>
							<div className="flex items-center px-3 bg-secondary rounded-neo border border-border text-sm text-muted-foreground shrink-0">
								.deploy-app.com
							</div>
						</div>
					</CardContent>
				</Card>

				{/* Actions */}
				<div className="flex gap-3 justify-end">
					<Button
						type="button"
						variant="neutral"
						onClick={() => navigate("/dashboard")}
					>
						Cancel
					</Button>
					<Button
						type="submit"
						disabled={createAppMutation.isPending || !appName.trim()}
					>
						{createAppMutation.isPending ? "Creating..." : "Create App"}
					</Button>
				</div>
			</form>
		</div>
	);
}
