import { CheckIcon } from "lucide-react";

import { CodeBlock } from "@/components/design/CodeBlock";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/design/Tabs";

const SNIPPETS: { language: string; code: string }[] = [
	{
		language: "Node",
		code: [
			"npm install @opentelemetry/auto-instrumentations-node",
			"node --require @opentelemetry/auto-instrumentations-node/register app.js",
		].join("\n"),
	},
	{
		language: "Python",
		code: [
			"pip install opentelemetry-distro opentelemetry-exporter-otlp",
			"opentelemetry-bootstrap -a install",
			"opentelemetry-instrument python app.py",
		].join("\n"),
	},
	{
		language: "Go",
		code: [
			"go get go.opentelemetry.io/otel/sdk go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp \\",
			"  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp",
			"",
			"// main.go",
			"exporter, err := otlptracehttp.New(ctx)",
			"otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter)))",
			'http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "server"))',
		].join("\n"),
	},
	{
		language: "Java",
		code: [
			"curl -LO https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/latest/download/opentelemetry-javaagent.jar",
			"java -javaagent:opentelemetry-javaagent.jar -jar app.jar",
		].join("\n"),
	},
];

export function TraceSetup() {
	return (
		<Tabs
			defaultValue={SNIPPETS[0]?.language}
			className="w-[min(560px,100%)] overflow-hidden rounded-lg border border-line2 bg-background text-left"
		>
			<TabsList aria-label="Language" className="px-2">
				{SNIPPETS.map((s) => (
					<TabsTrigger key={s.language} value={s.language} className="text-[12.5px]">
						{s.language}
					</TabsTrigger>
				))}
			</TabsList>
			{SNIPPETS.map((s) => (
				<TabsContent key={s.language} value={s.language}>
					<CodeBlock className="rounded-none border-0">{s.code}</CodeBlock>
				</TabsContent>
			))}
			<p className="m-0 flex items-center gap-2 border-t border-line px-3.5 py-2 font-mono text-[11.5px] text-fg2">
				<CheckIcon aria-hidden className="size-3.5 shrink-0 text-ok-fg" />
				OTEL_EXPORTER_OTLP_ENDPOINT is already set on every replica
			</p>
		</Tabs>
	);
}
