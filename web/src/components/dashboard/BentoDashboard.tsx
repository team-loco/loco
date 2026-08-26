import { useMemo, useState, useEffect, useRef } from "react";
import {
    useQuery,
    useMutation,
    createQueryOptions,
    useTransport,
} from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { useOrgWorkspace } from "@/context/ContextProvider";
import {
    listEnvironments,
    createEnvironment,
    updateEnvironment,
    deleteEnvironment,
} from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import {
    EnvironmentType,
    type Environment,
} from "@gen/loco/environment/v1/environment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { ResourceType } from "@gen/loco/resource/v1/resource_pb";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import {
    DeploymentPhase,
    type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";
import { toast } from "sonner";
import { toastConnectError } from "@/lib/error-handler";
import { CreateResource } from "@/pages/CreateResource";
import {
    Plus,
    Search,
    X,
    Shield,
    ArrowUpRight,
    Code2,
    Database,
    Globe,
    Layers,
    Zap,
    Menu,
} from "lucide-react";

// ─── Types ─────────────────────────────────────────────────────────────────────

type VisualStatus = "healthy" | "deploying" | "failed" | "not_deployed";

interface BentoDashboardProps {
    resources: Resource[];
    workspaceId?: string | undefined;
}

// ─── Config ────────────────────────────────────────────────────────────────────

const STATUS_CFG: Record<
    VisualStatus,
    {
        dotClass: string;
        textClass: string;
        bgClass: string;
        borderClass: string;
        label: string;
    }
> = {
    healthy: {
        dotClass: "bg-success",
        textClass: "text-success",
        bgClass: "bg-success/10",
        borderClass: "border-success/20",
        label: "Healthy",
    },
    deploying: {
        dotClass: "bg-info",
        textClass: "text-info",
        bgClass: "bg-info/10",
        borderClass: "border-info/20",
        label: "Deploying",
    },
    failed: {
        dotClass: "bg-error",
        textClass: "text-error",
        bgClass: "bg-error/10",
        borderClass: "border-error/20",
        label: "Failed",
    },
    not_deployed: {
        dotClass: "bg-muted-foreground",
        textClass: "text-muted-foreground",
        bgClass: "bg-muted",
        borderClass: "border-border",
        label: "Not deployed",
    },
};

const ENV_TYPE_ORDER: EnvironmentType[] = [
    EnvironmentType.PRODUCTION,
    EnvironmentType.STAGING,
    EnvironmentType.DEV,
    EnvironmentType.UNSPECIFIED,
];

const ENV_TYPE_CFG: Record<
    EnvironmentType,
    { label: string; color: string; bg: string }
> = {
    [EnvironmentType.UNSPECIFIED]: {
        label: "Unknown",
        color: "#7a6a58",
        bg: "#f0ece6",
    },
    [EnvironmentType.DEV]: {
        label: "Dev",
        color: "#2a6b4a",
        bg: "#eaf2ed",
    },
    [EnvironmentType.STAGING]: {
        label: "Staging",
        color: "#9c6b1e",
        bg: "#fdf3e3",
    },
    [EnvironmentType.PRODUCTION]: {
        label: "Production",
        color: "#c0392b",
        bg: "#fdeaea",
    },
};

const RESOURCE_TYPE_CFG: Partial<
    Record<ResourceType, { label: string; icon: React.ReactNode }>
> = {
    [ResourceType.SERVICE]: {
        label: "Service",
        icon: <Globe size={11} className="text-[#a0907e]" />,
    },
    [ResourceType.DATABASE]: {
        label: "Database",
        icon: <Database size={11} className="text-[#a0907e]" />,
    },
    [ResourceType.FUNCTION]: {
        label: "Function",
        icon: <Code2 size={11} className="text-[#a0907e]" />,
    },
    [ResourceType.CACHE]: {
        label: "Cache",
        icon: <Zap size={11} className="text-[#a0907e]" />,
    },
    [ResourceType.QUEUE]: {
        label: "Queue",
        icon: <Layers size={11} className="text-[#a0907e]" />,
    },
    [ResourceType.BLOB]: {
        label: "Storage",
        icon: <Database size={11} className="text-[#a0907e]" />,
    },
};

// ─── Helpers ───────────────────────────────────────────────────────────────────

function phaseToStatus(phase: DeploymentPhase): VisualStatus {
    switch (phase) {
        case DeploymentPhase.RUNNING:
            return "healthy";
        case DeploymentPhase.SUCCEEDED:
            return "healthy";
        case DeploymentPhase.DEPLOYING:
            return "deploying";
        case DeploymentPhase.PENDING:
            return "deploying";
        case DeploymentPhase.FAILED:
            return "failed";
        case DeploymentPhase.CANCELED:
            return "not_deployed";
        case DeploymentPhase.UNSPECIFIED:
            return "not_deployed";
    }
}

function getActiveDeployment(
    deployments: Deployment[],
    envId: string,
): Deployment | null {
    return (
        deployments.find((d) => d.environmentId === envId && d.isActive) ?? null
    );
}

function formatTimeAgo(ts?: { seconds: bigint }): string {
    if (!ts) return "";
    const now = BigInt(Math.floor(Date.now() / 1000));
    const diff = Number(now - ts.seconds);
    if (diff < 0) return "just now";
    if (diff < 60) return `${String(diff)}s ago`;
    if (diff < 3600) return `${String(Math.floor(diff / 60))}m ago`;
    if (diff < 86400) return `${String(Math.floor(diff / 3600))}h ago`;
    return `${String(Math.floor(diff / 86400))}d ago`;
}

function sortEnvironments(envs: Environment[]): Environment[] {
    return [...envs].sort(
        (a, b) =>
            ENV_TYPE_ORDER.indexOf(a.type) - ENV_TYPE_ORDER.indexOf(b.type),
    );
}

// ─── StatusDot ─────────────────────────────────────────────────────────────────

function StatusDot({
    status,
    pulse = false,
}: {
    status: VisualStatus;
    pulse?: boolean;
}) {
    const cfg = STATUS_CFG[status];
    return (
        <span
            className={`inline-block w-[7px] h-[7px] rounded-full shrink-0 ${cfg.dotClass} ${
                pulse && status !== "healthy" ? "animate-pulse" : ""
            }`}
        />
    );
}

// ─── ResourceCard ──────────────────────────────────────────────────────────────

function ResourceCard({
    resource,
    deployment,
    onNavigate,
}: {
    resource: Resource;
    deployment: Deployment | null;
    onNavigate: (id: string) => void;
}) {
    const status: VisualStatus = deployment
        ? phaseToStatus(deployment.status)
        : "not_deployed";
    const cfg = STATUS_CFG[status];
    const typeCfg = RESOURCE_TYPE_CFG[resource.type];
    const image =
        deployment?.spec?.spec.case === "service"
            ? deployment.spec.spec.value.build?.image
            : undefined;

    return (
        <div
            onClick={() => {
                onNavigate(resource.id);
            }}
            className={`bg-card rounded-xl p-4 cursor-pointer transition-all duration-150 border hover:bg-accent hover:-translate-y-px hover:shadow-md hover:border-border ${cfg.borderClass}`}
        >
            {/* Header */}
            <div className="flex items-start justify-between mb-2.5">
                <div className="flex items-center gap-1.5 min-w-0">
                    {typeCfg?.icon}
                    <span className="font-serif text-[15px] truncate">
                        {resource.name}
                    </span>
                </div>
                <span
                    className={`flex items-center gap-1 text-[11px] px-2 py-0.5 rounded-full font-semibold shrink-0 ml-2 ${cfg.bgClass} ${cfg.textClass}`}
                >
                    <StatusDot status={status} pulse />
                    {cfg.label}
                </span>
            </div>

            {/* Image tag */}
            {image && (
                <div className="font-mono text-[10px] text-muted-foreground bg-muted px-2 py-0.5 rounded-[5px] inline-block mb-3 max-w-full overflow-hidden text-ellipsis whitespace-nowrap">
                    {image}
                </div>
            )}

            {/* Footer: region + replicas + time */}
            <div className="flex items-center justify-between mt-auto">
                <div className="flex items-center gap-1.5">
                    {deployment?.region && (
                        <span className="text-[9px] font-mono bg-muted text-muted-foreground px-1.5 py-0.5 rounded">
                            {deployment.region
                                .replace(/-/g, "")
                                .toUpperCase()
                                .slice(0, 6)}
                        </span>
                    )}
                    {deployment && (
                        <span className="text-[9px] font-mono text-muted-foreground">
                            {deployment.replicas}×
                        </span>
                    )}
                </div>
                {deployment?.createdAt && (
                    <span className="text-[10px] text-muted-foreground">
                        {formatTimeAgo(deployment.createdAt)}
                    </span>
                )}
            </div>
        </div>
    );
}

// ─── StatsBar ──────────────────────────────────────────────────────────────────

function StatsBar({
    resources,
    environments,
    deploymentsByResourceId,
    activeEnvId,
}: {
    resources: Resource[];
    environments: Environment[];
    deploymentsByResourceId: Record<string, Deployment[]>;
    activeEnvId: string | null;
}) {
    const services = resources.filter(
        (r) => r.type === ResourceType.SERVICE,
    ).length;
    const databases = resources.filter(
        (r) => r.type === ResourceType.DATABASE,
    ).length;
    const caches = resources.filter(
        (r) => r.type === ResourceType.CACHE,
    ).length;

    const deployed = useMemo(() => {
        if (!activeEnvId) return 0;
        return resources.filter(
            (r) =>
                getActiveDeployment(
                    deploymentsByResourceId[r.id] ?? [],
                    activeEnvId,
                ) !== null,
        ).length;
    }, [resources, deploymentsByResourceId, activeEnvId]);

    const totalReplicas = useMemo(() => {
        if (!activeEnvId) return 0;
        return resources.reduce((acc, r) => {
            const d = getActiveDeployment(
                deploymentsByResourceId[r.id] ?? [],
                activeEnvId,
            );
            return acc + (d?.replicas ?? 0);
        }, 0);
    }, [resources, deploymentsByResourceId, activeEnvId]);

    const stats = [
        {
            label: "Resources",
            value: resources.length,
            sub: `${String(services)} svc · ${String(databases)} db · ${String(caches)} cache`,
        },
        {
            label: "Deployed",
            value: deployed,
            sub: `of ${String(resources.length)} in this env`,
        },
        {
            label: "Replicas",
            value: totalReplicas,
            sub: "across active deployments",
        },
        {
            label: "Environments",
            value: environments.length,
            sub: "in workspace",
        },
    ];

    return (
        <div className="grid grid-cols-4 gap-2.5">
            {stats.map((s) => (
                <div
                    key={s.label}
                    className="bg-[#faf7f2] dark:bg-card border border-[#e8e0d4] dark:border-border rounded-xl p-3.5 transition-all"
                >
                    <div className="flex items-baseline gap-2 mb-1">
                        <span className="font-serif text-2xl tracking-tight text-[#1a1208] dark:text-[#f0ead8]">
                            {s.value}
                        </span>
                        <span className="font-serif text-base tracking-tight text-[#4a3c30] dark:text-[#c8b8a8]">
                            {s.label}
                        </span>
                    </div>
                    <div className="text-[11px] text-[#a0907e] dark:text-[#7a6a58] font-medium uppercase tracking-wide">
                        {s.sub}
                    </div>
                </div>
            ))}
        </div>
    );
}

// ─── EnvModal ──────────────────────────────────────────────────────────────────

function EnvModal({
    mode,
    env,
    isPending,
    onClose,
    onSave,
    onDelete,
}: {
    mode: "create" | "edit" | "delete";
    env?: Environment | undefined;
    isPending: boolean;
    onClose: () => void;
    onSave: (data: {
        name: string;
        type: EnvironmentType;
        description: string;
    }) => void;
    onDelete: (envId: string) => void;
}) {
    const [name, setName] = useState(env?.name ?? "");
    const [desc, setDesc] = useState(env?.description ?? "");
    const [type, setType] = useState<EnvironmentType>(
        env?.type ?? EnvironmentType.DEV,
    );
    const isDelete = mode === "delete";

    const typeOptions: {
        value: EnvironmentType;
        label: string;
        icon: React.ReactNode;
    }[] = [
        {
            value: EnvironmentType.DEV,
            label: "Dev",
            icon: <Code2 size={14} />,
        },
        {
            value: EnvironmentType.STAGING,
            label: "Staging",
            icon: <ArrowUpRight size={14} />,
        },
        {
            value: EnvironmentType.PRODUCTION,
            label: "Production",
            icon: <Shield size={14} />,
        },
    ];

    return (
        <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-background/40 backdrop-blur-sm"
            onClick={onClose}
        >
            <div
                className="bg-card rounded-2xl border border-border shadow-xl w-[440px] overflow-hidden"
                onClick={(e) => {
                    e.stopPropagation();
                }}
            >
                {/* Header */}
                <div className="px-6 py-4 border-b border-border/60 flex items-center justify-between">
                    <span className="font-serif text-lg text-foreground">
                        {mode === "create"
                            ? "New environment"
                            : mode === "edit"
                              ? "Edit environment"
                              : "Delete environment"}
                    </span>
                    <button
                        onClick={onClose}
                        className="text-muted-foreground hover:text-foreground transition-colors"
                    >
                        <X size={17} />
                    </button>
                </div>

                <div className="px-6 py-5">
                    {isDelete ? (
                        <>
                            <div className="p-3.5 bg-error/10 rounded-xl border border-error/20 mb-5">
                                <p className="text-sm text-error leading-relaxed">
                                    This will permanently delete{" "}
                                    <strong className="text-error font-bold">
                                        {env?.name}
                                    </strong>
                                    . This fails if any resources are currently
                                    deployed here.
                                </p>
                            </div>
                            <div className="flex justify-end gap-2">
                                <button
                                    onClick={onClose}
                                    className="px-4 py-2 rounded-lg border border-border text-muted-foreground text-sm font-medium hover:bg-accent transition-colors"
                                >
                                    Cancel
                                </button>
                                <button
                                    onClick={() => {
                                        if (env) onDelete(env.id);
                                    }}
                                    disabled={isPending}
                                    className="px-4 py-2 rounded-lg bg-error text-white text-sm font-medium disabled:opacity-50 hover:bg-error/90 transition-colors"
                                >
                                    Delete environment
                                </button>
                            </div>
                        </>
                    ) : (
                        <>
                            <div className="mb-4">
                                <label className="block text-xs font-semibold text-muted-foreground mb-1.5 uppercase tracking-wider">
                                    Name
                                </label>
                                <input
                                    value={name}
                                    onChange={(e) => {
                                        setName(e.target.value);
                                    }}
                                    maxLength={63}
                                    placeholder="e.g. production, canary, qa"
                                    className="w-full px-3 py-2 border border-border rounded-lg text-sm font-mono text-foreground bg-accent/50 outline-none focus:ring-1 focus:ring-ring focus:border-ring transition-all"
                                />
                            </div>
                            <div className="mb-4">
                                <label className="block text-xs font-semibold text-muted-foreground mb-1.5 uppercase tracking-wider">
                                    Type
                                </label>
                                <div className="flex gap-2">
                                    {typeOptions.map((opt) => {
                                        const cfg = ENV_TYPE_CFG[opt.value];
                                        const isSel = type === opt.value;
                                        return (
                                            <button
                                                key={opt.value}
                                                onClick={() => {
                                                    setType(opt.value);
                                                }}
                                                className={`flex-1 py-2.5 px-2 rounded-lg flex flex-col items-center gap-1 text-xs font-semibold transition-all duration-150 border-2 cursor-pointer ${
                                                    isSel
                                                        ? ""
                                                        : "border-border text-muted-foreground bg-accent/30 hover:border-border-strong hover:bg-accent/50"
                                                }`}
                                                style={
                                                    isSel
                                                        ? {
                                                              borderColor:
                                                                  cfg.color,
                                                              color: cfg.color,
                                                              background: `${cfg.color}15`,
                                                          }
                                                        : undefined
                                                }
                                            >
                                                {opt.icon}
                                                {opt.label}
                                            </button>
                                        );
                                    })}
                                </div>
                            </div>
                            <div className="mb-5">
                                <label className="block text-xs font-semibold text-muted-foreground mb-1.5 uppercase tracking-wider">
                                    Description{" "}
                                    <span className="font-normal lowercase text-muted-foreground/60 italic">
                                        (optional)
                                    </span>
                                </label>
                                <textarea
                                    value={desc}
                                    onChange={(e) => {
                                        setDesc(e.target.value);
                                    }}
                                    maxLength={256}
                                    rows={2}
                                    placeholder="What is this environment used for?"
                                    className="w-full px-3 py-2 border border-border rounded-lg text-sm text-foreground bg-accent/50 outline-none focus:ring-1 focus:ring-ring focus:border-ring transition-all resize-none"
                                />
                            </div>
                            <div className="flex justify-end gap-2">
                                <button
                                    onClick={onClose}
                                    className="px-4 py-2 rounded-lg border border-border text-muted-foreground text-sm font-medium hover:bg-accent transition-colors cursor-pointer"
                                >
                                    Cancel
                                </button>
                                <button
                                    onClick={() => {
                                        onSave({
                                            name,
                                            type,
                                            description: desc,
                                        });
                                    }}
                                    disabled={!name.trim() || isPending}
                                    className="px-4 py-2 rounded-lg bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50 hover:opacity-90 transition-colors cursor-pointer"
                                >
                                    {mode === "create"
                                        ? "Create environment"
                                        : "Save changes"}
                                </button>
                            </div>
                        </>
                    )}
                </div>
            </div>
        </div>
    );
}

// ─── EnvPillSwitcher ───────────────────────────────────────────────────────────

function EnvPillSwitcher({
    environments,
    activeEnvId,
    onSelect,
    onCreateEnv,
    onEditEnv,
    onDeleteEnv,
}: {
    environments: Environment[];
    activeEnvId: string | null;
    onSelect: (envId: string) => void;
    onCreateEnv: () => void;
    onEditEnv: (env: Environment) => void;
    onDeleteEnv: (env: Environment) => void;
}) {
    const [openMenuId, setOpenMenuId] = useState<string | null>(null);
    const ref = useRef<HTMLDivElement>(null);

    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (ref.current && !ref.current.contains(e.target as Node)) {
                setOpenMenuId(null);
            }
        };
        document.addEventListener("mousedown", handler);
        return () => {
            document.removeEventListener("mousedown", handler);
        };
    }, []);

    return (
        <div
            ref={ref}
            className="inline-flex items-center gap-0.5 p-[3px] bg-accent rounded-lg"
        >
            {environments.map((env) => {
                const isActive = activeEnvId === env.id;
                const menuOpen = openMenuId === env.id;
                const cfg = ENV_TYPE_CFG[env.type];

                return (
                    <div key={env.id} className="relative flex items-center">
                        <button
                            onClick={() => {
                                onSelect(env.id);
                                setOpenMenuId(null);
                            }}
                            className={`group pl-3 pr-1.5 py-1.5 rounded-lg text-[13px] font-sans transition-all duration-150 whitespace-nowrap flex items-center gap-2 cursor-pointer border ${
                                isActive
                                    ? "bg-card text-foreground shadow-sm border-border/50"
                                    : "bg-transparent text-muted-foreground hover:bg-accent/50 border-transparent"
                            }`}
                        >
                            <div className="flex items-center gap-1.5 font-medium">
                                <span
                                    className="w-1.5 h-1.5 rounded-full shrink-0"
                                    style={{ background: cfg.color }}
                                />
                                {env.name}
                            </div>

                            <div
                                onClick={(e) => {
                                    e.stopPropagation();
                                    setOpenMenuId(menuOpen ? null : env.id);
                                }}
                                className={`w-5 h-5 rounded-md flex items-center justify-center transition-all cursor-pointer shrink-0 ${
                                    menuOpen
                                        ? "bg-accent text-foreground"
                                        : "text-muted-foreground/60 hover:bg-accent hover:text-foreground"
                                }`}
                            >
                                <Menu size={12} />
                            </div>
                        </button>

                        {/* Dropdown */}
                        {menuOpen && (
                            <div className="absolute top-[calc(100%+8px)] left-0 bg-card border border-border rounded-xl shadow-xl z-50 min-w-[148px] overflow-hidden">
                                <button
                                    onClick={(e) => {
                                        e.stopPropagation();
                                        onEditEnv(env);
                                        setOpenMenuId(null);
                                    }}
                                    className="flex items-center gap-2 w-full px-3.5 py-2.5 hover:bg-accent text-[13px] text-foreground font-medium transition-colors"
                                >
                                    <svg
                                        width="13"
                                        height="13"
                                        viewBox="0 0 24 24"
                                        fill="none"
                                        stroke="currentColor"
                                        strokeWidth="2"
                                    >
                                        <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
                                        <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
                                    </svg>
                                    Edit
                                </button>
                                <div className="h-px bg-border/60 mx-2.5" />
                                <button
                                    onClick={(e) => {
                                        e.stopPropagation();
                                        onDeleteEnv(env);
                                        setOpenMenuId(null);
                                    }}
                                    className="flex items-center gap-2 w-full px-3.5 py-2.5 hover:bg-error/10 text-[13px] text-error font-medium transition-colors"
                                >
                                    <svg
                                        width="13"
                                        height="13"
                                        viewBox="0 0 24 24"
                                        fill="none"
                                        stroke="currentColor"
                                        strokeWidth="2"
                                    >
                                        <polyline points="3 6 5 6 21 6" />
                                        <path d="M19 6l-1 14H6L5 6" />
                                        <path d="M10 11v6M14 11v6" />
                                        <path d="M9 6V4h6v2" />
                                    </svg>
                                    Delete
                                </button>
                            </div>
                        )}
                    </div>
                );
            })}

            {/* Divider + New */}
            <div className="w-px h-[18px] bg-foreground/20 mx-1" />
            <button
                onClick={onCreateEnv}
                className="flex items-center gap-1.5 pr-3 pl-1 py-1.5 rounded-lg text-[13px] text-muted-foreground font-medium hover:bg-accent text-foreground transition-all duration-150 whitespace-nowrap cursor-pointer"
            >
                <Plus size={11} strokeWidth={2.5} className="cursor-pointer" />
                New env
            </button>
        </div>
    );
}

// ─── BentoDashboard ────────────────────────────────────────────────────────────

export function BentoDashboard({
    resources,
    workspaceId,
}: BentoDashboardProps) {
    const navigate = useNavigate();
    const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
    const transport = useTransport();
    const wsId = workspaceId ?? activeWorkspaceId ?? "";

    // ── State ──
    // null = use auto-default; string = user has explicitly selected
    const [selectedEnvId, setSelectedEnvId] = useState<string | null>(null);
    const [search, setSearch] = useState("");
    const [envModal, setEnvModal] = useState<{
        mode: "create" | "edit" | "delete";
        env?: Environment;
    } | null>(null);
    const [showCreateModal, setShowCreateModal] = useState(false);

    // ── Fetch environments ──
    const { data: envsData, refetch: refetchEnvs } = useQuery(
        listEnvironments,
        wsId ? { workspaceId: wsId } : undefined,
        { enabled: !!wsId },
    );

    const environments = useMemo(
        () => sortEnvironments(envsData?.environments ?? []),
        [envsData],
    );

    // ── Derive active env: explicit selection → Production → Staging → Dev → first ──
    const activeEnvId = useMemo(() => {
        if (selectedEnvId) return selectedEnvId;
        if (!environments.length) return null;
        const prod = environments.find(
            (e) => e.type === EnvironmentType.PRODUCTION,
        );
        const stg = environments.find(
            (e) => e.type === EnvironmentType.STAGING,
        );
        const dev = environments.find((e) => e.type === EnvironmentType.DEV);
        return (prod ?? stg ?? dev ?? environments[0])?.id ?? null;
    }, [selectedEnvId, environments]);

    // ── Fetch deployments per resource ──
    const deploymentQueries = useQueries({
        queries: resources.map((r) =>
            createQueryOptions(
                listDeployments,
                { resourceId: r.id, pageSize: 50 },
                { transport },
            ),
        ),
    });

    const deploymentsByResourceId = useMemo(() => {
        const map: Record<string, Deployment[]> = {};
        resources.forEach((r, i) => {
            map[r.id] = deploymentQueries[i]?.data?.deployments ?? [];
        });
        return map;
    }, [resources, deploymentQueries]);

    // ── Env CRUD mutations ──
    const createEnvMutation = useMutation(createEnvironment, {
        onSuccess: () => {
            void refetchEnvs();
            toast.success("Environment created");
            setEnvModal(null);
        },
        onError: (error: Error) => {
            toastConnectError(error);
        },
    });

    const updateEnvMutation = useMutation(updateEnvironment, {
        onSuccess: () => {
            void refetchEnvs();
            toast.success("Environment updated");
            setEnvModal(null);
        },
        onError: (error: Error) => {
            toastConnectError(error);
        },
    });

    const deleteEnvMutation = useMutation(deleteEnvironment, {
        onSuccess: (_, vars) => {
            void refetchEnvs();
            if (activeEnvId === vars.environmentId) setSelectedEnvId(null);
            toast.success("Environment deleted");
            setEnvModal(null);
        },
        onError: (error: Error) => {
            toastConnectError(error);
        },
    });

    const envMutationPending =
        createEnvMutation.isPending ||
        updateEnvMutation.isPending ||
        deleteEnvMutation.isPending;

    // ── Search filter ──
    const filteredResources = useMemo(() => {
        const q = search.trim().toLowerCase();
        if (!q) return resources;
        return resources.filter(
            (r) =>
                r.name.toLowerCase().includes(q) ||
                RESOURCE_TYPE_CFG[r.type]?.label.toLowerCase().includes(q),
        );
    }, [resources, search]);

    const handleNavigate = (resourceId: string) => {
        if (activeOrgId && activeWorkspaceId) {
            void navigate(
                `/org/${activeOrgId}/wks/${activeWorkspaceId}/resource/${resourceId}`,
            );
        }
    };

    return (
        <div className="relative w-[95%] mx-auto space-y-4">
            {/* Header row: env switcher + new resource button */}
            <div className="flex items-center justify-between flex-wrap gap-3">
                <EnvPillSwitcher
                    environments={environments}
                    activeEnvId={activeEnvId}
                    onSelect={setSelectedEnvId}
                    onCreateEnv={() => {
                        setEnvModal({ mode: "create" });
                    }}
                    onEditEnv={(env) => {
                        setEnvModal({ mode: "edit", env });
                    }}
                    onDeleteEnv={(env) => {
                        setEnvModal({ mode: "delete", env });
                    }}
                />
                <button
                    onClick={() => {
                        setShowCreateModal(true);
                    }}
                    className="flex items-center gap-1.5 px-3.5 py-2 rounded-lg bg-[#2a2018] text-[#f7f3ec] text-[13px] font-medium hover:bg-[#3a3028] transition-colors cursor-pointer"
                >
                    <Plus size={12} strokeWidth={2.5} />
                    New Resource
                </button>
            </div>

            {/* Stats bar */}
            <StatsBar
                resources={resources}
                environments={environments}
                deploymentsByResourceId={deploymentsByResourceId}
                activeEnvId={activeEnvId}
            />

            {/* Search row */}
            <div className="flex items-center">
                <div className="relative w-[400px] shrink-0">
                    <Search
                        size={14}
                        className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground/50 pointer-events-none"
                    />
                    <input
                        value={search}
                        onChange={(e) => {
                            setSearch(e.target.value);
                        }}
                        placeholder="Search resources…"
                        className="w-full h-9 pl-9 pr-3 bg-card border border-border rounded-lg text-[13px] text-foreground outline-none focus:ring-1 focus:ring-ring focus:border-ring transition-all"
                    />
                    {search && (
                        <button
                            onClick={() => {
                                setSearch("");
                            }}
                            className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                        >
                            <X size={14} />
                        </button>
                    )}
                </div>
            </div>

            {/* Resource grid */}
            {filteredResources.length === 0 ? (
                <div className="text-center py-12 text-[#a0907e] text-sm">
                    {search
                        ? "No resources match your search"
                        : "No resources in this workspace"}
                </div>
            ) : (
                <div className="grid grid-cols-3 gap-3">
                    {filteredResources.map((r) => (
                        <ResourceCard
                            key={r.id}
                            resource={r}
                            deployment={
                                activeEnvId
                                    ? getActiveDeployment(
                                          deploymentsByResourceId[r.id] ?? [],
                                          activeEnvId,
                                      )
                                    : null
                            }
                            onNavigate={handleNavigate}
                        />
                    ))}
                </div>
            )}

            {/* Env modal */}
            {showCreateModal && (
                <CreateResource
                    onClose={() => {
                        setShowCreateModal(false);
                    }}
                />
            )}
            {envModal && (
                <EnvModal
                    mode={envModal.mode}
                    env={envModal.env}
                    isPending={envMutationPending}
                    onClose={() => {
                        setEnvModal(null);
                    }}
                    onSave={(data) => {
                        if (envModal.mode === "create") {
                            createEnvMutation.mutate({
                                workspaceId: wsId,
                                name: data.name,
                                type: data.type,
                                description: data.description || undefined,
                            });
                        } else {
                            updateEnvMutation.mutate({
                                environmentId: envModal.env?.id ?? "",
                                name: data.name,
                                type: data.type,
                                description: data.description || undefined,
                            });
                        }
                    }}
                    onDelete={(envId) => {
                        deleteEnvMutation.mutate({ environmentId: envId });
                    }}
                />
            )}
        </div>
    );
}
